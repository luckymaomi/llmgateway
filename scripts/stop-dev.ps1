$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

function Assert-NoLLMGatewayDevelopmentProcess {
  $root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
  $buildRoot = [System.IO.Path]::GetFullPath((Join-Path $root ".build")).TrimEnd('\') + '\'
  foreach ($process in @(Get-Process -Name "gateway" -ErrorAction SilentlyContinue)) {
    try {
      $path = [string] $process.Path
    } catch {
      $path = ""
    }
    if (-not $path.StartsWith($buildRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
      continue
    }
    $relativePath = $path.Substring($buildRoot.Length)
    if ($relativePath -match '^dev-[^\\]+\\gateway\.exe$') {
      throw "LLMGateway development process $($process.Id) is still running. Stop start_dev.py with Ctrl+C before stopping infrastructure."
    }
  }
}

Assert-NoLLMGatewayDevelopmentProcess

Push-Location (Join-Path $PSScriptRoot "..")
try {
  Invoke-LLMGatewayDocker compose down
  Write-Host "LLMGateway development infrastructure stopped. Named volumes were preserved."
} finally {
  Pop-Location
}
