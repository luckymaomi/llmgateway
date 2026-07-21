$ErrorActionPreference = "Stop"

. "$PSScriptRoot\isolated-services.ps1"

Push-Location (Join-Path $PSScriptRoot "..")
$runID = New-LLMGatewayTestRunID -Purpose "migrations"
$postgres = $null
$environmentSnapshot = Save-LLMGatewayEnvironment
$testFailure = $null
try {
  Clear-LLMGatewayEnvironment
  $postgres = Start-LLMGatewayTestPostgres -RunID $runID -DatabaseName "llmgateway_migrations" -Password "migration-postgres-fixture"

  $env:LLMGATEWAY_PROFILE = "test"
  $env:LLMGATEWAY_DATABASE_URL = $postgres.DatabaseURL
  $env:LLMGATEWAY_MASTER_KEYS = "1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  $env:LLMGATEWAY_SESSION_PEPPER = "llmgateway-test-session-pepper-000000"
  $env:LLMGATEWAY_API_KEY_PEPPER = "llmgateway-test-api-key-pepper-000000"

  & go run .\cmd\dbtool --action up
  if ($LASTEXITCODE -ne 0) {
    throw "Migration up failed."
  }

  $docker = Get-LLMGatewayDockerCommand
  $tableCount = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_migrations -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'"
  if ($LASTEXITCODE -ne 0 -or [int]$tableCount -lt 20) {
    throw "Migration schema is incomplete; found $tableCount public tables."
  }

  & go run .\cmd\dbtool --action rebuild --confirm-development-data-loss
  if ($LASTEXITCODE -ne 0) {
    throw "Migration rebuild failed."
  }

  $rebuiltTableCount = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_migrations -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'"
  if ($LASTEXITCODE -ne 0 -or [int]$rebuiltTableCount -lt 20) {
    throw "Migration rebuild did not restore the schema; found $rebuiltTableCount public tables."
  }

} catch {
  $testFailure = $_
} finally {
  $cleanupFailures = @()
  try {
    Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot
  } catch {
    $cleanupFailures += "environment restore: $($_.Exception.Message)"
  }
  if ($null -ne $postgres) {
    try {
      Stop-LLMGatewayTestContainer -Container $postgres.Container -RunID $runID
    } catch {
      $cleanupFailures += "PostgreSQL cleanup: $($_.Exception.Message)"
    }
  }
  try {
    Pop-Location
  } catch {
    $cleanupFailures += "location restore: $($_.Exception.Message)"
  }
  if ($null -ne $testFailure) {
    if ($cleanupFailures.Count -gt 0) {
      throw "Migration round-trip failed: $($testFailure.Exception.Message) Cleanup also failed: $($cleanupFailures -join '; ')"
    }
    throw $testFailure
  }
  if ($cleanupFailures.Count -gt 0) {
    throw "Migration round-trip cleanup failed: $($cleanupFailures -join '; ')"
  }
}

Write-Host "Migration round-trip passed in an isolated PostgreSQL container."
