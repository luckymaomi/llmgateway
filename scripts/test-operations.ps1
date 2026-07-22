$ErrorActionPreference = "Stop"
$runningOnWindows = $env:OS -eq "Windows_NT"
$pnpmCommand = if ($runningOnWindows) { "pnpm.cmd" } else { "pnpm" }
$powerShellCommand = if ($runningOnWindows) { "powershell" } else { "pwsh" }
$runtimeName = if ($runningOnWindows) { "Windows amd64" } else { "Linux amd64" }

. (Join-Path $PSScriptRoot "isolated-services.ps1")

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
$buildDirectory = Join-Path (Join-Path $root ".build") "operations-$runID"
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

  $recoveryEmail = "operations-admin@example.test"
  $recoveryPassword = "operations-recovery-password-$runID"
  $recoveryPasswordPath = Join-Path $buildDirectory "administrator-password.txt"
  $docker = Get-LLMGatewayDockerCommand
  & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -c `
    "INSERT INTO users (email, display_name, password_hash, role, status, approved_at, disabled_at) VALUES ('$recoveryEmail', 'Operations Administrator', 'fixture-hash', 'administrator', 'disabled', now(), now()); INSERT INTO sessions (user_id, token_digest, csrf_digest, expires_at) SELECT id, digest('operations-recovery-session', 'sha256'), digest('operations-recovery-csrf', 'sha256'), now() + interval '1 hour' FROM users WHERE email = '$recoveryEmail';" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Could not create the offline recovery fixture." }
  [IO.File]::WriteAllText($recoveryPasswordPath, $recoveryPassword + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))

  $previousErrorActionPreference = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    $rejectedRecoveryOutput = @(& go run ./cmd/dbtool -action recover-administrator -administrator-email $recoveryEmail -password-file $recoveryPasswordPath 2>&1) -join "`n"
    $rejectedRecoveryExit = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $previousErrorActionPreference
  }
  if ($rejectedRecoveryExit -eq 0 -or $rejectedRecoveryOutput -notmatch 'confirm-account-recovery') {
    throw "Offline recovery without the confirmation flag was not rejected."
  }
  $rejectedRecoveryFact = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT status::text || '|' || count(*) FILTER (WHERE session.revoked_at IS NULL) FROM users owner LEFT JOIN sessions session ON session.user_id = owner.id WHERE owner.email = '$recoveryEmail' GROUP BY owner.status"
  if ($LASTEXITCODE -ne 0 -or $rejectedRecoveryFact -ne "disabled|1") {
    throw "Rejected offline recovery changed the administrator facts: $rejectedRecoveryFact"
  }

  $recoveryOutput = @(& go run ./cmd/dbtool -action recover-administrator -administrator-email $recoveryEmail -password-file $recoveryPasswordPath -confirm-account-recovery 2>&1) -join "`n"
  if ($LASTEXITCODE -ne 0 -or $recoveryOutput -notmatch 'revoked_sessions=1' -or $recoveryOutput.Contains($recoveryPassword)) {
    throw "Confirmed offline administrator recovery failed or exposed its password."
  }
  $recoveryPassword = ""
  Remove-Item -LiteralPath $recoveryPasswordPath -Force
  $recoveryFact = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT owner.status::text || '|' || count(*) FILTER (WHERE session.revoked_at IS NULL) || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'identity.administrator_recovered' AND audit.target_id = owner.id::text AND audit.actor_user_id IS NULL AND audit.request_id IS NOT NULL) FROM users owner LEFT JOIN sessions session ON session.user_id = owner.id WHERE owner.email = '$recoveryEmail' GROUP BY owner.id, owner.status"
  if ($LASTEXITCODE -ne 0 -or $recoveryFact -ne "active|0|1") {
    throw "Offline administrator recovery did not persist activation, session revocation, and system audit facts: $recoveryFact"
  }

  & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "backup-postgres.ps1") `
    -OutputPath $backupPath -Container $postgres.Container -DatabaseName $databaseName -DatabaseUser llmgateway -AllowIsolatedTestContainer
  if ($LASTEXITCODE -ne 0) { throw "PostgreSQL backup command failed." }
  & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "restore-postgres.ps1") `
    -InputPath $backupPath -Container $postgres.Container -TargetDatabase $restoredDatabaseName -DatabaseUser llmgateway -ConfirmRestore -AllowIsolatedTestContainer
  if ($LASTEXITCODE -ne 0) { throw "PostgreSQL restore command failed." }

  $restoredFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d $restoredDatabaseName -Atc `
    "SELECT (SELECT count(*) FROM provider_credentials WHERE name = 'Rotation Credential') || '|' || (SELECT count(*) FROM audit_events WHERE action = 'credential.encryption_rotated') || '|' || (SELECT max(version_id) FROM goose_db_version WHERE is_applied = true)"
  if ($LASTEXITCODE -ne 0 -or $restoredFacts -ne "1|1|1") {
    throw "Restored PostgreSQL facts are incomplete: $restoredFacts"
  }

  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $password
  $gatewayPort = Get-FreeLoopbackPort
  $binaryName = if ($runningOnWindows) { "llmgateway.exe" } else { "llmgateway" }
  $binaryPath = Join-Path $buildDirectory $binaryName
  $stdoutPath = Join-Path $buildDirectory "gateway.stdout.log"
  $stderrPath = Join-Path $buildDirectory "gateway.stderr.log"
  & $pnpmCommand --dir web run build
  if ($LASTEXITCODE -ne 0) { throw "Production frontend build failed." }
  go build -tags webembed -trimpath -o $binaryPath .\cmd\gateway
  if ($LASTEXITCODE -ne 0) { throw "$runtimeName production binary build failed." }

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
  $gatewayStartArguments = @{
    FilePath               = $binaryPath
    PassThru               = $true
    RedirectStandardOutput = $stdoutPath
    RedirectStandardError  = $stderrPath
  }
  if ($runningOnWindows) { $gatewayStartArguments.WindowStyle = "Hidden" }
  $gateway = Start-Process @gatewayStartArguments
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
    throw "Embedded production frontend was not served by the $runtimeName gateway."
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

Write-Host "Credential rotation, offline administrator recovery, PostgreSQL backup/restore, and the $runtimeName production runtime passed in an isolated environment."
