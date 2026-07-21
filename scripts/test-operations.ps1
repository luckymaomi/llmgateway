$ErrorActionPreference = "Stop"

. "$PSScriptRoot\isolated-services.ps1"

function Get-FreeLoopbackPort {
  $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
  $listener.Start()
  try { return ([Net.IPEndPoint]$listener.LocalEndpoint).Port } finally { $listener.Stop() }
}

$root = Split-Path -Parent $PSScriptRoot
$runID = "operations-$([guid]::NewGuid().ToString('N'))"
$databaseName = "llmgateway_operations"
$restoredDatabaseName = "llmgateway_operations_restore"
$password = "operations-$runID"
$buildDirectory = Join-Path $root ".build\operations-$runID"
$backupPath = Join-Path $buildDirectory "llmgateway.dump"
$environmentSnapshot = Save-LLMGatewayEnvironment
$postgres = $null
$valkey = $null
$gateway = $null
$failure = $null

Push-Location $root
try {
  New-Item -ItemType Directory -Force -Path $buildDirectory | Out-Null
  $postgres = Start-LLMGatewayTestPostgres -RunID $runID -DatabaseName $databaseName -Password $password
  $env:LLMGATEWAY_OPERATIONS_TEST_DATABASE_URL = $postgres.DatabaseURL
  $env:LLMGATEWAY_OPERATIONS_TEST_REQUIRED = "true"
  $env:LLMGATEWAY_PROFILE = "test"
  $env:LLMGATEWAY_DATABASE_URL = $postgres.DatabaseURL
  $oldKey = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("0123456789abcdef0123456789abcdef"))
  $newKey = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("fedcba9876543210fedcba9876543210"))
  $env:LLMGATEWAY_MASTER_KEYS = "1:$oldKey,2:$newKey"
  $env:LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION = "2"

  go test ./internal/store -run '^TestProviderCredentialMasterKeyRotationIsAtomicAndIdempotent$' -count=1
  if ($LASTEXITCODE -ne 0) { throw "Credential master key rotation integration test failed." }
  go run ./cmd/dbtool -action rotate-credentials -confirm-key-rotation
  if ($LASTEXITCODE -ne 0) { throw "Credential rotation command failed." }

  & powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\backup-postgres.ps1 `
    -OutputPath $backupPath -Container $postgres.Container -DatabaseName $databaseName -DatabaseUser llmgateway -AllowIsolatedTestContainer
  if ($LASTEXITCODE -ne 0) { throw "PostgreSQL backup command failed." }
  & powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\restore-postgres.ps1 `
    -InputPath $backupPath -Container $postgres.Container -TargetDatabase $restoredDatabaseName -DatabaseUser llmgateway -ConfirmRestore -AllowIsolatedTestContainer
  if ($LASTEXITCODE -ne 0) { throw "PostgreSQL restore command failed." }

  $docker = Get-LLMGatewayDockerCommand
  $restoredFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d $restoredDatabaseName -Atc `
    "SELECT (SELECT count(*) FROM provider_credentials WHERE name = 'Rotation Credential') || '|' || (SELECT count(*) FROM audit_events WHERE action = 'credential.encryption_rotated') || '|' || (SELECT max(version_id) FROM goose_db_version WHERE is_applied = true)"
  if ($LASTEXITCODE -ne 0 -or $restoredFacts -ne "1|1|1") {
    throw "Restored PostgreSQL facts are incomplete: $restoredFacts"
  }

  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $password
  $gatewayPort = Get-FreeLoopbackPort
  $binaryPath = Join-Path $buildDirectory "llmgateway-windows-amd64.exe"
  $stdoutPath = Join-Path $buildDirectory "gateway.stdout.log"
  $stderrPath = Join-Path $buildDirectory "gateway.stderr.log"
  pnpm.cmd --dir web run build
  if ($LASTEXITCODE -ne 0) { throw "Production frontend build failed." }
  go build -tags webembed -trimpath -o $binaryPath .\cmd\gateway
  if ($LASTEXITCODE -ne 0) { throw "Windows amd64 production binary build failed." }

  $env:LLMGATEWAY_PROFILE = "production"
  $env:LLMGATEWAY_DATABASE_URL = $postgres.DatabaseURL -replace "/$databaseName\?", "/$restoredDatabaseName`?"
  $env:LLMGATEWAY_DATABASE_MIGRATE_ON_START = "false"
  $env:LLMGATEWAY_DATABASE_MIN_CONNECTIONS = "1"
  $env:LLMGATEWAY_DATABASE_MAX_CONNECTIONS = "4"
  $env:LLMGATEWAY_VALKEY_ADDRESS = $valkey.Address
  $env:LLMGATEWAY_VALKEY_PASSWORD = $password
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$gatewayPort"
  $env:LLMGATEWAY_SESSION_PEPPER = "operations-session-pepper-$runID"
  $env:LLMGATEWAY_API_KEY_PEPPER = "operations-api-key-pepper-$runID"
  $env:LLMGATEWAY_COORDINATION_KEY_HASH_SECRET = "operations-coordination-secret-$runID"
  $gateway = Start-Process -FilePath $binaryPath -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
  $deadline = (Get-Date).AddSeconds(20)
  do {
    if ($gateway.HasExited) { throw "Production gateway exited before readiness." }
    try {
      $ready = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$gatewayPort/health/ready" -TimeoutSec 1
      if ([int]$ready.StatusCode -eq 200) { break }
    } catch {}
    Start-Sleep -Milliseconds 100
  } while ((Get-Date) -lt $deadline)
  if ($null -eq $ready -or [int]$ready.StatusCode -ne 200) { throw "Production gateway did not become ready." }
  $web = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$gatewayPort/" -TimeoutSec 5
  if ([int]$web.StatusCode -ne 200 -or $web.Content -notmatch '<div id="root"></div>') {
    throw "Embedded production frontend was not served by the Windows amd64 gateway."
  }
} catch {
  $failure = $_
} finally {
  $cleanupFailures = @()
  if ($null -ne $gateway -and -not $gateway.HasExited) {
    try { Stop-Process -Id $gateway.Id -Force; $null = $gateway.WaitForExit(5000) } catch { $cleanupFailures += "gateway cleanup: $($_.Exception.Message)" }
  }
  try { Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot } catch { $cleanupFailures += "environment restore: $($_.Exception.Message)" }
  if ($null -ne $valkey) {
    try { Stop-LLMGatewayTestContainer -Container $valkey.Container -RunID $runID } catch { $cleanupFailures += "Valkey cleanup: $($_.Exception.Message)" }
  }
  if ($null -ne $postgres) {
    try { Stop-LLMGatewayTestContainer -Container $postgres.Container -RunID $runID } catch { $cleanupFailures += "PostgreSQL cleanup: $($_.Exception.Message)" }
  }
  try {
    $resolvedBuild = [IO.Path]::GetFullPath($buildDirectory)
    $resolvedRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
    if ($resolvedBuild.StartsWith($resolvedRoot, [StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $resolvedBuild)) {
      Remove-Item -LiteralPath $resolvedBuild -Recurse -Force
    }
  } catch { $cleanupFailures += "build cleanup: $($_.Exception.Message)" }
  try { Pop-Location } catch { $cleanupFailures += "location restore: $($_.Exception.Message)" }
  if ($null -ne $failure) {
    if ($cleanupFailures.Count -gt 0) { throw "Operations test failed: $($failure.Exception.Message) Cleanup also failed: $($cleanupFailures -join '; ')" }
    throw $failure
  }
  if ($cleanupFailures.Count -gt 0) { throw "Operations cleanup failed: $($cleanupFailures -join '; ')" }
}

Write-Host "Credential master key rotation, PostgreSQL backup/restore, and the Windows amd64 production runtime passed in an isolated environment."
