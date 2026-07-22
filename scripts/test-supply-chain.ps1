[CmdletBinding()]
param(
  [string] $Image = "",
  [string] $ReleaseDirectory = "",
  [switch] $SkipImage
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"
. "$PSScriptRoot\release-tools.ps1"

$root = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$buildRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
$pathComparison = if ($env:OS -eq "Windows_NT") { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
if ($ReleaseDirectory) {
  try {
    $releaseDirectoryCandidate = if ([IO.Path]::IsPathRooted($ReleaseDirectory)) { $ReleaseDirectory } else { Join-Path $root $ReleaseDirectory }
    $ReleaseDirectory = [IO.Path]::GetFullPath($releaseDirectoryCandidate)
  } catch {
    throw "ReleaseDirectory must be a valid path inside the repository .build directory."
  }
  $buildPrefix = $buildRoot.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
  if (-not $ReleaseDirectory.StartsWith($buildPrefix, $pathComparison) -or
      -not (Test-Path -LiteralPath $ReleaseDirectory -PathType Container)) {
    throw "ReleaseDirectory must be an existing directory inside the repository .build directory."
  }
  $currentDirectory = $ReleaseDirectory
  while ($currentDirectory.Equals($buildRoot, $pathComparison) -or $currentDirectory.StartsWith($buildPrefix, $pathComparison)) {
    $directoryItem = Get-Item -LiteralPath $currentDirectory -Force
    if (($directoryItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      throw "ReleaseDirectory must not traverse a symbolic link or junction."
    }
    $currentDirectory = Split-Path -Parent $currentDirectory
  }
}
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
  gitHistorySecrets = $false
  historicalFixtureFindings = @()
  historicalFixtureReason = "No historical fixture exception was used."
  currentSourceSecrets = $false
  releaseArtifactsSecrets = if ($ReleaseDirectory) { $false } else { $null }
  imageVulnerabilities = $false
  imageSecrets = $false
  imageConfiguration = $false
  image = ""
}

# These are the two historical mock response body constants already audited by the owner.
# Every field is checked; this is intentionally narrower than a path or rule allowlist.
$historicalFixtureExceptions = @(
  [pscustomobject]@{
    Fingerprint = "d4b3e2c721d5f8a6acd09cbb603363afbb615446:web/e2e/mock-server.ts:generic-api-key:244"
    RuleID = "generic-api-key"
    Commit = "d4b3e2c721d5f8a6acd09cbb603363afbb615446"
    File = "web/e2e/mock-server.ts"
    StartLine = 244
  }
  [pscustomobject]@{
    Fingerprint = "b42652b74fba4da7f7b2901eaa44917a073ce877:web/e2e/mock-server.ts:generic-api-key:135"
    RuleID = "generic-api-key"
    Commit = "b42652b74fba4da7f7b2901eaa44917a073ce877"
    File = "web/e2e/mock-server.ts"
    StartLine = 135
  }
)

function Invoke-GitleaksScan {
  param(
    [Parameter(Mandatory = $true)][ValidateSet("git", "dir")][string] $Mode,
    [Parameter(Mandatory = $true)][string] $Target,
    [Parameter(Mandatory = $true)][string] $ReportPath,
    [Parameter(Mandatory = $true)][string] $FindingMessage,
    [object[]] $AllowedFindings = @(),
    [int] $MaxArchiveDepth = 0
  )

  if ($AllowedFindings.Count -gt 0 -and $Mode -ne "git") {
    throw "Exact finding exceptions are only supported for Git history scans."
  }
  $configuredFingerprints = @{}
  foreach ($allowedFinding in $AllowedFindings) {
    $allowedFingerprint = [string]$allowedFinding.Fingerprint
    $expectedFingerprint = "$([string]$allowedFinding.Commit):$([string]$allowedFinding.File):$([string]$allowedFinding.RuleID):$([int]$allowedFinding.StartLine)"
    if (-not $allowedFingerprint -or
        $allowedFingerprint -cne $expectedFingerprint -or
        $configuredFingerprints.ContainsKey($allowedFingerprint)) {
      throw "The exact historical fixture exception configuration is invalid."
    }
    $configuredFingerprints[$allowedFingerprint] = $true
  }

  $arguments = @(
    $Mode,
    $Target,
    "--redact=100",
    "--no-banner",
    "--no-color",
    "--log-level", "error",
    "--exit-code", "3",
    "--report-format", "json",
    "--report-path", $ReportPath
  )
  if ($MaxArchiveDepth -gt 0) { $arguments += @("--max-archive-depth", $MaxArchiveDepth) }
  if ($Mode -eq "git") { $arguments += "--log-opts=--all" }

  # Start from a known-safe report so a failed scanner cannot leave stale findings
  # from a previous run in the evidence directory.
  [IO.File]::WriteAllText($ReportPath, "[]", [Text.UTF8Encoding]::new($false))
  & $gitleaks @arguments *> $null
  $scanExitCode = $LASTEXITCODE
  if ($scanExitCode -ne 0 -and $scanExitCode -ne 3) { throw "Gitleaks could not complete the requested secret scan." }

  try {
    $parsedReport = Get-Content -Raw -Encoding UTF8 -LiteralPath $ReportPath | ConvertFrom-Json
    # Windows PowerShell can preserve a JSON array as one pipeline object inside
    # a function; enumerate it explicitly so every finding is checked separately.
    $findings = @($parsedReport | ForEach-Object { $_ })
  } catch {
    throw "Gitleaks did not produce a valid machine-readable report."
  }
  if ($findings.Count -eq 0) {
    if ($scanExitCode -eq 0) { return @() }
    throw "Gitleaks reported findings without a machine-readable finding list."
  }

  $sanitizedFindings = @()
  foreach ($finding in $findings) {
    $startLine = 0
    try { $startLine = [int]$finding.StartLine } catch { $startLine = 0 }
    $sanitizedFindings += [pscustomobject][ordered]@{
      fingerprint = [string]$finding.Fingerprint
      rule = [string]$finding.RuleID
      commit = [string]$finding.Commit
      file = [string]$finding.File
      startLine = $startLine
    }
  }
  [IO.File]::WriteAllText($ReportPath, ($sanitizedFindings | ConvertTo-Json -Depth 3), [Text.UTF8Encoding]::new($false))
  if ($scanExitCode -eq 0) { throw "Gitleaks returned success with one or more findings." }
  if ($AllowedFindings.Count -eq 0) { throw $FindingMessage }

  $seenFingerprints = @{}
  foreach ($finding in $sanitizedFindings) {
    $fingerprint = [string]$finding.fingerprint
    $ruleID = [string]$finding.rule
    $commit = [string]$finding.commit
    $file = [string]$finding.file
    $startLine = [int]$finding.startLine
    if (-not $fingerprint -or $seenFingerprints.ContainsKey($fingerprint)) {
      throw "Gitleaks returned a duplicate or incomplete finding fingerprint."
    }
    $seenFingerprints[$fingerprint] = $true
    $exactException = @($AllowedFindings | Where-Object {
      ([string]$_.Fingerprint -ceq $fingerprint) -and
      ([string]$_.RuleID -ceq $ruleID) -and
      ([string]$_.Commit -ceq $commit) -and
      ([string]$_.File -ceq $file) -and
      ([int]$_.StartLine -eq $startLine)
    })
    if ($exactException.Count -ne 1) {
      $fingerprintException = @($AllowedFindings | Where-Object { [string]$_.Fingerprint -ceq $fingerprint })
      if ($fingerprintException.Count -eq 1) {
        throw "Gitleaks historical fixture metadata drifted for an approved fingerprint."
      }
      throw "Gitleaks found an unapproved secret in the requested scan."
    }
  }
  return $sanitizedFindings
}

Push-Location $root
try {
  New-Item -ItemType Directory -Force -Path $scanDirectory, $evidenceDirectory | Out-Null

  $isShallowRepository = [string](& git rev-parse --is-shallow-repository 2>$null)
  if ($LASTEXITCODE -ne 0 -or $isShallowRepository -ne "false") {
    throw "Gitleaks history scanning requires a complete Git checkout."
  }

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
  $stagedSourceFileCount = 0
  foreach ($relativePath in $sourceFiles) {
    $sourcePath = [IO.Path]::GetFullPath((Join-Path $root $relativePath))
    if (-not $sourcePath.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
      throw "Refusing to stage a supply-chain scan source outside the repository: $relativePath"
    }
    $sourceItem = Get-Item -Force -LiteralPath $sourcePath -ErrorAction SilentlyContinue
    if ($null -eq $sourceItem) {
      continue
    }
    if ($sourceItem.PSIsContainer -or ($sourceItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      throw "Refusing to stage a non-regular supply-chain scan source: $relativePath"
    }
    $destination = [IO.Path]::GetFullPath((Join-Path $scanDirectory $relativePath))
    if (-not $destination.StartsWith($scanDirectory + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
      throw "Refusing to stage an invalid supply-chain scan path: $relativePath"
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
    Copy-Item -LiteralPath $sourcePath -Destination $destination
    $stagedSourceFileCount++
  }
  if ($stagedSourceFileCount -eq 0) { throw "The current non-ignored source tree contains no regular files." }
  $gitleaks = Install-LLMGatewayReleaseTool -Name gitleaks
  $acceptedHistoricalFindings = @(Invoke-GitleaksScan -Mode git -Target $root -ReportPath (Join-Path $evidenceDirectory "gitleaks-history-report.json") -FindingMessage "Gitleaks found a secret in Git history." -AllowedFindings $historicalFixtureExceptions)
  $report.gitHistorySecrets = $true
  $report.historicalFixtureFindings = $acceptedHistoricalFindings
  if ($acceptedHistoricalFindings.Count -gt 0) {
    $report.historicalFixtureReason = "Only the two exact audited mock response body fingerprints are accepted; any additional finding or metadata drift fails."
  }
  Invoke-GitleaksScan -Mode dir -Target $scanDirectory -ReportPath (Join-Path $evidenceDirectory "gitleaks-report.json") -FindingMessage "Gitleaks found a secret in the current non-ignored source tree."
  $report.currentSourceSecrets = $true
  if ($ReleaseDirectory) {
    Invoke-GitleaksScan -Mode dir -Target $ReleaseDirectory -ReportPath (Join-Path $evidenceDirectory "gitleaks-release-report.json") -FindingMessage "Gitleaks found a secret in the release artifacts." -MaxArchiveDepth 2
    $report.releaseArtifactsSecrets = $true
  }

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
    & $trivy image --quiet --exit-code 1 --severity HIGH,CRITICAL --scanners vuln,secret $Image *> $null
    if ($LASTEXITCODE -ne 0) { throw "Trivy found a high or critical image vulnerability or an embedded secret." }
    $report.imageVulnerabilities = $true
    $report.imageSecrets = $true
    & $trivy config --quiet --exit-code 1 --severity HIGH,CRITICAL (Join-Path $root "Dockerfile") *> $null
    if ($LASTEXITCODE -ne 0) { throw "Trivy found a high or critical Dockerfile misconfiguration." }
    & $trivy config --quiet --exit-code 1 --severity HIGH,CRITICAL (Join-Path $root "deploy") *> $null
    if ($LASTEXITCODE -ne 0) { throw "Trivy found a high or critical deployment misconfiguration." }
    $report.imageConfiguration = $true
  }

  [IO.File]::WriteAllText($reportPath, ($report | ConvertTo-Json -Depth 5), [Text.UTF8Encoding]::new($false))
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
  if ($workDirectory.StartsWith($buildRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -and
      (Split-Path -Leaf $workDirectory).StartsWith("supply-", [StringComparison]::Ordinal) -and
      (Test-Path -LiteralPath $workDirectory)) {
    Remove-Item -LiteralPath $workDirectory -Recurse -Force
  }
  Pop-Location
}

if ($null -ne $failure) { throw $failure }
Write-Host "Go, Node, Git history, current source, release artifact (when requested), image vulnerability and secret, license, and deployment configuration scans passed."
