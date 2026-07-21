$script:ReleaseToolSpecifications = @{
  trivy = @{
    Version = "0.72.0"
    WindowsAsset = "trivy_0.72.0_windows-64bit.zip"
    LinuxAsset = "trivy_0.72.0_Linux-64bit.tar.gz"
    Checksums = "trivy_0.72.0_checksums.txt"
    BaseURL = "https://github.com/aquasecurity/trivy/releases/download/v0.72.0"
    Executable = "trivy"
  }
  gitleaks = @{
    Version = "8.30.1"
    WindowsAsset = "gitleaks_8.30.1_windows_x64.zip"
    LinuxAsset = "gitleaks_8.30.1_linux_x64.tar.gz"
    Checksums = "gitleaks_8.30.1_checksums.txt"
    BaseURL = "https://github.com/gitleaks/gitleaks/releases/download/v8.30.1"
    Executable = "gitleaks"
  }
  syft = @{
    Version = "1.49.0"
    WindowsAsset = "syft_1.49.0_windows_amd64.zip"
    LinuxAsset = "syft_1.49.0_linux_amd64.tar.gz"
    Checksums = "syft_1.49.0_checksums.txt"
    BaseURL = "https://github.com/anchore/syft/releases/download/v1.49.0"
    Executable = "syft"
  }
  cosign = @{
    Version = "3.1.2"
    WindowsAsset = "cosign-windows-amd64.exe"
    LinuxAsset = "cosign-linux-amd64"
    Checksums = "cosign_checksums.txt"
    BaseURL = "https://github.com/sigstore/cosign/releases/download/v3.1.2"
    Executable = "cosign"
  }
}

function Install-LLMGatewayReleaseTool {
  param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("trivy", "gitleaks", "syft", "cosign")]
    [string] $Name
  )

  $root = Split-Path -Parent $PSScriptRoot
  $specification = $script:ReleaseToolSpecifications[$Name]
  $platform = if ($env:OS -eq "Windows_NT") { "windows-amd64" } else { "linux-amd64" }
  $asset = if ($env:OS -eq "Windows_NT") { $specification.WindowsAsset } else { $specification.LinuxAsset }
  $toolDirectory = [IO.Path]::GetFullPath((Join-Path $root ".build\tools\$Name-$($specification.Version)-$platform"))
  New-Item -ItemType Directory -Force -Path $toolDirectory | Out-Null
  $assetPath = Join-Path $toolDirectory $asset
  $checksumPath = Join-Path $toolDirectory $specification.Checksums
  if (-not (Test-Path -LiteralPath $assetPath)) {
    Invoke-WebRequest -UseBasicParsing -Uri "$($specification.BaseURL)/$asset" -OutFile $assetPath -TimeoutSec 300
  }
  if (-not (Test-Path -LiteralPath $checksumPath)) {
    Invoke-WebRequest -UseBasicParsing -Uri "$($specification.BaseURL)/$($specification.Checksums)" -OutFile $checksumPath -TimeoutSec 60
  }
  $checksumLine = Get-Content -Encoding ascii -LiteralPath $checksumPath | Where-Object { $_ -match " $([regex]::Escape($asset))$" } | Select-Object -First 1
  $expectedChecksum = [string]($checksumLine -split '\s+')[0]
  $actualChecksum = (Get-FileHash -Algorithm SHA256 -LiteralPath $assetPath).Hash.ToLowerInvariant()
  if (-not $expectedChecksum -or $actualChecksum -ne $expectedChecksum.ToLowerInvariant()) {
    throw "$Name $($specification.Version) did not match its official release checksum."
  }

  $executableName = $specification.Executable + $(if ($env:OS -eq "Windows_NT") { ".exe" } else { "" })
  $executablePath = Join-Path $toolDirectory $executableName
  if (-not (Test-Path -LiteralPath $executablePath)) {
    if ($asset.EndsWith(".zip", [StringComparison]::OrdinalIgnoreCase)) {
      Expand-Archive -LiteralPath $assetPath -DestinationPath $toolDirectory
    } elseif ($asset.EndsWith(".tar.gz", [StringComparison]::OrdinalIgnoreCase)) {
      & tar -xzf $assetPath -C $toolDirectory
      if ($LASTEXITCODE -ne 0) { throw "Could not extract $asset." }
    } else {
      Copy-Item -LiteralPath $assetPath -Destination $executablePath
    }
  }
  if (-not (Test-Path -LiteralPath $executablePath)) {
    throw "$asset did not contain $executableName."
  }
  if ($env:OS -ne "Windows_NT") {
    & chmod 0755 $executablePath
    if ($LASTEXITCODE -ne 0) { throw "Could not make $executablePath executable." }
  }
  return $executablePath
}
