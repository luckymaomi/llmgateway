$ErrorActionPreference = "Stop"

. "$PSScriptRoot\isolated-services.ps1"

Push-Location (Join-Path $PSScriptRoot "..")
$runID = New-LLMGatewayTestRunID -Purpose "control"
$postgres = $null
$valkey = $null
$environmentSnapshot = Save-LLMGatewayEnvironment
$testFailure = $null
try {
  Clear-LLMGatewayEnvironment
  $postgres = Start-LLMGatewayTestPostgres -RunID $runID -DatabaseName "llmgateway_control" -Password "control-postgres-fixture"
  $valkeyPassword = "control-valkey-fixture"
  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $valkeyPassword
  $env:LLMGATEWAY_CONTROL_TEST_DATABASE_URL = $postgres.DatabaseURL
  $env:LLMGATEWAY_CONTROL_TEST_VALKEY_ADDRESS = $valkey.Address
  $env:LLMGATEWAY_CONTROL_TEST_VALKEY_PASSWORD = $valkeyPassword
  $env:LLMGATEWAY_CONTROL_TEST_VALKEY_DATABASE = "0"
  $env:LLMGATEWAY_CONTROL_TEST_REQUIRED = "true"

  & go test ./internal/store -run '^(TestProviderMutation|TestConfigurationPublication|TestGatewayKeyRevocation|TestIdentityRepository|TestInvitationMutation|TestQuotaGrantMutation)' -count=1 -v
  if ($LASTEXITCODE -ne 0) {
    throw "Control-plane repository persistence tests failed."
  }
  & go test ./internal/controlapi -run '^TestPersistentProviderControlLifecycle$' -count=1 -v
  if ($LASTEXITCODE -ne 0) {
    throw "Control API persistence test failed."
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
  if ($null -ne $valkey) {
    try {
      Stop-LLMGatewayTestContainer -Container $valkey.Container -RunID $runID
    } catch {
      $cleanupFailures += "Valkey cleanup: $($_.Exception.Message)"
    }
  }
  try {
    Pop-Location
  } catch {
    $cleanupFailures += "location restore: $($_.Exception.Message)"
  }
  if ($null -ne $testFailure) {
    if ($cleanupFailures.Count -gt 0) {
      throw "Control API persistence test failed: $($testFailure.Exception.Message) Cleanup also failed: $($cleanupFailures -join '; ')"
    }
    throw $testFailure
  }
  if ($cleanupFailures.Count -gt 0) {
    throw "Control API persistence cleanup failed: $($cleanupFailures -join '; ')"
  }
}

Write-Host "Control API persistence passed in an isolated PostgreSQL container."
