$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

Push-Location (Join-Path $PSScriptRoot "..")
try {
  Write-Host "Validating Docker Compose configuration..."
  Invoke-LLMGatewayDocker compose config --quiet

  Write-Host "Starting PostgreSQL and Valkey..."
  Invoke-LLMGatewayDocker compose up --detach
  Wait-LLMGatewayContainerHealthy -Container "llmgateway-postgres"
  Wait-LLMGatewayContainerHealthy -Container "llmgateway-valkey"
  Test-LLMGatewayPostgres
  Test-LLMGatewayValkey

  Write-Host "LLMGateway development infrastructure is ready."
  Write-Host "PostgreSQL: 127.0.0.1:15432"
  Write-Host "Valkey:     127.0.0.1:16380"
} finally {
  Pop-Location
}
