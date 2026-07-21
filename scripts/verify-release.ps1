[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string] $Directory,
  [string] $PublicKey = "",
  [switch] $Keyless
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\release-tools.ps1"

$Directory = [IO.Path]::GetFullPath($Directory)
if (-not (Test-Path -LiteralPath $Directory -PathType Container)) { throw "Release directory does not exist." }
$checksumPath = Join-Path $Directory "SHA256SUMS"
$bundlePath = Join-Path $Directory "SHA256SUMS.sigstore.json"
$manifestPath = Join-Path $Directory "release-manifest.json"
$provenancePath = Join-Path $Directory "provenance.intoto.json"
foreach ($required in @($checksumPath, $bundlePath, $manifestPath, $provenancePath)) {
  if (-not (Test-Path -LiteralPath $required -PathType Leaf)) { throw "Release file is missing: $required" }
}

$checksums = @{}
foreach ($line in Get-Content -Encoding utf8 -LiteralPath $checksumPath) {
  if ($line -notmatch '^([0-9a-f]{64})  ([^\\/]+)$') { throw "Invalid SHA256SUMS line." }
  $expected = $Matches[1]
  $name = $Matches[2]
  if ($checksums.ContainsKey($name)) { throw "Duplicate SHA256SUMS entry: $name" }
  $path = Join-Path $Directory $name
  if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Checksummed release file is missing: $name" }
  $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
  if ($actual -ne $expected) { throw "Checksum mismatch: $name" }
  $checksums[$name] = $expected
}
if ($checksums.Count -lt 8) { throw "Release checksum manifest is unexpectedly small." }

$manifest = Get-Content -Raw -Encoding utf8 -LiteralPath $manifestPath | ConvertFrom-Json
if (-not $manifest.version -or -not $manifest.revision -or -not $manifest.builtAt -or -not $manifest.reproducibleBinaries) {
  throw "Release manifest does not contain a reproducible build identity."
}
$provenance = Get-Content -Raw -Encoding utf8 -LiteralPath $provenancePath | ConvertFrom-Json
if ($provenance._type -ne "https://in-toto.io/Statement/v1" -or $provenance.predicateType -ne "https://slsa.dev/provenance/v1" -or -not $provenance.predicate.runDetails.metadata.reproducible) {
  throw "Release provenance does not satisfy the expected in-toto/SLSA contract."
}
foreach ($subject in @($provenance.subject)) {
  $path = Join-Path $Directory $subject.name
  if (-not (Test-Path -LiteralPath $path -PathType Leaf) -or (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant() -ne $subject.digest.sha256) {
    throw "Provenance subject does not match: $($subject.name)"
  }
}
foreach ($sbom in Get-ChildItem -LiteralPath $Directory -File | Where-Object { $_.Name -match '\.(spdx|cdx)\.json$' }) {
  $null = Get-Content -Raw -Encoding utf8 -LiteralPath $sbom.FullName | ConvertFrom-Json
}

$cosign = Install-LLMGatewayReleaseTool -Name cosign
if ($Keyless) {
  & $cosign verify-blob --bundle $bundlePath --certificate-identity-regexp '^https://github.com/luckymaomi/llmgateway/' --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' $checksumPath | Out-Null
} else {
  if (-not $PublicKey) { $PublicKey = Join-Path $Directory "release-acceptance.pub" }
  & $cosign verify-blob --insecure-ignore-tlog --key $PublicKey --bundle $bundlePath $checksumPath | Out-Null
}
if ($LASTEXITCODE -ne 0) { throw "Release signature verification failed." }

Write-Host "Release checksums, manifest, SBOM JSON, provenance subjects, and signature passed verification."
