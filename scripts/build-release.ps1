[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string] $Version,
  [string] $Revision = "",
  [string] $BuiltAt = "",
  [string] $OutputDirectory = "",
  [ValidateSet("Test", "Keyless")]
  [string] $SigningMode = "Test"
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"
. "$PSScriptRoot\release-tools.ps1"

if ($Version -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') { throw "Version must be a semantic release version." }
$root = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
if (-not $Revision) {
  $Revision = [string](& git -C $root rev-parse HEAD)
  if ($LASTEXITCODE -ne 0 -or $Revision -notmatch '^[0-9a-f]{40}$') { throw "Could not resolve the Git revision." }
}
if ($Revision -notmatch '^[0-9a-f]{7,40}$|^working-tree$') { throw "Revision must be a Git object ID or working-tree." }
if (-not $BuiltAt) { $BuiltAt = [DateTimeOffset]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ") }
try { $null = [DateTimeOffset]::Parse($BuiltAt) } catch { throw "BuiltAt must be an RFC3339 timestamp." }
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $root ".build\release-$Version" }
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
$buildRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
if (-not $OutputDirectory.StartsWith($buildRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
  throw "Release output must be inside the repository .build directory."
}
if (Test-Path -LiteralPath $OutputDirectory) { throw "Release output already exists: $OutputDirectory" }

$workDirectory = Join-Path $buildRoot "release-work-$([guid]::NewGuid().ToString('N'))"
$docker = Get-LLMGatewayDockerCommand
$pnpmCommand = if ($env:OS -eq "Windows_NT") { "pnpm.cmd" } else { "pnpm" }
$image = "llmgateway:release-$($Version.ToLowerInvariant())"
$imageBuilt = $false
$goEnvironment = @{}
foreach ($name in @("GOOS", "GOARCH", "CGO_ENABLED")) {
  $item = Get-Item "Env:$name" -ErrorAction SilentlyContinue
  $goEnvironment[$name] = if ($null -eq $item) { @{ Exists = $false; Value = "" } } else { @{ Exists = $true; Value = $item.Value } }
}

function Invoke-ReleaseBuild {
  param(
    [string] $OS,
    [string] $Arch,
    [string] $Directory
  )
  New-Item -ItemType Directory -Force -Path $Directory | Out-Null
  $suffix = if ($OS -eq "windows") { ".exe" } else { "" }
  $ldflags = "-s -w -X github.com/luckymaomi/llmgateway/internal/buildinfo.version=$Version -X github.com/luckymaomi/llmgateway/internal/buildinfo.revision=$Revision -X github.com/luckymaomi/llmgateway/internal/buildinfo.builtAt=$BuiltAt"
  $env:GOOS = $OS
  $env:GOARCH = $Arch
  $env:CGO_ENABLED = "0"
  foreach ($binary in @(
    @{ Name = "llmgateway"; Package = ".\cmd\gateway"; Tags = "webembed" },
    @{ Name = "llmgateway-dbtool"; Package = ".\cmd\dbtool"; Tags = "" },
    @{ Name = "llmgateway-healthcheck"; Package = ".\cmd\healthcheck"; Tags = "" }
  )) {
    $arguments = @("build", "-buildvcs=false", "-trimpath", "-ldflags", $ldflags)
    if ($binary.Tags) { $arguments += @("-tags", $binary.Tags) }
    $arguments += @("-o", (Join-Path $Directory ($binary.Name + $suffix)), $binary.Package)
    & go @arguments
    if ($LASTEXITCODE -ne 0) { throw "Could not build $($binary.Name) for $OS/$Arch." }
  }
}

$failure = $null
Push-Location $root
try {
  New-Item -ItemType Directory -Force -Path $OutputDirectory, $workDirectory | Out-Null
  & $pnpmCommand --dir web install --frozen-lockfile
  if ($LASTEXITCODE -ne 0) { throw "Frontend dependency integrity failed." }
  & $pnpmCommand --dir web run build
  if ($LASTEXITCODE -ne 0) { throw "Production frontend build failed." }

  $platforms = @(
    @{ OS = "windows"; Arch = "amd64" },
    @{ OS = "linux"; Arch = "amd64" }
  )
  $reproducible = $true
  foreach ($platform in $platforms) {
    $platformName = "$($platform.OS)-$($platform.Arch)"
    $first = Join-Path $workDirectory "$platformName-first"
    $second = Join-Path $workDirectory "$platformName-second"
    Invoke-ReleaseBuild -OS $platform.OS -Arch $platform.Arch -Directory $first
    Invoke-ReleaseBuild -OS $platform.OS -Arch $platform.Arch -Directory $second
    $firstFiles = Get-ChildItem -LiteralPath $first -File | Sort-Object Name
    foreach ($file in $firstFiles) {
      $repeat = Join-Path $second $file.Name
      if (-not (Test-Path -LiteralPath $repeat) -or (Get-FileHash -Algorithm SHA256 $file.FullName).Hash -ne (Get-FileHash -Algorithm SHA256 $repeat).Hash) {
        $reproducible = $false
        throw "Repeated build changed $platformName/$($file.Name)."
      }
    }
    $packageDirectory = Join-Path $workDirectory "llmgateway-$Version-$platformName"
    New-Item -ItemType Directory -Force -Path $packageDirectory | Out-Null
    Copy-Item -LiteralPath (Join-Path $root "LICENSE") -Destination $packageDirectory
    Copy-Item -LiteralPath (Join-Path $root "README.md") -Destination $packageDirectory
    Copy-Item -LiteralPath (Join-Path $root "RELEASE.md") -Destination $packageDirectory
    Copy-Item -LiteralPath (Join-Path $root "deploy") -Destination $packageDirectory -Recurse
    Copy-Item -Path (Join-Path $first "*") -Destination $packageDirectory
    $archive = Join-Path $OutputDirectory "llmgateway-$Version-$platformName.zip"
    Compress-Archive -Path (Join-Path $packageDirectory "*") -DestinationPath $archive -CompressionLevel Optimal
  }

  foreach ($name in @("GOOS", "GOARCH", "CGO_ENABLED")) {
    if ($goEnvironment[$name].Exists) {
      [Environment]::SetEnvironmentVariable($name, $goEnvironment[$name].Value, "Process")
    } else {
      [Environment]::SetEnvironmentVariable($name, $null, "Process")
    }
  }

  $windowsGateway = Join-Path $workDirectory "windows-amd64-first\llmgateway.exe"
  $versionInfo = & $windowsGateway --version | ConvertFrom-Json
  if ($versionInfo.version -ne $Version -or $versionInfo.revision -ne $Revision -or $versionInfo.builtAt -ne $BuiltAt -or $versionInfo.os -ne "windows" -or $versionInfo.arch -ne "amd64") {
    throw "The Windows release binary did not expose the requested build identity."
  }

  & $docker build --build-arg "RELEASE_VERSION=$Version" --build-arg "RELEASE_REVISION=$Revision" --build-arg "RELEASE_BUILT_AT=$BuiltAt" --tag $image .
  if ($LASTEXITCODE -ne 0) { throw "Could not build the release image." }
  $imageBuilt = $true
  $imageIdentity = & $docker run --rm --entrypoint /llmgateway $image --version | ConvertFrom-Json
  if ($LASTEXITCODE -ne 0 -or $imageIdentity.version -ne $Version -or $imageIdentity.revision -ne $Revision -or $imageIdentity.builtAt -ne $BuiltAt -or $imageIdentity.os -ne "linux" -or $imageIdentity.arch -ne "amd64") {
    throw "The Linux release image did not expose the requested build identity."
  }
  $imageTar = Join-Path $OutputDirectory "llmgateway-$Version-linux-amd64.oci.tar"
  & $docker save --output $imageTar $image
  if ($LASTEXITCODE -ne 0) { throw "Could not export the release image." }
  $imageID = [string](& $docker image inspect --format '{{.Id}}' $image)

  $goLicenseReport = @(& go run github.com/google/go-licenses/v2@v2.0.1 report .\cmd\gateway .\cmd\dbtool .\cmd\healthcheck) -join "`n"
  if ($LASTEXITCODE -ne 0) { throw "Could not generate the Go license report." }
  [IO.File]::WriteAllText((Join-Path $OutputDirectory "go-licenses.csv"), $goLicenseReport + "`n", [Text.UTF8Encoding]::new($false))
  $nodeLicenseReport = & $pnpmCommand --dir web licenses list --prod --json
  if ($LASTEXITCODE -ne 0) { throw "Could not generate the Node production license report." }
  [IO.File]::WriteAllText((Join-Path $OutputDirectory "node-licenses.json"), ($nodeLicenseReport -join "`n"), [Text.UTF8Encoding]::new($false))

  $syft = Install-LLMGatewayReleaseTool -Name syft
  & $syft scan $image -o "spdx-json=$(Join-Path $OutputDirectory "llmgateway-$Version-image.spdx.json")"
  if ($LASTEXITCODE -ne 0) { throw "Could not generate the image SBOM." }
  & $syft scan "dir:$OutputDirectory" -o "cyclonedx-json=$(Join-Path $OutputDirectory "llmgateway-$Version-artifacts.cdx.json")"
  if ($LASTEXITCODE -ne 0) { throw "Could not generate the artifact SBOM." }

  $subjects = @()
  foreach ($file in Get-ChildItem -LiteralPath $OutputDirectory -File | Sort-Object Name) {
    $subjects += @{ name = $file.Name; digest = @{ sha256 = (Get-FileHash -Algorithm SHA256 $file.FullName).Hash.ToLowerInvariant() } }
  }
  $provenance = [ordered]@{
    _type = "https://in-toto.io/Statement/v1"
    subject = $subjects
    predicateType = "https://slsa.dev/provenance/v1"
    predicate = [ordered]@{
      buildDefinition = [ordered]@{
        buildType = "https://github.com/luckymaomi/llmgateway/release-build@v1"
        externalParameters = @{ version = $Version; revision = $Revision; builtAt = $BuiltAt; platforms = @("windows/amd64", "linux/amd64") }
        internalParameters = @{ cgo = "disabled"; trimpath = $true; webembed = $true }
        resolvedDependencies = @(@{ uri = "git+https://github.com/luckymaomi/llmgateway@$Revision"; digest = @{ gitCommit = $Revision } })
      }
      runDetails = [ordered]@{
        builder = @{ id = if ($env:GITHUB_WORKFLOW_REF) { "https://github.com/luckymaomi/llmgateway/actions" } else { "local-release-acceptance" } }
        metadata = @{ invocationId = [guid]::NewGuid().ToString(); reproducible = $reproducible }
      }
    }
  }
  $provenancePath = Join-Path $OutputDirectory "provenance.intoto.json"
  [IO.File]::WriteAllText($provenancePath, ($provenance | ConvertTo-Json -Depth 20), [Text.UTF8Encoding]::new($false))
  $manifest = [ordered]@{ version = $Version; revision = $Revision; builtAt = $BuiltAt; image = $image; imageId = $imageID; reproducibleBinaries = $reproducible; signingMode = $SigningMode }
  [IO.File]::WriteAllText((Join-Path $OutputDirectory "release-manifest.json"), ($manifest | ConvertTo-Json), [Text.UTF8Encoding]::new($false))

  $checksumPath = Join-Path $OutputDirectory "SHA256SUMS"
  $checksumLines = Get-ChildItem -LiteralPath $OutputDirectory -File | Where-Object { $_.Name -notmatch '^SHA256SUMS|\.sigstore\.json$|\.pub$' } | Sort-Object Name | ForEach-Object {
    "$((Get-FileHash -Algorithm SHA256 $_.FullName).Hash.ToLowerInvariant())  $($_.Name)"
  }
  [IO.File]::WriteAllLines($checksumPath, $checksumLines, [Text.UTF8Encoding]::new($false))

  $cosign = Install-LLMGatewayReleaseTool -Name cosign
  $bundlePath = Join-Path $OutputDirectory "SHA256SUMS.sigstore.json"
  if ($SigningMode -eq "Test") {
    $keyPrefix = Join-Path $workDirectory "release-test"
    $previousPassword = $env:COSIGN_PASSWORD
    $env:COSIGN_PASSWORD = "llmgateway-release-acceptance"
    try {
      & $cosign generate-key-pair --output-key-prefix $keyPrefix
      if ($LASTEXITCODE -ne 0) { throw "Could not generate the ephemeral release acceptance key." }
      $localSigningConfig = Join-Path $workDirectory "local-signing-config.json"
      & $cosign signing-config create --out $localSigningConfig
      if ($LASTEXITCODE -ne 0) { throw "Could not create the offline release acceptance signing config." }
      & $cosign sign-blob --yes --signing-config $localSigningConfig --key "$keyPrefix.key" --bundle $bundlePath $checksumPath | Out-Null
      if ($LASTEXITCODE -ne 0) { throw "Could not sign the release checksum manifest." }
      & $cosign verify-blob --insecure-ignore-tlog --key "$keyPrefix.pub" --bundle $bundlePath $checksumPath | Out-Null
      if ($LASTEXITCODE -ne 0) { throw "Could not verify the release checksum manifest." }
      Copy-Item -LiteralPath "$keyPrefix.pub" -Destination (Join-Path $OutputDirectory "release-acceptance.pub")
    } finally {
      $env:COSIGN_PASSWORD = $previousPassword
    }
  } else {
    & $cosign sign-blob --yes --bundle $bundlePath $checksumPath | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not create the keyless release signature bundle." }
    & $cosign verify-blob --bundle $bundlePath --certificate-identity-regexp '^https://github.com/luckymaomi/llmgateway/' --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' $checksumPath | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not verify the keyless release signature bundle." }
  }
} catch {
  $failure = $_
} finally {
  foreach ($name in @("GOOS", "GOARCH", "CGO_ENABLED")) {
    if ($goEnvironment[$name].Exists) {
      [Environment]::SetEnvironmentVariable($name, $goEnvironment[$name].Value, "Process")
    } else {
      [Environment]::SetEnvironmentVariable($name, $null, "Process")
    }
  }
  if ($imageBuilt -and $image -match '^llmgateway:release-[0-9a-z.-]+$') {
    & $docker image rm $image 2>$null | Out-Null
  }
  if (Test-Path -LiteralPath $workDirectory) { Remove-Item -LiteralPath $workDirectory -Recurse -Force }
  Pop-Location
}

if ($null -ne $failure) { throw $failure }
Write-Host "Release artifacts, reproducibility, version identity, SBOMs, checksums, provenance, signature, and verification passed: $OutputDirectory"
