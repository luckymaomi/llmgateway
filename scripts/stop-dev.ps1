$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

Push-Location (Join-Path $PSScriptRoot "..")
try {
  Invoke-LLMGatewayDocker compose down
  Write-Host "LLMGateway development infrastructure stopped. Named volumes were preserved."
} finally {
  Pop-Location
}
