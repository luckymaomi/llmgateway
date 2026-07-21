function Get-AcceptanceLoopbackPort {
  $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
  try {
    $listener.Start()
    return ([System.Net.IPEndPoint] $listener.LocalEndpoint).Port
  } finally {
    $listener.Stop()
  }
}

function Start-AcceptanceProcess {
  param(
    [Parameter(Mandatory = $true)][string] $BinaryPath,
    [Parameter(Mandatory = $true)][string] $StandardOutputPath,
    [Parameter(Mandatory = $true)][string] $StandardErrorPath
  )

  $arguments = @{
    FilePath               = $BinaryPath
    PassThru               = $true
    RedirectStandardOutput = $StandardOutputPath
    RedirectStandardError  = $StandardErrorPath
  }
  if ($env:OS -eq "Windows_NT") { $arguments.WindowStyle = "Hidden" }
  return Start-Process @arguments
}

function Wait-AcceptanceReadiness {
  param(
    [Parameter(Mandatory = $true)][System.Diagnostics.Process] $Process,
    [Parameter(Mandatory = $true)][string] $ReadinessURL,
    [int] $TimeoutSeconds = 60
  )

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    if ($Process.HasExited) {
      throw "Acceptance process exited before readiness."
    }
    try {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $ReadinessURL -TimeoutSec 2
      if ($response.StatusCode -eq 200) { return }
    } catch {
      if ((Get-Date) -ge $deadline) { break }
    }
    Start-Sleep -Milliseconds 100
  } while ((Get-Date) -lt $deadline)
  throw "Acceptance process did not become ready within $TimeoutSeconds seconds."
}

function Stop-AcceptanceProcess {
  param(
    [System.Diagnostics.Process] $Process,
    [Parameter(Mandatory = $true)][string] $ExpectedBinaryPath
  )

  if ($null -eq $Process -or $Process.HasExited) { return }
  $live = Get-Process -Id $Process.Id -ErrorAction SilentlyContinue
  if ($null -eq $live) { return }
  $expected = [System.IO.Path]::GetFullPath($ExpectedBinaryPath)
  $actual = [System.IO.Path]::GetFullPath($live.Path)
  if (-not [string]::Equals($expected, $actual, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to stop a process that does not own the acceptance binary."
  }
  $live.Kill()
  $live.WaitForExit(15000) | Out-Null
  if ($null -ne (Get-Process -Id $live.Id -ErrorAction SilentlyContinue)) {
    throw "Acceptance process did not stop."
  }
}

function Remove-AcceptanceBuildDirectory {
  param(
    [Parameter(Mandatory = $true)][string] $RepositoryRoot,
    [Parameter(Mandatory = $true)][string] $BuildDirectory,
    [Parameter(Mandatory = $true)][string] $ExpectedPrefix
  )

  $buildRoot = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot ".build"))
  $resolved = [System.IO.Path]::GetFullPath($BuildDirectory)
  $requiredRoot = $buildRoot.TrimEnd([System.IO.Path]::DirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
  if (-not $resolved.StartsWith($requiredRoot, [System.StringComparison]::OrdinalIgnoreCase) -or
      -not [System.IO.Path]::GetFileName($resolved).StartsWith($ExpectedPrefix, [System.StringComparison]::Ordinal)) {
    throw "Refusing to remove an unowned acceptance build directory."
  }
  if (Test-Path -LiteralPath $resolved) {
    Remove-Item -LiteralPath $resolved -Recurse -Force
  }
}
