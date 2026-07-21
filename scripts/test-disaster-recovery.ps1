param(
  [string] $GatewayImage = "llmgateway:disaster-recovery",
  [switch] $SkipBuild
)

$ErrorActionPreference = "Stop"
. "$PSScriptRoot\isolated-services.ps1"

$root = Split-Path -Parent $PSScriptRoot
$runID = New-LLMGatewayTestRunID -Purpose "dr"
$project = "llmgateway-$runID"
$controllerName = "llmgateway-dr-controller-$runID"
$controllerImage = "llmgateway:dr-controller"
$dataVolume = "llmgateway-dr-data-$runID"
$linuxRoot = ""
$buildDirectory = Join-Path $root ".build\disaster-recovery-$runID"
$seedDirectory = Join-Path $buildDirectory "seed"
$evidenceDirectory = Join-Path $root ".build\acceptance-evidence"
$docker = Get-LLMGatewayDockerCommand
$environmentSnapshot = Save-LLMGatewayEnvironment
$controller = ""
$failure = $null
$backupCompletedAt = $null
$recoveryStartedAt = $null
$report = [ordered]@{
  topology = "linux-compose-empty-host-restic-recovery"
  encryptedSnapshot = $false
  sourceVolumesDestroyed = $false
  configurationRestored = $false
  postgresRestoredToNewDatabase = $false
  headedBrowserJourneys = 0
  confirmationGuards = $false
  wrongPasswordRejected = $false
  nonEmptyTargetRejected = $false
  repeatedDatabaseRestoreRejected = $false
  repositoryCorruptionDetected = $false
  secretsInLogs = $false
  recoveryPointAgeSeconds = 0
  recoveryTimeSeconds = 0
  rpoTargetSeconds = 21600
  rtoTargetSeconds = 7200
}

function New-RandomHex([int] $Bytes) {
  $buffer = [byte[]]::new($Bytes)
  $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
  try { $generator.GetBytes($buffer) } finally { $generator.Dispose() }
  return (($buffer | ForEach-Object { $_.ToString("x2") }) -join "")
}

function Write-SeedFile([string] $RelativePath, [string] $Value) {
  $path = Join-Path $seedDirectory $RelativePath
  [IO.Directory]::CreateDirectory((Split-Path -Parent $path)) | Out-Null
  [IO.File]::WriteAllText($path, $Value, [Text.UTF8Encoding]::new($false))
}

function Invoke-Controller([string] $Command, [switch] $AllowFailure, [switch] $Quiet) {
  $previousPreference = $ErrorActionPreference
  try {
    $ErrorActionPreference = "Continue"
    $output = @(& $docker exec $controller bash -lc $Command 2>&1)
    $exitCode = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $previousPreference
  }
  if (-not $Quiet) { $output | ForEach-Object { Write-Host $_ } }
  if ($exitCode -ne 0 -and -not $AllowFailure) {
    throw "Disaster-recovery controller failed with exit code $exitCode."
  }
  return [pscustomobject]@{ ExitCode = $exitCode; Output = $output }
}

function Assert-ControllerFailure([string] $Command, [string] $Message) {
  $result = Invoke-Controller -Command $Command -AllowFailure -Quiet
  if ($result.ExitCode -eq 0) { throw $Message }
}

function Wait-HTTPSReady([string] $URL) {
  $deadline = (Get-Date).AddSeconds(120)
  do {
    $previousPreference = $ErrorActionPreference
    try {
      $ErrorActionPreference = "Continue"
      & curl.exe --silent --fail --insecure --output NUL "$URL/health/ready" 2>$null
      $exitCode = $LASTEXITCODE
    } finally {
      $ErrorActionPreference = $previousPreference
    }
    if ($exitCode -eq 0) { return }
    Start-Sleep -Milliseconds 500
  } while ((Get-Date) -lt $deadline)
  throw "The recovered production TLS entry did not become ready."
}

function Invoke-HeadedJourney([string] $Mode, [string] $URL) {
  $env:LLMGATEWAY_DEPLOYMENT_MODE = $Mode
  $env:LLMGATEWAY_DEPLOYMENT_URL = $URL
  pnpm.cmd --dir web exec playwright test --config playwright.deployment.config.ts
  if ($LASTEXITCODE -ne 0) { throw "The $Mode disaster-recovery browser journey failed." }
  $report.headedBrowserJourneys++
}

Push-Location $root
try {
  Clear-LLMGatewayEnvironment
  [IO.Directory]::CreateDirectory($seedDirectory) | Out-Null
  [IO.Directory]::CreateDirectory($evidenceDirectory) | Out-Null

  $postgresPassword = New-RandomHex 24
  $valkeyPassword = New-RandomHex 24
  $masterKeyBytes = [byte[]]::new(32)
  $masterKeyGenerator = [Security.Cryptography.RandomNumberGenerator]::Create()
  try { $masterKeyGenerator.GetBytes($masterKeyBytes) } finally { $masterKeyGenerator.Dispose() }
  $masterKey = [Convert]::ToBase64String($masterKeyBytes)
  $sessionPepper = New-RandomHex 32
  $apiKeyPepper = New-RandomHex 32
  $coordinationSecret = New-RandomHex 32
  $resticPassword = New-RandomHex 32
  $marker = [guid]::NewGuid().ToString("N")
  $httpPort = Get-LLMGatewayFreeLoopbackPort
  $httpsPort = Get-LLMGatewayFreeLoopbackPort
  $deploymentURL = "https://localhost:$httpsPort"

  & $docker volume create --label "llmgateway.test.owner=llmgateway-isolated-tests" --label "llmgateway.test.run=$runID" $dataVolume | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Could not create the isolated Linux recovery volume." }
  $linuxRoot = ([string](& $docker volume inspect --format '{{.Mountpoint}}' $dataVolume)).Trim()
  if ($LASTEXITCODE -ne 0 -or $linuxRoot -notmatch '^/var/lib/docker/volumes/llmgateway-dr-data-[a-z0-9-]+/_data$') {
    throw "Docker returned an unsafe recovery-volume mountpoint."
  }
  $configurationRoot = "$linuxRoot/configuration"
  $restoredConfigurationRoot = "$linuxRoot/restore/backup/configuration"
  $runtimeEnvironment = @"
LLMGATEWAY_COMPOSE_PROJECT=$project
LLMGATEWAY_GATEWAY_IMAGE=$GatewayImage
LLMGATEWAY_POSTGRES_IMAGE=postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15
LLMGATEWAY_VALKEY_IMAGE=valkey/valkey@sha256:c9b77919daeba2c02ad954d0c844cc4e7142069d177b89c5fd771f405daf9e02
LLMGATEWAY_CADDY_IMAGE=caddy:2.10.2-alpine
LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION=1
LLMGATEWAY_SITE_ADDRESS=https://localhost
LLMGATEWAY_ACME_EMAIL=staging@example.invalid
LLMGATEWAY_HTTP_PORT=$httpPort
LLMGATEWAY_HTTPS_PORT=$httpsPort
LLMGATEWAY_POSTGRES_DB=llmgateway
LLMGATEWAY_POSTGRES_USER=llmgateway
LLMGATEWAY_POSTGRES_PASSWORD_FILE=$configurationRoot/postgres-password
LLMGATEWAY_DATABASE_URL_FILE=$configurationRoot/database-url
LLMGATEWAY_VALKEY_PASSWORD_FILE=$configurationRoot/valkey-password
LLMGATEWAY_VALKEY_ACL_FILE=$configurationRoot/valkey-acl
LLMGATEWAY_MASTER_KEYS_FILE=$configurationRoot/master-keys
LLMGATEWAY_SESSION_PEPPER_FILE=$configurationRoot/session-pepper
LLMGATEWAY_API_KEY_PEPPER_FILE=$configurationRoot/api-key-pepper
LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE=$configurationRoot/coordination-secret
"@
  $backupEnvironment = @"
LLMGATEWAY_RESTIC_IMAGE=restic/restic@sha256:136600b6ff6843d61d355f7f71f460a166429f35de6fd11b568fece3c9a4d510
LLMGATEWAY_RESTIC_REPOSITORY_FILE=$linuxRoot/backup-control/repository
LLMGATEWAY_RESTIC_PASSWORD_FILE=$linuxRoot/backup-control/password
LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY=$linuxRoot/restic-repository
LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE=$configurationRoot/production.env
LLMGATEWAY_CONFIGURATION_DIRECTORY=$configurationRoot
LLMGATEWAY_BACKUP_STAGING_ROOT=$linuxRoot/staging
LLMGATEWAY_RESTIC_CHECK_SUBSET=100%
LLMGATEWAY_RUNTIME_SECRET_GID=65532
"@
  Write-SeedFile "configuration\postgres-password" $postgresPassword
  Write-SeedFile "configuration\database-url" "postgres://llmgateway:$postgresPassword@postgres:5432/llmgateway?sslmode=disable"
  Write-SeedFile "configuration\valkey-password" $valkeyPassword
  Write-SeedFile "configuration\valkey-acl" "user default on >$valkeyPassword ~* &* +@all"
  Write-SeedFile "configuration\master-keys" "1:$masterKey"
  Write-SeedFile "configuration\session-pepper" $sessionPepper
  Write-SeedFile "configuration\api-key-pepper" $apiKeyPepper
  Write-SeedFile "configuration\coordination-secret" $coordinationSecret
  Write-SeedFile "configuration\recovery-marker" $marker
  Write-SeedFile "configuration\production.env" $runtimeEnvironment
  Write-SeedFile "backup-control\repository" "local:/repository"
  Write-SeedFile "backup-control\password" $resticPassword
  Write-SeedFile "backup-control\expected-marker" $marker
  Write-SeedFile "backup-control\backup.env" $backupEnvironment

  if (-not $SkipBuild) {
    & $docker build --build-arg RELEASE_VERSION=disaster-recovery --tag $GatewayImage .
    if ($LASTEXITCODE -ne 0) { throw "The disaster-recovery Gateway image build failed." }
  }
  & $docker build --tag $controllerImage --file scripts/acceptance/Dockerfile.disaster-recovery-controller .
  if ($LASTEXITCODE -ne 0) { throw "The fixed disaster-recovery controller image build failed." }

  $controllerOutput = & $docker run --detach --rm --name $controllerName `
    --label "llmgateway.test.owner=llmgateway-isolated-tests" --label "llmgateway.test.run=$runID" `
    --mount "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock" `
    --mount "type=volume,source=$dataVolume,target=$linuxRoot" `
    --mount "type=bind,source=$root,target=/workspace,readonly" `
    $controllerImage "sleep 7200"
  if ($LASTEXITCODE -ne 0) { throw "Could not start the disaster-recovery controller." }
  $controller = ([string]($controllerOutput | Select-Object -First 1)).Trim()
  & $docker cp "$seedDirectory\." "$controller`:$linuxRoot"
  if ($LASTEXITCODE -ne 0) { throw "Could not seed the isolated Linux recovery root." }
  Invoke-Controller "mkdir -p '$linuxRoot/deployment'; cp /workspace/deploy/compose.production.yaml /workspace/deploy/compose.acceptance.yaml /workspace/deploy/Caddyfile /workspace/deploy/Caddyfile.internal '$linuxRoot/deployment/'; chmod 0700 '$linuxRoot' '$linuxRoot/backup-control'; chgrp 65532 '$configurationRoot' '$configurationRoot'/*; chmod 0750 '$configurationRoot'; chmod 0640 '$configurationRoot'/*; chmod 0600 '$linuxRoot/backup-control'/*; mkdir -p '$linuxRoot/restic-repository' '$linuxRoot/staging'" | Out-Null

  $sourceCompose = "source /workspace/deploy/lib.sh; load_llmgateway_environment '$configurationRoot/production.env'; export DEPLOY_DIRECTORY='$linuxRoot/deployment'; deployment_compose --file '$linuxRoot/deployment/compose.acceptance.yaml'"
  $sourceStorage = Invoke-Controller "$sourceCompose up --detach --wait postgres valkey" -AllowFailure
  if ($sourceStorage.ExitCode -ne 0) {
    Invoke-Controller "$sourceCompose logs --no-color postgres valkey" -AllowFailure | Out-Null
    throw "The source PostgreSQL/Valkey topology did not become healthy."
  }
  Invoke-Controller "$sourceCompose --profile migration run --rm migrate" | Out-Null
  Invoke-Controller "$sourceCompose up --detach --wait gateway-a gateway-b caddy" | Out-Null
  Wait-HTTPSReady $deploymentURL
  Invoke-HeadedJourney "setup" $deploymentURL

  Assert-ControllerFailure "bash /workspace/deploy/initialize-backup-linux.sh '$linuxRoot/backup-control/backup.env'" "Backup initialization succeeded without confirmation."
  Invoke-Controller "bash /workspace/deploy/initialize-backup-linux.sh '$linuxRoot/backup-control/backup.env' --confirm-backup-repository-initialization" | Out-Null
  Invoke-Controller "export DEPLOY_DIRECTORY='$linuxRoot/deployment'; bash /workspace/deploy/backup-linux.sh '$linuxRoot/backup-control/backup.env'" | Out-Null
  $backupCompletedAt = Get-Date
  Invoke-Controller "rmdir '$linuxRoot/staging'; mkdir '$linuxRoot/staging'" | Out-Null
  $report.encryptedSnapshot = $true

  $sourceLogs = (Invoke-Controller "$sourceCompose logs --no-color" -Quiet).Output -join "`n"
  foreach ($secret in @($postgresPassword, $valkeyPassword, $masterKey, $sessionPepper, $apiKeyPepper, $coordinationSecret, $resticPassword)) {
    if ($sourceLogs.Contains($secret)) { $report.secretsInLogs = $true }
  }
  $recoveryStartedAt = Get-Date
  Invoke-Controller "$sourceCompose down --volumes --remove-orphans --timeout 30" | Out-Null
  Invoke-Controller "rm -rf '$configurationRoot' '$linuxRoot/staging'; mkdir -p '$linuxRoot/staging'" | Out-Null
  $report.sourceVolumesDestroyed = $true

  Assert-ControllerFailure "bash /workspace/deploy/restore-backup-linux.sh '$linuxRoot/backup-control/backup.env' '$linuxRoot/restore'" "Snapshot restore succeeded without confirmation."
  Invoke-Controller "mkdir -p '$linuxRoot/non-empty'; printf occupied >'$linuxRoot/non-empty/marker'" | Out-Null
  Assert-ControllerFailure "bash /workspace/deploy/restore-backup-linux.sh '$linuxRoot/backup-control/backup.env' '$linuxRoot/non-empty' --confirm-disaster-restore" "Snapshot restore accepted a non-empty target."
  $report.nonEmptyTargetRejected = $true
  Invoke-Controller "cp '$linuxRoot/backup-control/backup.env' '$linuxRoot/backup-control/wrong.env'; cp '$linuxRoot/backup-control/password' '$linuxRoot/backup-control/wrong-password'; printf wrong >>'$linuxRoot/backup-control/wrong-password'; sed -i 's|/password$|/wrong-password|' '$linuxRoot/backup-control/wrong.env'; chmod 0600 '$linuxRoot/backup-control/wrong.env' '$linuxRoot/backup-control/wrong-password'" | Out-Null
  Assert-ControllerFailure "bash /workspace/deploy/restore-backup-linux.sh '$linuxRoot/backup-control/wrong.env' '$linuxRoot/wrong-restore' --confirm-disaster-restore" "Snapshot restore accepted a wrong Restic password."
  $report.wrongPasswordRejected = $true
  Invoke-Controller "bash /workspace/deploy/restore-backup-linux.sh '$linuxRoot/backup-control/backup.env' '$linuxRoot/restore' --confirm-disaster-restore" | Out-Null
  Invoke-Controller "cmp '$restoredConfigurationRoot/recovery-marker' '$linuxRoot/backup-control/expected-marker'" | Out-Null
  $report.configurationRestored = $true
  $report.confirmationGuards = $true

  Invoke-Controller "sed -e 's|$configurationRoot|$restoredConfigurationRoot|g' -e 's|/llmgateway?sslmode|/llmgateway_restored?sslmode|' '$restoredConfigurationRoot/production.env' >'$linuxRoot/recovered-runtime.env'; sed -e 's|$GatewayImage|registry.example.invalid/llmgateway@sha256:$('0' * 64)|' -e 's|caddy:2.10.2-alpine|caddy@sha256:$('1' * 64)|' '$linuxRoot/recovered-runtime.env' >'$linuxRoot/recovered-immutable.env'; chmod 0600 '$linuxRoot/recovered-runtime.env' '$linuxRoot/recovered-immutable.env'" | Out-Null
  $recoveredCompose = "source /workspace/deploy/lib.sh; load_llmgateway_environment '$linuxRoot/recovered-runtime.env'; export DEPLOY_DIRECTORY='$linuxRoot/deployment'; deployment_compose --file '$linuxRoot/deployment/compose.acceptance.yaml'"
  Invoke-Controller "$recoveredCompose up --detach --wait postgres valkey" | Out-Null
  Assert-ControllerFailure "export DEPLOY_DIRECTORY='$linuxRoot/deployment'; bash /workspace/deploy/restore-postgres-linux.sh '$linuxRoot/recovered-immutable.env' '$linuxRoot/restore/backup/postgres.dump' llmgateway_restored" "Database restore succeeded without confirmation."
  Invoke-Controller "export DEPLOY_DIRECTORY='$linuxRoot/deployment'; bash /workspace/deploy/restore-postgres-linux.sh '$linuxRoot/recovered-immutable.env' '$linuxRoot/restore/backup/postgres.dump' llmgateway_restored --confirm-new-database-restore" | Out-Null
  Assert-ControllerFailure "export DEPLOY_DIRECTORY='$linuxRoot/deployment'; bash /workspace/deploy/restore-postgres-linux.sh '$linuxRoot/recovered-immutable.env' '$linuxRoot/restore/backup/postgres.dump' llmgateway_restored --confirm-new-database-restore" "Repeated database restore overwrote the target."
  $report.repeatedDatabaseRestoreRejected = $true
  $report.postgresRestoredToNewDatabase = $true
  Invoke-Controller "sed -i 's|/llmgateway?sslmode|/llmgateway_restored?sslmode|' '$restoredConfigurationRoot/database-url'" | Out-Null
  Invoke-Controller "$recoveredCompose --profile migration run --rm migrate" | Out-Null
  Invoke-Controller "$recoveredCompose up --detach --wait gateway-a gateway-b caddy" | Out-Null
  Wait-HTTPSReady $deploymentURL
  Invoke-HeadedJourney "restored" $deploymentURL
  $report.recoveryTimeSeconds = [int]((Get-Date) - $recoveryStartedAt).TotalSeconds
  $report.recoveryPointAgeSeconds = [int]($recoveryStartedAt - $backupCompletedAt).TotalSeconds

  $recoveredLogs = (Invoke-Controller "$recoveredCompose logs --no-color" -Quiet).Output -join "`n"
  foreach ($secret in @($postgresPassword, $valkeyPassword, $masterKey, $sessionPepper, $apiKeyPepper, $coordinationSecret, $resticPassword)) {
    if ($recoveredLogs.Contains($secret)) { $report.secretsInLogs = $true }
  }
  if ($report.secretsInLogs) { throw "A disaster-recovery test secret entered runtime logs." }
  if ($report.recoveryPointAgeSeconds -gt $report.rpoTargetSeconds -or $report.recoveryTimeSeconds -gt $report.rtoTargetSeconds) {
    throw "The disaster-recovery exercise exceeded its RPO or RTO target."
  }

  Invoke-Controller "source /workspace/deploy/backup-lib.sh; load_backup_environment '$linuxRoot/backup-control/backup.env'; run_restic check --read-data" | Out-Null
  Invoke-Controller "find '$linuxRoot/restic-repository/data' -type f -exec dd if=/dev/zero of={} bs=1 count=1 conv=notrunc \;" | Out-Null
  Assert-ControllerFailure "source /workspace/deploy/backup-lib.sh; load_backup_environment '$linuxRoot/backup-control/backup.env'; run_restic check --read-data" "Restic check accepted a corrupted pack."
  $report.repositoryCorruptionDetected = $true

  $evidencePath = Join-Path $evidenceDirectory "disaster-recovery-report.json"
  [IO.File]::WriteAllText($evidencePath, ($report | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
} catch {
  $failure = $_
} finally {
  $cleanupFailures = @()
  if ($controller) {
    try {
      $inspection = @(& $docker inspect $controller | ConvertFrom-Json)
      if ($inspection.Count -ne 1 -or $inspection[0].Config.Labels.'llmgateway.test.run' -ne $runID) { throw "Controller ownership does not match." }
      Invoke-Controller "source /workspace/deploy/lib.sh; if [[ -f '$linuxRoot/recovered-runtime.env' ]]; then load_llmgateway_environment '$linuxRoot/recovered-runtime.env'; elif [[ -f '$configurationRoot/production.env' ]]; then load_llmgateway_environment '$configurationRoot/production.env'; else export LLMGATEWAY_COMPOSE_PROJECT='$project'; fi; export DEPLOY_DIRECTORY='$linuxRoot/deployment'; deployment_compose --file '$linuxRoot/deployment/compose.acceptance.yaml' down --volumes --remove-orphans --timeout 30" -AllowFailure -Quiet | Out-Null
      & $docker rm --force $controller *> $null
      if ($LASTEXITCODE -ne 0) { throw "Could not remove the recovery controller." }
    } catch { $cleanupFailures += $_.Exception.Message }
  }
  try {
    $volumeFacts = @(& $docker volume inspect $dataVolume 2>$null | ConvertFrom-Json)
    if ($volumeFacts.Count -eq 1) {
      if ($volumeFacts[0].Labels.'llmgateway.test.run' -ne $runID) { throw "Recovery-volume ownership does not match." }
      & $docker volume rm $dataVolume *> $null
      if ($LASTEXITCODE -ne 0) { throw "Could not remove the isolated Linux recovery volume." }
    }
  } catch { $cleanupFailures += $_.Exception.Message }
  try {
    $resolved = [IO.Path]::GetFullPath($buildDirectory)
    $buildRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
    if (-not $resolved.StartsWith($buildRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw "Unsafe local recovery path." }
    if ([IO.Directory]::Exists($resolved)) { [IO.Directory]::Delete($resolved, $true) }
  } catch { $cleanupFailures += $_.Exception.Message }
  try { Restore-LLMGatewayEnvironment $environmentSnapshot; Pop-Location } catch { $cleanupFailures += $_.Exception.Message }
  if ($failure) {
    if ($cleanupFailures.Count) { throw "Disaster recovery failed: $($failure.Exception.Message) Cleanup also failed: $($cleanupFailures -join '; ')" }
    throw $failure
  }
  if ($cleanupFailures.Count) { throw "Disaster-recovery cleanup failed: $($cleanupFailures -join '; ')" }
}

Write-Host "Encrypted backup, source-volume loss, empty-environment restore, corruption detection, and headed administrator/member recovery passed."
