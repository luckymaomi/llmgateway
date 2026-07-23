[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string] $Directory,
  [Parameter(Mandatory = $true)]
  [string] $ExpectedVersion,
  [Parameter(Mandatory = $true)]
  [string] $ExpectedRevision,
  [string] $PublicKey = "",
  [switch] $Keyless
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\release-tools.ps1"

$semanticReleaseVersionPattern = '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?$'

function Get-CanonicalBuiltAt {
  param([Parameter(Mandatory = $true)][string] $Value)

  if ($Value -notmatch '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$') {
    throw "Release builtAt must be a canonical UTC RFC3339 timestamp."
  }
  [DateTimeOffset] $parsed = [DateTimeOffset]::MinValue
  if (-not [DateTimeOffset]::TryParseExact(
      $Value,
      "yyyy-MM-dd'T'HH:mm:ss'Z'",
      [Globalization.CultureInfo]::InvariantCulture,
      [Globalization.DateTimeStyles]::AssumeUniversal,
      [ref] $parsed
    )) {
    throw "Release builtAt is not a valid timestamp."
  }
  return $parsed.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture)
}

function Assert-ExactNames {
  param(
    [Parameter(Mandatory = $true)][string[]] $Actual,
    [Parameter(Mandatory = $true)][string[]] $Expected,
    [Parameter(Mandatory = $true)][string] $Label
  )

  $actualSorted = @($Actual | Sort-Object)
  $expectedSorted = @($Expected | Sort-Object)
  if ($actualSorted.Count -ne $expectedSorted.Count) { throw "$Label contains an unexpected or missing entry." }
  for ($index = 0; $index -lt $expectedSorted.Count; $index++) {
    if ($actualSorted[$index] -cne $expectedSorted[$index]) {
      throw "$Label entry mismatch: expected '$($expectedSorted[$index])', got '$($actualSorted[$index])'."
    }
  }
}

function Get-JsonDocument {
  param([Parameter(Mandatory = $true)][string] $Path)
  try {
    return Get-Content -Raw -Encoding UTF8 -LiteralPath $Path | ConvertFrom-Json
  } catch {
    throw "Invalid JSON release file: $Path"
  }
}

function Get-ArchiveFiles {
  param([Parameter(Mandatory = $true)][string] $Path)

  try { Add-Type -AssemblyName System.IO.Compression.FileSystem -ErrorAction Stop } catch { }
  $zip = $null
  $files = New-Object 'System.Collections.Generic.List[object]'
  $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
  try {
    $zip = [IO.Compression.ZipFile]::OpenRead($Path)
    foreach ($entry in $zip.Entries) {
      $name = [string] $entry.FullName
      if (-not $name -or $name.Contains('\') -or $name.StartsWith('/') -or
          $name -notmatch '^[A-Za-z0-9][A-Za-z0-9._/-]*$' -or
          $name -match '(^|/)\.\.?(/|$)' -or $name.EndsWith('/')) {
        throw "Archive contains an unsafe path: $($entry.FullName)"
      }
      if (-not $seen.Add($name)) { throw "Archive contains a duplicate entry: $name" }
      $externalAttributes = [BitConverter]::ToUInt32([BitConverter]::GetBytes([int] $entry.ExternalAttributes), 0)
      $files.Add([pscustomobject]@{
        Name = $name
        UnixMode = [int](($externalAttributes -shr 16) -band 0xffff)
      })
    }
  } catch {
    if ($_.Exception.Message -like "Archive contains*") { throw }
    throw "Could not read release archive: $Path"
  } finally {
    if ($null -ne $zip) { $zip.Dispose() }
  }
  return @($files | Sort-Object Name)
}

function Expand-ArchiveSafely {
  param(
    [Parameter(Mandatory = $true)][string] $ArchivePath,
    [Parameter(Mandatory = $true)][string] $Destination
  )

  New-Item -ItemType Directory -Force -Path $Destination | Out-Null
  try { Add-Type -AssemblyName System.IO.Compression.FileSystem -ErrorAction Stop } catch { }
  $zip = $null
  try {
    $zip = [IO.Compression.ZipFile]::OpenRead($ArchivePath)
    $destinationPrefix = [IO.Path]::GetFullPath($Destination).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    foreach ($entry in $zip.Entries) {
      $name = [string] $entry.FullName
      if (-not $name -or $name.Contains('\') -or $name.StartsWith('/') -or
          $name -notmatch '^[A-Za-z0-9][A-Za-z0-9._/-]*$' -or
          $name -match '(^|/)\.\.?(/|$)' -or $name.EndsWith('/')) {
        throw "Archive contains an unsafe path: $($entry.FullName)"
      }
      if (-not $seen.Add($name)) { throw "Archive contains a duplicate entry: $name" }
      $relative = $name.Replace('/', [IO.Path]::DirectorySeparatorChar)
      $target = [IO.Path]::GetFullPath((Join-Path $Destination $relative))
      if (-not $target.StartsWith($destinationPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Archive extraction escaped its destination: $name"
      }
      New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null
      $input = $null
      $output = $null
      try {
        $input = $entry.Open()
        $output = [IO.File]::Open($target, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
        $input.CopyTo($output)
      } finally {
        if ($null -ne $output) { $output.Dispose() }
        if ($null -ne $input) { $input.Dispose() }
      }
    }
  } finally {
    if ($null -ne $zip) { $zip.Dispose() }
  }
}

if ($ExpectedVersion -notmatch $semanticReleaseVersionPattern) {
  throw "ExpectedVersion must be strict SemVer without build metadata; numeric prerelease identifiers cannot have leading zeroes."
}
if ($Keyless) {
  if ($ExpectedRevision -notmatch '^[0-9a-f]{40}$') { throw "Keyless ExpectedRevision must be a full lowercase Git commit ID." }
} elseif ($ExpectedRevision -notmatch '^[0-9a-f]{40}$|^working-tree$') {
  throw "ExpectedRevision must be a full lowercase Git commit ID or working-tree."
}

$Directory = [IO.Path]::GetFullPath($Directory)
if (-not (Test-Path -LiteralPath $Directory -PathType Container)) { throw "Release directory does not exist." }
$version = $ExpectedVersion
$windowsZipName = "llmgateway-$version-windows-amd64.zip"
$linuxZipName = "llmgateway-$version-linux-amd64.zip"
$imageTarName = "llmgateway-$version-linux-amd64.oci.tar"
$spdxName = "llmgateway-$version-image.spdx.json"
$cycloneDXName = "llmgateway-$version-artifacts.cdx.json"
$payloadNames = @(
  $windowsZipName,
  $linuxZipName,
  $imageTarName,
  "go-licenses.csv",
  "node-licenses.json",
  $spdxName,
  $cycloneDXName,
  "provenance.intoto.json",
  "release-manifest.json"
)
$expectedNames = @($payloadNames + "SHA256SUMS" + "SHA256SUMS.sigstore.json")
if (-not $Keyless) { $expectedNames += "release-acceptance.pub" }
$actualEntries = @(Get-ChildItem -LiteralPath $Directory -Force | ForEach-Object {
  if ($_.PSIsContainer -or ($_.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw "Release directory contains an unexpected directory or link: $($_.Name)"
  }
  if ($_.Length -le 0) { throw "Release file is empty: $($_.Name)" }
  $_.Name
})
Assert-ExactNames -Actual $actualEntries -Expected $expectedNames -Label "release file set"

$checksumPath = Join-Path $Directory "SHA256SUMS"
$checksums = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
$checksumLines = @(Get-Content -Encoding ASCII -LiteralPath $checksumPath)
if ($checksumLines.Count -eq 0) { throw "SHA256SUMS is empty." }
foreach ($line in $checksumLines) {
  if ($line -notmatch '^([0-9a-f]{64})  ([A-Za-z0-9][A-Za-z0-9._-]*)$') { throw "Invalid SHA256SUMS line." }
  $expectedHash = $Matches[1]
  $name = $Matches[2]
  if ($checksums.ContainsKey($name)) { throw "Duplicate SHA256SUMS entry: $name" }
  if (-not $payloadNames.Contains($name)) { throw "SHA256SUMS contains an unexpected entry: $name" }
  $path = Join-Path $Directory $name
  $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
  if ($actualHash -cne $expectedHash) { throw "Checksum mismatch: $name" }
  $checksums.Add($name, $expectedHash)
}
Assert-ExactNames -Actual @($checksums.Keys) -Expected $payloadNames -Label "SHA256SUMS"

$manifest = Get-JsonDocument -Path (Join-Path $Directory "release-manifest.json")
if ($manifest.format -cne "llmgateway-release-manifest" -or
    $manifest.version -cne $version -or
    $manifest.revision -cne $ExpectedRevision -or
    $manifest.signingMode -cne $(if ($Keyless) { "Keyless" } else { "Test" }) -or
    $manifest.image -cne "llmgateway:release-$($version.ToLowerInvariant())" -or
    $manifest.imageId -notmatch '^sha256:[0-9a-f]{64}$') {
  throw "Release manifest identity does not match the expected version, revision, or signing mode."
}
$builtAt = Get-CanonicalBuiltAt -Value ([string] $manifest.builtAt)
if ($manifest.builtAt -cne $builtAt) { throw "Release manifest builtAt is not canonical UTC."
}
if ($manifest.reproducibility.allArtifacts -ne $false -or
    $manifest.reproducibility.goBinaries -ne $true -or
    $manifest.reproducibility.frontendTree -ne $true -or
    $manifest.containerBinariesMatchLinuxRelease -ne $true) {
  throw "Release manifest does not accurately describe the verified reproducibility boundaries."
}
$frontendDigest = [string] $manifest.frontendTree.sha256
if ($frontendDigest -notmatch '^[0-9a-f]{64}$' -or [int] $manifest.frontendTree.fileCount -le 0 -or $manifest.frontendTree.name -cne "web/dist") {
  throw "Release manifest frontend tree evidence is invalid."
}

$expectedBinaryNames = @(
  "linux-amd64/llmgateway",
  "linux-amd64/llmgateway-dbtool",
  "linux-amd64/llmgateway-healthcheck",
  "windows-amd64/llmgateway.exe",
  "windows-amd64/llmgateway-dbtool.exe",
  "windows-amd64/llmgateway-healthcheck.exe"
)
$manifestBinaries = @($manifest.verifiedBinaries | ForEach-Object {
  if ($_.name -notmatch '^(?:linux-amd64|windows-amd64)/[A-Za-z0-9.-]+$' -or $_.sha256 -notmatch '^[0-9a-f]{64}$') {
    throw "Release manifest contains an invalid binary evidence entry."
  }
  [pscustomobject]@{ name = [string] $_.name; sha256 = ([string] $_.sha256).ToLowerInvariant() }
})
Assert-ExactNames -Actual @($manifestBinaries | ForEach-Object name) -Expected $expectedBinaryNames -Label "manifest binary evidence"
$binaryByName = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
foreach ($binary in $manifestBinaries) { $binaryByName.Add($binary.name, $binary.sha256) }

$manifestArchives = @($manifest.archives | ForEach-Object {
  $archiveEntries = @($_.entries | ForEach-Object {
    $entryName = [string] $_
    if (-not $entryName -or $entryName.Contains('\') -or $entryName.StartsWith('/') -or
        $entryName -notmatch '^[A-Za-z0-9][A-Za-z0-9._/-]*$' -or
        $entryName -match '(^|/)\.\.?(/|$)' -or $entryName.EndsWith('/')) {
      throw "Release manifest contains a non-canonical archive entry."
    }
    $entryName
  })
  [pscustomobject]@{ name = [string] $_.name; entries = $archiveEntries }
})
Assert-ExactNames -Actual @($manifestArchives | ForEach-Object name) -Expected @($windowsZipName, $linuxZipName) -Label "manifest archive evidence"
function Assert-ReleaseArchive {
  param(
    [Parameter(Mandatory = $true)][string] $Path,
    [Parameter(Mandatory = $true)][string[]] $BinaryNames,
    [Parameter(Mandatory = $true)][string[]] $ExpectedEntries,
    [Parameter(Mandatory = $true)][string] $Platform
  )
  foreach ($binaryName in $BinaryNames) {
    if ($ExpectedEntries -cnotcontains $binaryName) { throw "$Platform release archive is missing a runtime binary." }
  }
  $archiveFiles = @(Get-ArchiveFiles -Path $Path)
  Assert-ExactNames -Actual @($archiveFiles | ForEach-Object Name) -Expected $ExpectedEntries -Label "$Platform release archive manifest parity"
  if ($Platform -eq "linux-amd64") {
    $regularMode = [Convert]::ToInt32("100644", 8)
    $executableMode = [Convert]::ToInt32("100755", 8)
    foreach ($file in $archiveFiles) {
      $expectedMode = if ($file.Name -match '^deploy/[A-Za-z0-9._/-]+\.sh$' -or $BinaryNames -ccontains $file.Name) {
        $executableMode
      } else {
        $regularMode
      }
      if ($file.UnixMode -ne $expectedMode) { throw "Linux release archive mode is invalid for $($file.Name)." }
    }
  }
  foreach ($entry in $ExpectedEntries) {
    if ($entry -match '(?i)(^|/)(?:\.env|.*\.(?:key|pem|dump|log))$' -and $entry -notmatch '(?i)\.env\.example$') {
      throw "Release archive contains a forbidden sensitive-looking file: $entry"
    }
  }
}
foreach ($archiveEvidence in $manifestArchives) {
  $binaryNames = if ($archiveEvidence.name -eq $windowsZipName) { @("llmgateway.exe", "llmgateway-dbtool.exe", "llmgateway-healthcheck.exe") } else { @("llmgateway", "llmgateway-dbtool", "llmgateway-healthcheck") }
  $platform = if ($archiveEvidence.name -eq $windowsZipName) { "windows-amd64" } else { "linux-amd64" }
  Assert-ReleaseArchive -Path (Join-Path $Directory $archiveEvidence.name) -BinaryNames $binaryNames -ExpectedEntries $archiveEvidence.entries -Platform $platform
}

$spdx = Get-JsonDocument -Path (Join-Path $Directory $spdxName)
if ($spdx.spdxVersion -notmatch '^SPDX-[0-9]+\.[0-9]+$' -or @($spdx.packages).Count -eq 0) { throw "SPDX SBOM is empty or malformed." }
$cycloneDX = Get-JsonDocument -Path (Join-Path $Directory $cycloneDXName)
if ($cycloneDX.bomFormat -cne "CycloneDX" -or -not $cycloneDX.specVersion -or @($cycloneDX.components).Count -eq 0) { throw "CycloneDX SBOM is empty or malformed." }
if (@(Get-Content -Encoding UTF8 -LiteralPath (Join-Path $Directory "go-licenses.csv")).Count -eq 0) { throw "Go license report is empty." }
$nodeLicenses = Get-JsonDocument -Path (Join-Path $Directory "node-licenses.json")
if (@($nodeLicenses.PSObject.Properties).Count -eq 0) { throw "Node license report is empty." }

$provenance = Get-JsonDocument -Path (Join-Path $Directory "provenance.intoto.json")
if ($provenance._type -cne "https://in-toto.io/Statement/v1" -or $provenance.predicateType -cne "https://slsa.dev/provenance/v1") {
  throw "Release provenance type is invalid."
}
$external = $provenance.predicate.buildDefinition.externalParameters
if ($provenance.predicate.buildDefinition.buildType -cne "https://github.com/luckymaomi/llmgateway/release-build" -or
    $external.version -cne $version -or
    $external.revision -cne $ExpectedRevision -or
    $external.builtAt -cne $builtAt -or
    $external.signingMode -cne $(if ($Keyless) { "Keyless" } else { "Test" })) {
  throw "Release provenance build parameters do not match the expected identity."
}
Assert-ExactNames -Actual @($external.platforms) -Expected @("windows/amd64", "linux/amd64") -Label "provenance platforms"
$internal = $provenance.predicate.buildDefinition.internalParameters
if ($internal.cgo -cne "disabled" -or $internal.trimpath -ne $true -or $internal.webembed -ne $true -or $internal.sourceTreeClean -ne ($ExpectedRevision -ne "working-tree")) {
  throw "Release provenance internal build parameters are invalid."
}
$dependencies = @($provenance.predicate.buildDefinition.resolvedDependencies)
if ($ExpectedRevision -eq "working-tree") {
  if ($dependencies.Count -ne 0) { throw "working-tree provenance must not claim a Git dependency digest." }
} elseif ($dependencies.Count -ne 1 -or
          $dependencies[0].uri -cne "git+https://github.com/luckymaomi/llmgateway@$ExpectedRevision" -or
          $dependencies[0].digest.gitCommit -cne $ExpectedRevision) {
  throw "Release provenance Git dependency does not match ExpectedRevision."
}
$expectedWorkflowIdentity = "https://github.com/luckymaomi/llmgateway/.github/workflows/verify.yml@refs/tags/v$version"
$expectedBuilder = if ($Keyless) { $expectedWorkflowIdentity } else { "https://github.com/luckymaomi/llmgateway/local-release-acceptance" }
$metadata = $provenance.predicate.runDetails.metadata
if ($provenance.predicate.runDetails.builder.id -cne $expectedBuilder -or
    $metadata.reproducible -ne $false -or
    $metadata.reproducibility.allArtifacts -ne $false -or
    $metadata.reproducibility.goBinaries -ne $true -or
    $metadata.reproducibility.frontendTree -ne $true -or
    $metadata.containerBinariesMatchLinuxRelease -ne $true) {
  throw "Release provenance does not accurately describe verified reproducibility."
}
[Guid] $invocation = [Guid]::Empty
if (-not [Guid]::TryParse([string] $metadata.invocationId, [ref] $invocation)) { throw "Release provenance invocationId is invalid." }

$expectedSubjects = @($manifestBinaries | ForEach-Object { [pscustomobject]@{ name = $_.name; sha256 = $_.sha256 } })
$expectedSubjects += [pscustomobject]@{ name = "web/dist"; sha256 = $frontendDigest }
$subjects = @($provenance.subject | ForEach-Object {
  if (-not $_.name -or $_.digest.sha256 -notmatch '^[0-9a-f]{64}$') { throw "Release provenance contains an invalid subject." }
  [pscustomobject]@{ name = [string] $_.name; sha256 = ([string] $_.digest.sha256).ToLowerInvariant() }
})
Assert-ExactNames -Actual @($subjects | ForEach-Object name) -Expected @($expectedSubjects | ForEach-Object name) -Label "provenance subjects"
foreach ($subject in $expectedSubjects) {
  $matches = @($subjects | Where-Object { $_.name -ceq $subject.name })
  if ($matches.Count -ne 1 -or $matches[0].sha256 -cne $subject.sha256) { throw "Provenance subject does not match manifest: $($subject.name)" }
}

$verificationDirectory = Join-Path ([IO.Path]::GetTempPath()) "llmgateway-release-verify-$([guid]::NewGuid().ToString('N'))"
try {
  $windowsExtract = Join-Path $verificationDirectory "windows"
  $linuxExtract = Join-Path $verificationDirectory "linux"
  Expand-ArchiveSafely -ArchivePath (Join-Path $Directory $windowsZipName) -Destination $windowsExtract
  Expand-ArchiveSafely -ArchivePath (Join-Path $Directory $linuxZipName) -Destination $linuxExtract
  foreach ($binary in @("llmgateway", "llmgateway-dbtool", "llmgateway-healthcheck")) {
    $linuxName = "linux-amd64/$binary"
    $windowsName = "windows-amd64/$binary.exe"
    $linuxHash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $linuxExtract $binary)).Hash.ToLowerInvariant()
    $windowsHash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $windowsExtract ($binary + ".exe"))).Hash.ToLowerInvariant()
    if ($binaryByName[$linuxName] -cne $linuxHash -or $binaryByName[$windowsName] -cne $windowsHash) {
      throw "Archive binary hash does not match manifest evidence: $binary"
    }
  }
} finally {
  if (Test-Path -LiteralPath $verificationDirectory) { Remove-Item -LiteralPath $verificationDirectory -Recurse -Force }
}

$bundlePath = Join-Path $Directory "SHA256SUMS.sigstore.json"
$bundle = Get-JsonDocument -Path $bundlePath
if ($null -eq $bundle) { throw "Signature bundle is empty." }
$cosign = Install-LLMGatewayReleaseTool -Name cosign
if ($Keyless) {
  & $cosign verify-blob --bundle $bundlePath --certificate-identity $expectedWorkflowIdentity --certificate-oidc-issuer "https://token.actions.githubusercontent.com" $checksumPath | Out-Null
} else {
  $includedPublicKey = Join-Path $Directory "release-acceptance.pub"
  if (-not (Test-Path -LiteralPath $includedPublicKey -PathType Leaf)) { throw "Test release public key is missing." }
  if ($PublicKey) {
    $externalPublicKey = [IO.Path]::GetFullPath($PublicKey)
    if (-not (Test-Path -LiteralPath $externalPublicKey -PathType Leaf) -or
        (Get-FileHash -Algorithm SHA256 -LiteralPath $externalPublicKey).Hash.ToLowerInvariant() -cne (Get-FileHash -Algorithm SHA256 -LiteralPath $includedPublicKey).Hash.ToLowerInvariant()) {
      throw "The supplied Test public key does not match release-acceptance.pub."
    }
  } else {
    $externalPublicKey = $includedPublicKey
  }
  & $cosign verify-blob --insecure-ignore-tlog --key $externalPublicKey --bundle $bundlePath $checksumPath | Out-Null
}
if ($LASTEXITCODE -ne 0) { throw "Release signature verification failed." }

Write-Host "Release file set, exact checksums, manifest, SBOMs, zip contents, provenance subjects, archive parity, and signature passed verification."
