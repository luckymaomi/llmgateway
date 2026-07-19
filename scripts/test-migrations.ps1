$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

Push-Location (Join-Path $PSScriptRoot "..")
$databaseName = "llmgateway_test_$PID"
try {
  $docker = Get-LLMGatewayDockerCommand
  & $docker exec llmgateway-postgres psql -v ON_ERROR_STOP=1 -U llmgateway -d postgres -c "CREATE DATABASE $databaseName" | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "Could not create isolated migration database."
  }

  $env:LLMGATEWAY_PROFILE = "test"
  $env:LLMGATEWAY_DATABASE_URL = "postgres://llmgateway:llmgateway_dev@127.0.0.1:15432/$databaseName`?sslmode=disable"
  $env:LLMGATEWAY_MASTER_KEYS = "1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  $env:LLMGATEWAY_SESSION_PEPPER = "llmgateway-test-session-pepper-000000"
  $env:LLMGATEWAY_API_KEY_PEPPER = "llmgateway-test-api-key-pepper-000000"

  & go run .\cmd\dbtool --action up
  if ($LASTEXITCODE -ne 0) {
    throw "Migration up failed."
  }

  $tableCount = & $docker exec llmgateway-postgres psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'"
  if ($LASTEXITCODE -ne 0 -or [int]$tableCount -lt 20) {
    throw "Migration schema is incomplete; found $tableCount public tables."
  }

  & go run .\cmd\dbtool --action rebuild --confirm-development-data-loss
  if ($LASTEXITCODE -ne 0) {
    throw "Migration rebuild failed."
  }

  $rebuiltTableCount = & $docker exec llmgateway-postgres psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'"
  if ($LASTEXITCODE -ne 0 -or [int]$rebuiltTableCount -lt 20) {
    throw "Migration rebuild did not restore the schema; found $rebuiltTableCount public tables."
  }

  Write-Host "Migration round-trip passed in isolated database $databaseName."
} finally {
  Remove-Item Env:LLMGATEWAY_DATABASE_URL -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_MASTER_KEYS -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_SESSION_PEPPER -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_API_KEY_PEPPER -ErrorAction SilentlyContinue
  $docker = Get-LLMGatewayDockerCommand
  & $docker exec llmgateway-postgres psql -v ON_ERROR_STOP=1 -U llmgateway -d postgres -c "DROP DATABASE IF EXISTS $databaseName WITH (FORCE)" | Out-Null
  Pop-Location
}
