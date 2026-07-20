$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

Push-Location (Join-Path $PSScriptRoot "..")
$databaseName = "llmgateway_control_test_$PID"
try {
  $docker = Get-LLMGatewayDockerCommand
  & $docker exec llmgateway-postgres psql -v ON_ERROR_STOP=1 -U llmgateway -d postgres -c "CREATE DATABASE $databaseName" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Could not create control API test database." }

  $env:LLMGATEWAY_CONTROL_TEST_DATABASE_URL = "postgres://llmgateway:llmgateway_dev@127.0.0.1:15432/$databaseName`?sslmode=disable"
  & go test ./internal/controlapi -run '^TestPersistentProviderControlLifecycle$' -count=1
  if ($LASTEXITCODE -ne 0) { throw "Control API persistence test failed." }
} finally {
  Remove-Item Env:LLMGATEWAY_CONTROL_TEST_DATABASE_URL -ErrorAction SilentlyContinue
  $docker = Get-LLMGatewayDockerCommand
  & $docker exec llmgateway-postgres psql -v ON_ERROR_STOP=1 -U llmgateway -d postgres -c "DROP DATABASE IF EXISTS $databaseName WITH (FORCE)" | Out-Null
  Pop-Location
}
