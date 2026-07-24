$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"
. "$PSScriptRoot\dev-processes.ps1"

$root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
Stop-LLMGatewayDevelopmentProcesses -Root $root

Push-Location $root
try {
  Invoke-LLMGatewayDocker compose down
  Write-Host "LLMGateway development processes and infrastructure stopped. Named volumes were preserved."
} finally {
  Pop-Location
}
