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

$semanticReleaseVersionPattern = '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?$'

function Get-CanonicalBuiltAt {
  param([Parameter(Mandatory = $true)][string] $Value)

  if ($Value -notmatch '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2})$') {
    throw "BuiltAt must be an RFC3339 timestamp with seconds and an explicit timezone."
  }
  [DateTimeOffset] $parsed = [DateTimeOffset]::MinValue
  if (-not [DateTimeOffset]::TryParseExact(
      $Value,
      "yyyy-MM-dd'T'HH:mm:ssK",
      [Globalization.CultureInfo]::InvariantCulture,
      [Globalization.DateTimeStyles]::None,
      [ref] $parsed
    )) {
    throw "BuiltAt must be a valid RFC3339 timestamp."
  }
  return $parsed.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture)
}

function Get-DirectoryEvidence {
  param([Parameter(Mandatory = $true)][string] $Directory)

  $directoryPath = [IO.Path]::GetFullPath($Directory)
  if (-not (Test-Path -LiteralPath $directoryPath -PathType Container)) {
    throw "Build output directory is missing: $directoryPath"
  }
  $prefix = $directoryPath.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
  $entries = @(Get-ChildItem -LiteralPath $directoryPath -Force -Recurse)
  if (@($entries | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }).Count -gt 0) {
    throw "Build output must not contain symbolic links or reparse points: $directoryPath"
  }
  $files = @($entries | Where-Object { -not $_.PSIsContainer } | ForEach-Object {
    $fullPath = [IO.Path]::GetFullPath($_.FullName)
    if (-not $fullPath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
      throw "Build output escaped its expected directory: $fullPath"
    }
    [pscustomobject]@{
      path = $fullPath.Substring($prefix.Length).Replace('\', '/')
      sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $fullPath).Hash.ToLowerInvariant()
    }
  } | Sort-Object path)
  if ($files.Count -eq 0) { throw "Build output directory is empty: $directoryPath" }

  $canonicalLines = @($files | ForEach-Object { "$($_.sha256)  $($_.path)" })
  $canonicalBytes = [Text.UTF8Encoding]::new($false).GetBytes(($canonicalLines -join "`n") + "`n")
  $hasher = [Security.Cryptography.SHA256]::Create()
  try {
    $treeDigest = ([BitConverter]::ToString($hasher.ComputeHash($canonicalBytes))).Replace('-', '').ToLowerInvariant()
  } finally {
    $hasher.Dispose()
  }
  return [pscustomobject]@{ files = $files; treeDigest = $treeDigest }
}

function Assert-DirectoryEvidenceEqual {
  param(
    [Parameter(Mandatory = $true)] $First,
    [Parameter(Mandatory = $true)] $Second,
    [Parameter(Mandatory = $true)][string] $Label
  )

  $firstFiles = @($First.files)
  $secondFiles = @($Second.files)
  if ($firstFiles.Count -ne $secondFiles.Count -or $First.treeDigest -cne $Second.treeDigest) {
    throw "Repeated build changed $Label."
  }
  for ($index = 0; $index -lt $firstFiles.Count; $index++) {
    if ($firstFiles[$index].path -cne $secondFiles[$index].path -or $firstFiles[$index].sha256 -cne $secondFiles[$index].sha256) {
      throw "Repeated build changed $Label/$($firstFiles[$index].path)."
    }
  }
}

function Invoke-ReleaseBuild {
  param(
    [Parameter(Mandatory = $true)][string] $OS,
    [Parameter(Mandatory = $true)][string] $Arch,
    [Parameter(Mandatory = $true)][string] $Directory
  )

  New-Item -ItemType Directory -Force -Path $Directory | Out-Null
  $suffix = if ($OS -eq "windows") { ".exe" } else { "" }
  $ldflags = "-s -w -X github.com/luckymaomi/llmgateway/internal/buildinfo.version=$Version -X github.com/luckymaomi/llmgateway/internal/buildinfo.revision=$Revision -X github.com/luckymaomi/llmgateway/internal/buildinfo.builtAt=$BuiltAt"
  $env:GOOS = $OS
  $env:GOARCH = $Arch
  $env:CGO_ENABLED = "0"
  foreach ($binary in @(
    @{ Name = "llmgateway"; Package = "./cmd/gateway"; Tags = "webembed" },
    @{ Name = "llmgateway-dbtool"; Package = "./cmd/dbtool"; Tags = "" },
    @{ Name = "llmgateway-healthcheck"; Package = "./cmd/healthcheck"; Tags = "" }
  )) {
    $arguments = @("build", "-buildvcs=false", "-trimpath", "-ldflags", $ldflags)
    if ($binary.Tags) { $arguments += @("-tags", $binary.Tags) }
    $arguments += @("-o", (Join-Path $Directory ($binary.Name + $suffix)), $binary.Package)
    & go @arguments
    if ($LASTEXITCODE -ne 0) { throw "Could not build $($binary.Name) for $OS/$Arch." }
  }
}

function Assert-ExpectedBinarySet {
  param(
    [Parameter(Mandatory = $true)][string] $Directory,
    [Parameter(Mandatory = $true)][string[]] $Names,
    [Parameter(Mandatory = $true)][string] $Label
  )

  $actual = @(Get-ChildItem -LiteralPath $Directory -Force | ForEach-Object {
    if ($_.PSIsContainer -or ($_.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      throw "$Label contains an unexpected directory or link: $($_.Name)"
    }
    $_.Name
  } | Sort-Object)
  $expected = @($Names | Sort-Object)
  if ($actual.Count -ne $expected.Count) { throw "$Label does not contain exactly the expected binaries." }
  for ($index = 0; $index -lt $expected.Count; $index++) {
    if ($actual[$index] -cne $expected[$index]) { throw "$Label contains an unexpected binary: $($actual[$index])" }
  }
}

if ($Version -notmatch $semanticReleaseVersionPattern) {
  throw "Version must be strict SemVer without build metadata; numeric prerelease identifiers cannot have leading zeroes."
}

$root = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$buildRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
$revisionWasProvided = -not [string]::IsNullOrWhiteSpace($Revision)
$headRevision = [string](& git -C $root rev-parse HEAD)
if ($LASTEXITCODE -ne 0 -or $headRevision.Trim() -notmatch '^[0-9a-f]{40}$') { throw "Could not resolve the Git revision." }
$headRevision = $headRevision.Trim()
$gitStatus = @(& git -C $root status --porcelain --untracked-files=all)
if ($LASTEXITCODE -ne 0) { throw "Could not determine whether the source tree is clean." }
$sourceTreeClean = $gitStatus.Count -eq 0

if (-not $revisionWasProvided) {
  $Revision = if ($SigningMode -eq "Test" -and -not $sourceTreeClean) { "working-tree" } else { $headRevision }
}
if ($Revision -notmatch '^[0-9a-f]{40}$|^working-tree$') {
  throw "Revision must be a full lowercase Git commit ID or working-tree."
}
if ($Revision -eq "working-tree") {
  if ($SigningMode -ne "Test") { throw "Keyless releases cannot use a working-tree revision." }
  if ($sourceTreeClean) { throw "A clean Test release must use its full Git commit ID instead of working-tree." }
} else {
  if ($Revision -cne $headRevision) { throw "Revision must equal the checked-out Git commit." }
  if (-not $sourceTreeClean) { throw "A dirty source tree must use Revision working-tree for Test signing." }
}

if (-not $BuiltAt) { $BuiltAt = [DateTimeOffset]::UtcNow.ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture) }
$BuiltAt = Get-CanonicalBuiltAt -Value $BuiltAt

$expectedTag = "v$Version"
$workflowIdentity = "https://github.com/luckymaomi/llmgateway/.github/workflows/verify.yml@refs/tags/$expectedTag"
if ($SigningMode -eq "Keyless") {
  $expectedWorkflowRef = "luckymaomi/llmgateway/.github/workflows/verify.yml@refs/tags/$expectedTag"
  if ($env:GITHUB_ACTIONS -cne "true" -or
      $env:GITHUB_REPOSITORY -cne "luckymaomi/llmgateway" -or
      $env:GITHUB_REF_TYPE -cne "tag" -or
      $env:GITHUB_REF_NAME -cne $expectedTag -or
      $env:GITHUB_REF -cne "refs/tags/$expectedTag" -or
      $env:GITHUB_SHA -cne $Revision -or
      $env:GITHUB_WORKFLOW_REF -cne $expectedWorkflowRef) {
    throw "Keyless signing requires the exact repository, tag, commit, and Verify workflow identity."
  }
}

if (-not $OutputDirectory) { $OutputDirectory = Join-Path $buildRoot "release-$Version" }
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
if (-not $OutputDirectory.StartsWith($buildRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
  throw "Release output must be inside the repository .build directory."
}
if (Test-Path -LiteralPath $OutputDirectory) { throw "Release output already exists: $OutputDirectory" }

$workDirectory = Join-Path $buildRoot "release-work-$([guid]::NewGuid().ToString('N'))"
$docker = Get-LLMGatewayDockerCommand
$pnpmCommand = if ($env:OS -eq "Windows_NT") { "pnpm.cmd" } else { "pnpm" }
$image = "llmgateway:release-$($Version.ToLowerInvariant())"
$imageBuilt = $false
$containerName = "llmgateway-release-verify-$([guid]::NewGuid().ToString('N').Substring(0, 12))"
$containerCreated = $false
$goEnvironment = @{}
foreach ($name in @("GOOS", "GOARCH", "CGO_ENABLED")) {
  $item = Get-Item "Env:$name" -ErrorAction SilentlyContinue
  $goEnvironment[$name] = if ($null -eq $item) { @{ Exists = $false; Value = "" } } else { @{ Exists = $true; Value = $item.Value } }
}

$failure = $null
Push-Location $root
try {
  New-Item -ItemType Directory -Force -Path $OutputDirectory, $workDirectory | Out-Null

  & $pnpmCommand --dir web install --frozen-lockfile
  if ($LASTEXITCODE -ne 0) { throw "Frontend dependency integrity failed." }

  $webDist = Join-Path (Join-Path $root "web") "dist"
  if (Test-Path -LiteralPath $webDist) { Remove-Item -LiteralPath $webDist -Recurse -Force }
  & $pnpmCommand --dir web run build
  if ($LASTEXITCODE -ne 0) { throw "First production frontend build failed." }
  $firstFrontend = Get-DirectoryEvidence -Directory $webDist

  Remove-Item -LiteralPath $webDist -Recurse -Force
  & $pnpmCommand --dir web run build
  if ($LASTEXITCODE -ne 0) { throw "Second production frontend build failed." }
  $secondFrontend = Get-DirectoryEvidence -Directory $webDist
  Assert-DirectoryEvidenceEqual -First $firstFrontend -Second $secondFrontend -Label "web/dist"

  $deployGitArguments = @("-C", $root, "ls-files", "--cached")
  if ($Revision -eq "working-tree") { $deployGitArguments += @("--others", "--exclude-standard") }
  $deployGitArguments += @("--", "deploy")
  $deployCandidates = @(& git @deployGitArguments)
  if ($LASTEXITCODE -ne 0 -or $deployCandidates.Count -eq 0) { throw "Could not enumerate deployment files." }
  $deployPaths = @($deployCandidates | ForEach-Object {
    $relativePath = ([string] $_).Replace('\', '/')
    if ($relativePath -notmatch '^deploy/[A-Za-z0-9._/-]+$' -or $relativePath -match '(^|/)\.\.?(/|$)') {
      throw "Deployment files contain an unsafe release path."
    }
    $sourcePath = [IO.Path]::GetFullPath((Join-Path $root $relativePath))
    if (-not (Test-Path -LiteralPath $sourcePath)) {
      if ($Revision -eq "working-tree") { return }
      throw "A tracked deployment file is missing: $relativePath"
    }
    $sourceItem = Get-Item -LiteralPath $sourcePath
    if ($sourceItem.PSIsContainer -or ($sourceItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      throw "Deployment files must be regular files: $relativePath"
    }
    $relativePath
  } | Sort-Object -Unique)
  if ($deployPaths.Count -eq 0) { throw "Release contains no deployment files." }

  $releaseDocuments = @("LICENSE", "README.md", "RELEASE.md", "spec.md", "dev.md", "CONTRIBUTING.md", "SECURITY.md")

  Remove-Item Env:GOOS -ErrorAction SilentlyContinue
  Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
  $env:CGO_ENABLED = "0"
  $archiveToolSuffix = if ($env:OS -eq "Windows_NT") { ".exe" } else { "" }
  $archiveTool = Join-Path $workDirectory "releasearchive$archiveToolSuffix"
  & go build -trimpath -o $archiveTool ./cmd/releasearchive
  if ($LASTEXITCODE -ne 0) { throw "Could not build the release archive tool for the host." }

  $platforms = @(
    @{ OS = "windows"; Arch = "amd64" },
    @{ OS = "linux"; Arch = "amd64" }
  )
  $binaryEvidence = @()
  $archiveEvidence = @()
  foreach ($platform in $platforms) {
    $platformName = "$($platform.OS)-$($platform.Arch)"
    $first = Join-Path $workDirectory "$platformName-first"
    $second = Join-Path $workDirectory "$platformName-second"
    $suffix = if ($platform.OS -eq "windows") { ".exe" } else { "" }
    $binaryNames = @("llmgateway$suffix", "llmgateway-dbtool$suffix", "llmgateway-healthcheck$suffix")
    Invoke-ReleaseBuild -OS $platform.OS -Arch $platform.Arch -Directory $first
    Invoke-ReleaseBuild -OS $platform.OS -Arch $platform.Arch -Directory $second
    Assert-ExpectedBinarySet -Directory $first -Names $binaryNames -Label "$platformName first build"
    Assert-ExpectedBinarySet -Directory $second -Names $binaryNames -Label "$platformName repeated build"
    foreach ($binaryName in $binaryNames) {
      $firstPath = Join-Path $first $binaryName
      $secondPath = Join-Path $second $binaryName
      $firstHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $firstPath).Hash.ToLowerInvariant()
      $secondHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $secondPath).Hash.ToLowerInvariant()
      if ($firstHash -cne $secondHash) { throw "Repeated build changed $platformName/$binaryName." }
      $binaryEvidence += [ordered]@{ name = "$platformName/$binaryName"; sha256 = $firstHash }
    }

    $packageDirectory = Join-Path $workDirectory "llmgateway-$Version-$platformName"
    New-Item -ItemType Directory -Force -Path $packageDirectory | Out-Null
    foreach ($document in $releaseDocuments) {
      Copy-Item -LiteralPath (Join-Path $root $document) -Destination $packageDirectory
    }
    foreach ($relativePath in $deployPaths) {
      $sourcePath = [IO.Path]::GetFullPath((Join-Path $root $relativePath))
      $deployRoot = [IO.Path]::GetFullPath((Join-Path $root "deploy")) + [IO.Path]::DirectorySeparatorChar
      if (-not $sourcePath.StartsWith($deployRoot, [StringComparison]::OrdinalIgnoreCase) -or
          -not (Test-Path -LiteralPath $sourcePath -PathType Leaf) -or
          ((Get-Item -LiteralPath $sourcePath).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw "Refusing to package an invalid deployment file: $relativePath"
      }
      $destinationPath = [IO.Path]::GetFullPath((Join-Path $packageDirectory $relativePath))
      $packagePrefix = $packageDirectory + [IO.Path]::DirectorySeparatorChar
      if (-not $destinationPath.StartsWith($packagePrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Deployment package path escaped its archive root: $relativePath"
      }
      New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destinationPath) | Out-Null
      Copy-Item -LiteralPath $sourcePath -Destination $destinationPath
    }
    foreach ($binaryName in $binaryNames) {
      Copy-Item -LiteralPath (Join-Path $first $binaryName) -Destination $packageDirectory
    }

    $archiveEntries = @($releaseDocuments + $deployPaths + $binaryNames | Sort-Object)
    $archiveName = "llmgateway-$Version-$platformName.zip"
    $archive = Join-Path $OutputDirectory $archiveName
    $archiveArguments = @(
      "-source", $packageDirectory,
      "-output", $archive,
      "-modified", $BuiltAt
    )
    foreach ($entry in $archiveEntries) { $archiveArguments += @("-entry", $entry) }
    & $archiveTool @archiveArguments
    if ($LASTEXITCODE -ne 0) { throw "Could not create the canonical $platformName release archive." }
    $archiveEvidence += [ordered]@{ name = $archiveName; entries = $archiveEntries }
  }

  foreach ($name in @("GOOS", "GOARCH", "CGO_ENABLED")) {
    if ($goEnvironment[$name].Exists) {
      [Environment]::SetEnvironmentVariable($name, $goEnvironment[$name].Value, "Process")
    } else {
      [Environment]::SetEnvironmentVariable($name, $null, "Process")
    }
  }

  $hostOS = if ($env:OS -eq "Windows_NT") { "windows" } else { "linux" }
  $hostSuffix = if ($hostOS -eq "windows") { ".exe" } else { "" }
  $hostBuildDirectory = Join-Path $workDirectory "$hostOS-amd64-first"
  foreach ($binaryName in @("llmgateway", "llmgateway-dbtool", "llmgateway-healthcheck")) {
    $binaryPath = Join-Path $hostBuildDirectory ($binaryName + $hostSuffix)
    $versionInfo = & $binaryPath --version | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or
        $versionInfo.version -cne $Version -or
        $versionInfo.revision -cne $Revision -or
        $versionInfo.builtAt -cne $BuiltAt -or
        $versionInfo.os -cne $hostOS -or
        $versionInfo.arch -cne "amd64") {
      throw "$binaryName did not expose the requested $hostOS/amd64 build identity."
    }
  }

  & $docker build --build-arg "RELEASE_VERSION=$Version" --build-arg "RELEASE_REVISION=$Revision" --build-arg "RELEASE_BUILT_AT=$BuiltAt" --tag $image .
  if ($LASTEXITCODE -ne 0) { throw "Could not build the release image." }
  $imageBuilt = $true
  $imageIdentity = & $docker run --rm --entrypoint /llmgateway $image --version | ConvertFrom-Json
  if ($LASTEXITCODE -ne 0 -or
      $imageIdentity.version -cne $Version -or
      $imageIdentity.revision -cne $Revision -or
      $imageIdentity.builtAt -cne $BuiltAt -or
      $imageIdentity.os -cne "linux" -or
      $imageIdentity.arch -cne "amd64") {
    throw "The Linux release image did not expose the requested build identity."
  }

  $containerID = [string](& $docker create --name $containerName $image)
  if ($LASTEXITCODE -ne 0 -or -not $containerID.Trim()) { throw "Could not create a container for binary comparison." }
  $containerCreated = $true
  $containerDirectory = Join-Path $workDirectory "container-binaries"
  New-Item -ItemType Directory -Force -Path $containerDirectory | Out-Null
  $linuxBuildDirectory = Join-Path $workDirectory "linux-amd64-first"
  foreach ($binaryName in @("llmgateway", "llmgateway-dbtool", "llmgateway-healthcheck")) {
    $containerPath = Join-Path $containerDirectory $binaryName
    & $docker cp "${containerName}:/$binaryName" $containerPath
    if ($LASTEXITCODE -ne 0) { throw "Could not extract $binaryName from the release image." }
    $containerHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $containerPath).Hash.ToLowerInvariant()
    $releaseHash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $linuxBuildDirectory $binaryName)).Hash.ToLowerInvariant()
    if ($containerHash -cne $releaseHash) { throw "Release image binary differs from linux-amd64/$binaryName." }
  }
  & $docker rm $containerName | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Could not remove the binary comparison container." }
  $containerCreated = $false

  $imageTarName = "llmgateway-$Version-linux-amd64.oci.tar"
  $imageTar = Join-Path $OutputDirectory $imageTarName
  & $docker save --output $imageTar $image
  if ($LASTEXITCODE -ne 0) { throw "Could not export the release image." }
  $imageID = ([string](& $docker image inspect --format '{{.Id}}' $image)).Trim()
  if ($LASTEXITCODE -ne 0 -or $imageID -notmatch '^sha256:[0-9a-f]{64}$') { throw "Could not resolve the release image ID." }

  $goLicenseReport = @(& go run github.com/google/go-licenses/v2@v2.0.1 report ./cmd/gateway ./cmd/dbtool ./cmd/healthcheck) -join "`n"
  if ($LASTEXITCODE -ne 0 -or -not $goLicenseReport.Trim()) { throw "Could not generate the Go license report." }
  [IO.File]::WriteAllText((Join-Path $OutputDirectory "go-licenses.csv"), $goLicenseReport + "`n", [Text.UTF8Encoding]::new($false))
  $nodeLicenseReport = & $pnpmCommand --dir web licenses list --prod --json
  if ($LASTEXITCODE -ne 0 -or -not (@($nodeLicenseReport) -join "`n").Trim()) { throw "Could not generate the Node production license report." }
  [IO.File]::WriteAllText((Join-Path $OutputDirectory "node-licenses.json"), (@($nodeLicenseReport) -join "`n"), [Text.UTF8Encoding]::new($false))

  $spdxName = "llmgateway-$Version-image.spdx.json"
  $cycloneDXName = "llmgateway-$Version-artifacts.cdx.json"
  $syft = Install-LLMGatewayReleaseTool -Name syft
  & $syft scan $image -o "spdx-json=$(Join-Path $OutputDirectory $spdxName)"
  if ($LASTEXITCODE -ne 0) { throw "Could not generate the image SBOM." }
  & $syft scan "dir:$OutputDirectory" -o "cyclonedx-json=$(Join-Path $OutputDirectory $cycloneDXName)"
  if ($LASTEXITCODE -ne 0) { throw "Could not generate the artifact SBOM." }

  $binaryEvidence = @($binaryEvidence | Sort-Object name)
  $provenanceSubjects = @($binaryEvidence | ForEach-Object {
    [ordered]@{ name = $_.name; digest = @{ sha256 = $_.sha256 } }
  })
  $provenanceSubjects += [ordered]@{ name = "web/dist"; digest = @{ sha256 = $secondFrontend.treeDigest } }
  $resolvedDependencies = @()
  if ($Revision -ne "working-tree") {
    $resolvedDependencies = @([ordered]@{
      uri = "git+https://github.com/luckymaomi/llmgateway@$Revision"
      digest = @{ gitCommit = $Revision }
    })
  }
  $builderID = if ($SigningMode -eq "Keyless") { $workflowIdentity } else { "https://github.com/luckymaomi/llmgateway/local-release-acceptance" }
  $provenance = [ordered]@{
    _type = "https://in-toto.io/Statement/v1"
    subject = $provenanceSubjects
    predicateType = "https://slsa.dev/provenance/v1"
    predicate = [ordered]@{
      buildDefinition = [ordered]@{
        buildType = "https://github.com/luckymaomi/llmgateway/release-build"
        externalParameters = [ordered]@{
          version = $Version
          revision = $Revision
          builtAt = $BuiltAt
          signingMode = $SigningMode
          platforms = @("windows/amd64", "linux/amd64")
        }
        internalParameters = [ordered]@{ cgo = "disabled"; trimpath = $true; webembed = $true; sourceTreeClean = $sourceTreeClean }
        resolvedDependencies = $resolvedDependencies
      }
      runDetails = [ordered]@{
        builder = @{ id = $builderID }
        metadata = [ordered]@{
          invocationId = [guid]::NewGuid().ToString()
          reproducible = $false
          reproducibility = [ordered]@{ allArtifacts = $false; goBinaries = $true; frontendTree = $true }
          containerBinariesMatchLinuxRelease = $true
        }
      }
    }
  }
  $provenanceName = "provenance.intoto.json"
  [IO.File]::WriteAllText((Join-Path $OutputDirectory $provenanceName), ($provenance | ConvertTo-Json -Depth 20), [Text.UTF8Encoding]::new($false))

  $manifest = [ordered]@{
    format = "llmgateway-release-manifest"
    version = $Version
    revision = $Revision
    builtAt = $BuiltAt
    signingMode = $SigningMode
    image = $image
    imageId = $imageID
    reproducibility = [ordered]@{ allArtifacts = $false; goBinaries = $true; frontendTree = $true }
    containerBinariesMatchLinuxRelease = $true
    verifiedBinaries = $binaryEvidence
    frontendTree = [ordered]@{ name = "web/dist"; sha256 = $secondFrontend.treeDigest; fileCount = @($secondFrontend.files).Count }
    archives = $archiveEvidence
  }
  $manifestName = "release-manifest.json"
  [IO.File]::WriteAllText((Join-Path $OutputDirectory $manifestName), ($manifest | ConvertTo-Json -Depth 20), [Text.UTF8Encoding]::new($false))

  $payloadNames = @(
    "llmgateway-$Version-windows-amd64.zip",
    "llmgateway-$Version-linux-amd64.zip",
    $imageTarName,
    "go-licenses.csv",
    "node-licenses.json",
    $spdxName,
    $cycloneDXName,
    $provenanceName,
    $manifestName
  )
  $actualPayloadNames = @(Get-ChildItem -LiteralPath $OutputDirectory -Force | ForEach-Object {
    if ($_.PSIsContainer -or ($_.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "Release output contains an unexpected directory or link." }
    $_.Name
  } | Sort-Object)
  $expectedPayloadNames = @($payloadNames | Sort-Object)
  if ($actualPayloadNames.Count -ne $expectedPayloadNames.Count) { throw "Release payload file set is incomplete or contains extras." }
  for ($index = 0; $index -lt $expectedPayloadNames.Count; $index++) {
    if ($actualPayloadNames[$index] -cne $expectedPayloadNames[$index]) { throw "Unexpected release payload: $($actualPayloadNames[$index])" }
  }

  $checksumPath = Join-Path $OutputDirectory "SHA256SUMS"
  $checksumLines = @($payloadNames | Sort-Object | ForEach-Object {
    "$((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $OutputDirectory $_)).Hash.ToLowerInvariant())  $_"
  })
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
    & $cosign verify-blob --bundle $bundlePath --certificate-identity $workflowIdentity --certificate-oidc-issuer "https://token.actions.githubusercontent.com" $checksumPath | Out-Null
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
  if ($containerCreated -and $containerName -match '^llmgateway-release-verify-[0-9a-f]{12}$') {
    & $docker rm --force $containerName 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0 -and $null -eq $failure) { $failure = [Exception]::new("Could not clean up the release comparison container.") }
  }
  if ($imageBuilt -and $image -match '^llmgateway:release-[0-9a-z.-]+$') {
    & $docker image rm $image 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0 -and $null -eq $failure) { $failure = [Exception]::new("Could not clean up the release image.") }
  }
  if (Test-Path -LiteralPath $workDirectory) { Remove-Item -LiteralPath $workDirectory -Recurse -Force }
  if ($null -ne $failure -and
      $OutputDirectory.StartsWith($buildRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -and
      (Test-Path -LiteralPath $OutputDirectory)) {
    Remove-Item -LiteralPath $OutputDirectory -Recurse -Force
  }
  Pop-Location
}

if ($null -ne $failure) { throw $failure }
Write-Host "Release identity, repeated Go/frontend builds, container parity, SBOMs, checksums, provenance, and signature passed: $OutputDirectory"
