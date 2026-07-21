[CmdletBinding()]
param(
  [string] $Image = "",
  [switch] $SkipImage
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"
. "$PSScriptRoot\release-tools.ps1"

$root = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$runID = "supply-$([guid]::NewGuid().ToString('N'))"
$workDirectory = [IO.Path]::GetFullPath((Join-Path $root ".build\$runID"))
$scanDirectory = Join-Path $workDirectory "source"
$evidenceDirectory = Join-Path $root ".build\acceptance-evidence"
$reportPath = Join-Path $evidenceDirectory "supply-chain-report.json"
$builtImage = $false
$failure = $null
$docker = Get-LLMGatewayDockerCommand
$pnpmCommand = if ($env:OS -eq "Windows_NT") { "pnpm.cmd" } else { "pnpm" }
$report = [ordered]@{
  govulncheck = $false
  nodeAudit = $false
  goLicenses = $false
  nodeLicenses = $false
  currentSourceSecrets = $false
  imageVulnerabilities = $false
  imageConfiguration = $false
  image = ""
}

Push-Location $root
try {
  New-Item -ItemType Directory -Force -Path $scanDirectory, $evidenceDirectory | Out-Null

  & go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
  if ($LASTEXITCODE -ne 0) { throw "govulncheck found a reachable vulnerability." }
  $report.govulncheck = $true

  & $pnpmCommand --dir web audit --audit-level high
  if ($LASTEXITCODE -ne 0) { throw "pnpm audit found a high or critical vulnerability." }
  $report.nodeAudit = $true

  & go run github.com/google/go-licenses/v2@v2.0.1 check --include_tests ./...
  if ($LASTEXITCODE -ne 0) { throw "Go dependencies contain a forbidden or unknown license." }
  $report.goLicenses = $true

  $nodeLicenses = & $pnpmCommand --dir web licenses list --json | ConvertFrom-Json
  if ($LASTEXITCODE -ne 0) { throw "Could not inventory Node dependency licenses." }
  $licenseNames = @($nodeLicenses.PSObject.Properties | Select-Object -ExpandProperty Name)
  $forbiddenNodeLicenses = @($licenseNames | Where-Object { $_ -match '(?i)(AGPL|(^|[^L])GPL|LGPL|SSPL|BUSL|UNLICENSED|UNKNOWN)' })
  if ($forbiddenNodeLicenses.Count -gt 0) {
    throw "Node dependencies contain forbidden or unknown licenses: $($forbiddenNodeLicenses -join ', ')"
  }
  $report.nodeLicenses = $true

  $sourceFiles = @(& git ls-files --cached --others --exclude-standard)
  if ($LASTEXITCODE -ne 0 -or $sourceFiles.Count -eq 0) { throw "Could not enumerate the current non-ignored source tree." }
  $rootPrefix = $root.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
  foreach ($relativePath in $sourceFiles) {
    $sourcePath = [IO.Path]::GetFullPath((Join-Path $root $relativePath))
    if (-not $sourcePath.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase) -or -not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
      throw "Refusing to stage a supply-chain scan source outside the repository: $relativePath"
    }
    $destination = [IO.Path]::GetFullPath((Join-Path $scanDirectory $relativePath))
    if (-not $destination.StartsWith($scanDirectory + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
      throw "Refusing to stage an invalid supply-chain scan path: $relativePath"
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
    Copy-Item -LiteralPath $sourcePath -Destination $destination
  }
  $gitleaks = Install-LLMGatewayReleaseTool -Name gitleaks
  & $gitleaks dir $scanDirectory --redact --no-banner --report-format json --report-path (Join-Path $evidenceDirectory "gitleaks-report.json")
  if ($LASTEXITCODE -ne 0) { throw "Gitleaks found a secret in the current non-ignored source tree." }
  $report.currentSourceSecrets = $true

  if ($SkipImage) {
    $report.image = "skipped-by-platform"
  } else {
    if (-not $Image) {
      $Image = "llmgateway:supply-chain-$($runID.Substring($runID.Length - 12))"
      & $docker build --build-arg RELEASE_VERSION=0.1.0-supply-chain --build-arg RELEASE_REVISION=working-tree --build-arg RELEASE_BUILT_AT=2026-07-22T00:00:00Z --tag $Image .
      if ($LASTEXITCODE -ne 0) { throw "Could not build the image for supply-chain scanning." }
      $builtImage = $true
    }
    $report.image = $Image
    $trivy = Install-LLMGatewayReleaseTool -Name trivy
    & $trivy image --quiet --exit-code 1 --severity HIGH,CRITICAL --scanners vuln $Image
    if ($LASTEXITCODE -ne 0) { throw "Trivy found a high or critical image vulnerability." }
    $report.imageVulnerabilities = $true
    & $trivy config --quiet --exit-code 1 --severity HIGH,CRITICAL (Join-Path $root "Dockerfile")
    if ($LASTEXITCODE -ne 0) { throw "Trivy found a high or critical Dockerfile misconfiguration." }
    & $trivy config --quiet --exit-code 1 --severity HIGH,CRITICAL (Join-Path $root "deploy")
    if ($LASTEXITCODE -ne 0) { throw "Trivy found a high or critical deployment misconfiguration." }
    $report.imageConfiguration = $true
  }

  [IO.File]::WriteAllText($reportPath, ($report | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
} catch {
  $failure = $_
} finally {
  if ($builtImage -and $Image -match '^llmgateway:supply-chain-[a-z0-9]+$') {
    $previousErrorPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
      $inspection = @(& $docker image inspect $Image 2>$null | ConvertFrom-Json)
      $inspectionExitCode = $LASTEXITCODE
    } finally {
      $ErrorActionPreference = $previousErrorPreference
    }
    if ($inspectionExitCode -eq 0 -and $inspection.Count -eq 1 -and $inspection[0].Config.Labels.'org.opencontainers.image.title' -eq "LLMGateway") {
      & $docker image rm $Image | Out-Null
    }
  }
  $buildRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
  if ($workDirectory.StartsWith($buildRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -and
      (Split-Path -Leaf $workDirectory).StartsWith("supply-", [StringComparison]::Ordinal) -and
      (Test-Path -LiteralPath $workDirectory)) {
    Remove-Item -LiteralPath $workDirectory -Recurse -Force
  }
  Pop-Location
}

if ($null -ne $failure) { throw $failure }
Write-Host "Go, Node, current source, image vulnerability, license, secret, and deployment configuration scans passed."
