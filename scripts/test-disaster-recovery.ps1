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
$activeLock = $null
$recoveryStartedAt = $null
$resticOwners = [Collections.Generic.List[string]]::new()
$report = [ordered]@{
  topology = "linux-compose-empty-host-explicit-restic-recovery"
  encryptedSnapshot = $false
  explicitSnapshotSelection = $false
  manifestValidated = $false
  freshnessMarkerValidated = $false
  backupControlGuards = $false
  maintenanceLockConflicts = 0
  resticContainerCleanup = $false
  sourceVolumesDestroyed = $false
  staleRestoreRecovered = $false
  configurationRestored = $false
  postgresRestoredToNewDatabase = $false
  incompleteDatabaseReplacement = $false
  nonPostgresUnsafeStateRejected = $false
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

if ($GatewayImage -notmatch '^[a-z0-9][a-z0-9./:_-]{1,127}$') {
  throw "GatewayImage must be a lowercase local Docker image reference."
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
    $diagnostic = (@($output | Select-Object -Last 8) -join " | ")
    throw "Disaster-recovery controller failed with exit code $exitCode. Command: $Command Diagnostic: $diagnostic"
  }
  return [pscustomobject]@{ ExitCode = $exitCode; Output = $output }
}

function Assert-ControllerFailure([string] $Command, [string] $Message) {
  $result = Invoke-Controller -Command $Command -AllowFailure -Quiet
  if ($result.ExitCode -eq 0) { throw $Message }
}

function Wait-ControllerCondition(
  [string] $Command,
  [string] $FailureMessage,
  [int] $TimeoutSeconds = 20
) {
  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    $probe = Invoke-Controller -Command $Command -AllowFailure -Quiet
    if ($probe.ExitCode -eq 0) { return }
    Start-Sleep -Milliseconds 100
  } while ((Get-Date) -lt $deadline)
  throw $FailureMessage
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
    Start-Sleep -Milliseconds 100
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

function New-ResticOwner([string] $Operation) {
  $owner = "$runID-$Operation".ToLowerInvariant()
  if ($owner -notmatch '^[a-z0-9][a-z0-9.-]{2,63}$') {
    throw "The isolated Restic owner is invalid."
  }
  $resticOwners.Add($owner)
  return $owner
}

function Use-ResticOwner([string] $Owner, [string] $Command) {
  return "export LLMGATEWAY_RESTIC_RUN_OWNER='$Owner'; $Command"
}

function Start-MaintenanceLock([string] $LinuxPath) {
  $readyFile = "$LinuxPath/maintenance-lock.ready"
  $pidFile = "$LinuxPath/maintenance-lock.pid"
  $command = "rm -f '$readyFile' '$pidFile'; flock --nonblock --close /run/lock/llmgateway-maintenance.lock bash -c 'umask 077; printf ready > $readyFile; exec sleep 90' >/dev/null 2>&1 & printf '%s\n' `$! > '$pidFile'"
  Invoke-Controller $command -Quiet | Out-Null
  Wait-ControllerCondition "test -s '$readyFile' -a -s '$pidFile'" "The maintenance lock holder did not start."
  Assert-ControllerFailure "flock --nonblock /run/lock/llmgateway-maintenance.lock true" "The maintenance lock was not observably held."
  return [pscustomobject]@{ ReadyFile = $readyFile; PIDFile = $pidFile }
}

function Stop-MaintenanceLock($Lock) {
  if ($null -eq $Lock) { return }
  Invoke-Controller "xargs -r kill -- < '$($Lock.PIDFile)' || true" -Quiet | Out-Null
  Wait-ControllerCondition "flock --nonblock /run/lock/llmgateway-maintenance.lock true" "The maintenance lock was not released."
  Invoke-Controller "rm -f '$($Lock.ReadyFile)' '$($Lock.PIDFile)'" -Quiet | Out-Null
}

function Contains-Secret([string] $Text, [string[]] $Secrets) {
  foreach ($secret in $Secrets) {
    if ($Text.Contains($secret)) { return $true }
  }
  return $false
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
  $wrongResticPassword = New-RandomHex 32
  $secrets = @(
    $postgresPassword, $valkeyPassword, $masterKey, $sessionPepper,
    $apiKeyPepper, $coordinationSecret, $resticPassword, $wrongResticPassword
  )
  $httpPort = Get-LLMGatewayFreeLoopbackPort
  $httpsPort = Get-LLMGatewayFreeLoopbackPort
  $deploymentURL = "https://localhost:$httpsPort"
  $storedGatewayImage = "registry.example.invalid/llmgateway@sha256:$('0' * 64)"
  $storedCaddyImage = "registry.example.invalid/caddy@sha256:$('1' * 64)"
  $upgradeCandidateImage = "registry.example.invalid/llmgateway@sha256:$('2' * 64)"

  & $docker volume create --label "llmgateway.test.owner=llmgateway-isolated-tests" --label "llmgateway.test.run=$runID" $dataVolume | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Could not create the isolated Linux recovery volume." }
  $linuxRoot = ([string](& $docker volume inspect --format '{{.Mountpoint}}' $dataVolume)).Trim()
  if ($LASTEXITCODE -ne 0 -or $linuxRoot -notmatch '^/var/lib/docker/volumes/llmgateway-dr-data-[a-z0-9-]+/_data$') {
    throw "Docker returned an unsafe recovery-volume mountpoint."
  }

  $configurationRoot = "$linuxRoot/configuration"
  $secretRoot = "$configurationRoot/secrets"
  $backupControlRoot = "$linuxRoot/backup-control"
  $stagingRoot = "$linuxRoot/staging"
  $repositoryRoot = "$linuxRoot/restic-repository"
  $restoreControlRoot = "$linuxRoot/restore-control"
  $restoreRoot = "$linuxRoot/restore"
  $restoredPayloadRoot = "$restoreRoot/backup"
  $restoredConfigurationRoot = "$restoredPayloadRoot/configuration"
  $recoveredConfigurationRoot = "$linuxRoot/recovered-configuration"
  $runtimeEnvironment = @"
LLMGATEWAY_COMPOSE_PROJECT=$project
LLMGATEWAY_GATEWAY_IMAGE=$storedGatewayImage
LLMGATEWAY_POSTGRES_IMAGE=postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15
LLMGATEWAY_VALKEY_IMAGE=valkey/valkey@sha256:c9b77919daeba2c02ad954d0c844cc4e7142069d177b89c5fd771f405daf9e02
LLMGATEWAY_CADDY_IMAGE=$storedCaddyImage
LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION=1
LLMGATEWAY_SITE_ADDRESS=https://localhost
LLMGATEWAY_ACME_EMAIL=staging@example.invalid
LLMGATEWAY_HTTP_PORT=$httpPort
LLMGATEWAY_HTTPS_PORT=$httpsPort
LLMGATEWAY_POSTGRES_DB=llmgateway
LLMGATEWAY_POSTGRES_USER=llmgateway
LLMGATEWAY_POSTGRES_PASSWORD_FILE=$secretRoot/postgres-password
LLMGATEWAY_DATABASE_URL_FILE=$secretRoot/database-url
LLMGATEWAY_VALKEY_PASSWORD_FILE=$secretRoot/valkey-password
LLMGATEWAY_VALKEY_ACL_FILE=$secretRoot/valkey-acl
LLMGATEWAY_MASTER_KEYS_FILE=$secretRoot/master-keys
LLMGATEWAY_SESSION_PEPPER_FILE=$secretRoot/session-pepper
LLMGATEWAY_API_KEY_PEPPER_FILE=$secretRoot/api-key-pepper
LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE=$secretRoot/coordination-secret
"@
  $backupEnvironment = @"
LLMGATEWAY_BACKUP_MODE=acceptance
LLMGATEWAY_RESTIC_IMAGE=restic/restic@sha256:136600b6ff6843d61d355f7f71f460a166429f35de6fd11b568fece3c9a4d510
LLMGATEWAY_RESTIC_REPOSITORY_FILE=$backupControlRoot/repository
LLMGATEWAY_RESTIC_PASSWORD_FILE=$backupControlRoot/password
LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY=$repositoryRoot
LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE=$configurationRoot/deployment.env
LLMGATEWAY_CONFIGURATION_DIRECTORY=$configurationRoot
LLMGATEWAY_BACKUP_STAGING_ROOT=$stagingRoot
LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE=$stagingRoot/last-success
LLMGATEWAY_RESTIC_CHECK_SUBSET=100%
"@
  $wrongBackupEnvironment = $backupEnvironment.Replace(
    "LLMGATEWAY_RESTIC_PASSWORD_FILE=$backupControlRoot/password",
    "LLMGATEWAY_RESTIC_PASSWORD_FILE=$backupControlRoot/wrong-password"
  )
  $nonRootRepositoryEnvironment = $backupEnvironment.Replace(
    "LLMGATEWAY_RESTIC_REPOSITORY_FILE=$backupControlRoot/repository",
    "LLMGATEWAY_RESTIC_REPOSITORY_FILE=$backupControlRoot/nonroot-repository"
  )
  $productionLocalEnvironment = $backupEnvironment.Replace(
    "LLMGATEWAY_BACKUP_MODE=acceptance",
    "LLMGATEWAY_BACKUP_MODE=production"
  )

  Write-SeedFile "configuration\deployment.env" $runtimeEnvironment
  Write-SeedFile "configuration\secrets\postgres-password" $postgresPassword
  Write-SeedFile "configuration\secrets\database-url" "postgres://llmgateway:$postgresPassword@postgres:5432/llmgateway?sslmode=disable"
  Write-SeedFile "configuration\secrets\valkey-password" $valkeyPassword
  Write-SeedFile "configuration\secrets\valkey-acl" "user default on >$valkeyPassword ~* &* +@all"
  Write-SeedFile "configuration\secrets\master-keys" "1:$masterKey"
  Write-SeedFile "configuration\secrets\session-pepper" $sessionPepper
  Write-SeedFile "configuration\secrets\api-key-pepper" $apiKeyPepper
  Write-SeedFile "configuration\secrets\coordination-secret" $coordinationSecret
  Write-SeedFile "backup-control\repository" "local:/repository"
  Write-SeedFile "backup-control\nonroot-repository" "local:/repository"
  Write-SeedFile "backup-control\password" $resticPassword
  Write-SeedFile "backup-control\wrong-password" $wrongResticPassword
  Write-SeedFile "backup-control\backup.env" $backupEnvironment
  Write-SeedFile "backup-control\wrong.env" $wrongBackupEnvironment
  Write-SeedFile "backup-control\nonroot.env" $nonRootRepositoryEnvironment
  Write-SeedFile "backup-control\production-local.env" $productionLocalEnvironment
  Write-SeedFile "restore-control\new-database-url" "postgres://llmgateway:$postgresPassword@postgres:5432/llmgateway_restored?sslmode=disable"
  Write-SeedFile "restore-control\wrong-database-url" "postgres://llmgateway:$postgresPassword@postgres:5432/wrong_database?sslmode=disable"

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

  $prepareCommand = @"
install -d -o 0 -g 0 -m 0700 '$linuxRoot' '$backupControlRoot' '$stagingRoot' '$repositoryRoot' '$restoreControlRoot'
install -d -o 0 -g 0 -m 0750 '$linuxRoot/deployment' '$configurationRoot' '$secretRoot'
cp /workspace/deploy/compose.production.yaml /workspace/deploy/compose.acceptance.yaml /workspace/deploy/Caddyfile /workspace/deploy/Caddyfile.internal '$linuxRoot/deployment/'
chown 0:0 '$configurationRoot/deployment.env' '$secretRoot/postgres-password'
chmod 0640 '$configurationRoot/deployment.env'
chmod 0400 '$secretRoot/postgres-password'
chown 65532:65532 '$secretRoot/database-url' '$secretRoot/valkey-password' '$secretRoot/master-keys' '$secretRoot/session-pepper' '$secretRoot/api-key-pepper' '$secretRoot/coordination-secret'
chmod 0400 '$secretRoot/database-url' '$secretRoot/valkey-password' '$secretRoot/master-keys' '$secretRoot/session-pepper' '$secretRoot/api-key-pepper' '$secretRoot/coordination-secret'
chown 999:1000 '$secretRoot/valkey-acl'
chmod 0400 '$secretRoot/valkey-acl'
chown 0:0 '$backupControlRoot'/* '$restoreControlRoot'/*
chmod 0600 '$backupControlRoot'/* '$restoreControlRoot'/*
"@
  Invoke-Controller $prepareCommand -Quiet | Out-Null
  Invoke-Controller "source /workspace/deploy/backup-lib.sh; verify_runtime_configuration_tree '$configurationRoot'" -Quiet | Out-Null

  $sourceCompose = "source /workspace/deploy/lib.sh; load_llmgateway_environment '$configurationRoot/deployment.env'; export LLMGATEWAY_GATEWAY_IMAGE='$GatewayImage' LLMGATEWAY_CADDY_IMAGE='caddy:2.10.2-alpine' DEPLOY_DIRECTORY='$linuxRoot/deployment'; deployment_compose --file '$linuxRoot/deployment/compose.acceptance.yaml'"
  $sourceStorage = Invoke-Controller "$sourceCompose up --detach --wait postgres valkey" -AllowFailure
  if ($sourceStorage.ExitCode -ne 0) {
    Invoke-Controller "$sourceCompose logs --no-color postgres valkey" -AllowFailure | Out-Null
    throw "The source PostgreSQL/Valkey topology did not become healthy."
  }
  Invoke-Controller "$sourceCompose --profile migration run --rm migrate" -Quiet | Out-Null
  Invoke-Controller "$sourceCompose up --detach --wait gateway-a gateway-b caddy" -Quiet | Out-Null
  Wait-HTTPSReady $deploymentURL
  Invoke-HeadedJourney "setup" $deploymentURL

  $backupCommand = "export DEPLOY_DIRECTORY='$linuxRoot/deployment'; bash /workspace/deploy/backup-linux.sh '$backupControlRoot/backup.env'"
  Assert-ControllerFailure "bash /workspace/deploy/initialize-backup-linux.sh '$backupControlRoot/backup.env'" "Backup initialization succeeded without confirmation."
  Assert-ControllerFailure "bash /workspace/deploy/list-backups-linux.sh '$backupControlRoot/production-local.env'" "Production backup policy accepted a local repository."
  Invoke-Controller "ln -s '$backupControlRoot/backup.env' '$backupControlRoot/symlink.env'" -Quiet | Out-Null
  Assert-ControllerFailure "bash /workspace/deploy/list-backups-linux.sh '$backupControlRoot/symlink.env'" "Backup controls accepted a symbolic-link environment."
  Invoke-Controller "rm '$backupControlRoot/symlink.env'; chown 1000:1000 '$backupControlRoot/nonroot-repository'" -Quiet | Out-Null
  Assert-ControllerFailure "bash /workspace/deploy/list-backups-linux.sh '$backupControlRoot/nonroot.env'" "Backup controls accepted a non-root repository file."
  Invoke-Controller "install -o 0 -g 0 -m 0600 '$backupControlRoot/backup.env' '$configurationRoot/overlap.env'" -Quiet | Out-Null
  Assert-ControllerFailure "bash /workspace/deploy/list-backups-linux.sh '$configurationRoot/overlap.env'" "Backup controls accepted a file inside the runtime configuration tree."
  Invoke-Controller "rm '$configurationRoot/overlap.env'; install -o 0 -g 0 -m 0600 '$configurationRoot/deployment.env' '$backupControlRoot/deployment.env.saved'; printf '\nLLMGATEWAY_POSTGRES_DB=llmgateway\n' >> '$configurationRoot/deployment.env'" -Quiet | Out-Null
  Assert-ControllerFailure $backupCommand "Backup accepted a duplicate deployment environment key."
  Invoke-Controller "install -o 0 -g 0 -m 0640 '$backupControlRoot/deployment.env.saved' '$configurationRoot/deployment.env'" -Quiet | Out-Null
  $stagingResidueCheck = '[[ -z $(find ''' + $stagingRoot + ''' -mindepth 1 -maxdepth 1 -name ''backup.*'' -print -quit) ]]'
  Invoke-Controller $stagingResidueCheck -Quiet | Out-Null
  $report.backupControlGuards = $true

  $initializeOwner = New-ResticOwner "initialize"
  Invoke-Controller (Use-ResticOwner $initializeOwner "bash /workspace/deploy/initialize-backup-linux.sh '$backupControlRoot/backup.env' --confirm-backup-repository-initialization") -Quiet | Out-Null
  $backupOwner = New-ResticOwner "backup"
  Invoke-Controller (Use-ResticOwner $backupOwner $backupCommand) -Quiet | Out-Null
  $report.encryptedSnapshot = $true

  $freshnessOutput = Invoke-Controller "bash /workspace/deploy/check-backup-freshness-linux.sh '$backupControlRoot/backup.env'" -Quiet
  if (($freshnessOutput.Output -join "`n") -notmatch 'recovery point age: [0-9]+s') {
    throw "The independent backup freshness check did not report a valid recovery-point age."
  }
  $markerLines = @(Invoke-Controller "cat '$stagingRoot/last-success'" -Quiet).Output
  if ($markerLines.Count -ne 3 -or $markerLines[0] -ne 'format=llmgateway-backup-success' -or $markerLines[1] -notmatch '^recovery_point_utc=(.+)$') {
    throw "The backup success marker is invalid."
  }
  $markerRecoveryPoint = $Matches[1]
  $report.freshnessMarkerValidated = $true

  $listOwner = New-ResticOwner "list"
  $snapshotOutput = Invoke-Controller (Use-ResticOwner $listOwner "bash /workspace/deploy/list-backups-linux.sh '$backupControlRoot/backup.env'") -Quiet
  $snapshotDocuments = @(($snapshotOutput.Output -join "`n") | ConvertFrom-Json)
  if ($snapshotDocuments.Count -ne 1 -or [string]$snapshotDocuments[0].id -notmatch '^[a-f0-9]{64}$') {
    throw "The backup list did not return one explicit full snapshot ID."
  }
  $snapshotID = [string]$snapshotDocuments[0].id
  $report.explicitSnapshotSelection = $true
  Invoke-Controller '[[ -z $(docker ps --all --quiet --filter label=com.llmgateway.restic.owner) ]]' -Quiet | Out-Null
  Invoke-Controller '[[ -z $(find /run/llmgateway-restic -mindepth 1 -maxdepth 1 -print -quit) ]]' -Quiet | Out-Null
  $report.resticContainerCleanup = $true

  Invoke-Controller "install -d -o 0 -g 0 -m 0750 /etc/llmgateway; install -o 0 -g 0 -m 0640 '$configurationRoot/deployment.env' /etc/llmgateway/deployment.env" -Quiet | Out-Null
  $activeLock = Start-MaintenanceLock $linuxRoot
  Assert-ControllerFailure $backupCommand "Backup ignored the shared maintenance lock."
  $report.maintenanceLockConflicts++
  Assert-ControllerFailure "export DEPLOY_DIRECTORY='$linuxRoot/deployment'; bash /workspace/deploy/rotate-credentials-linux.sh '$configurationRoot/deployment.env' --confirm-key-rotation" "Credential rotation ignored the shared maintenance lock."
  $report.maintenanceLockConflicts++
  Assert-ControllerFailure "bash /workspace/deploy/upgrade-linux.sh '$upgradeCandidateImage' '$linuxRoot/upgrade-backups/upgrade.dump'" "Upgrade ignored the shared maintenance lock."
  $report.maintenanceLockConflicts++
  $lockedRestoreOwner = New-ResticOwner "locked-restore"
  Assert-ControllerFailure (Use-ResticOwner $lockedRestoreOwner "bash /workspace/deploy/restore-backup-linux.sh '$backupControlRoot/backup.env' '$snapshotID' '$linuxRoot/locked-restore' --confirm-disaster-restore") "Snapshot restore ignored the shared maintenance lock."
  $report.maintenanceLockConflicts++
  Stop-MaintenanceLock $activeLock
  $activeLock = $null

  $sourceLogs = (Invoke-Controller "$sourceCompose logs --no-color" -Quiet).Output -join "`n"
  if (Contains-Secret $sourceLogs $secrets) { $report.secretsInLogs = $true }

  $recoveryStartedAt = Get-Date
  Invoke-Controller "$sourceCompose down --volumes --remove-orphans --timeout 30" -Quiet | Out-Null
  Invoke-Controller "rm -rf -- '$configurationRoot' '$stagingRoot' /etc/llmgateway; install -d -o 0 -g 0 -m 0700 '$stagingRoot'" -Quiet | Out-Null
  $volumeCheck = '[[ -z $(docker volume ls --quiet --filter label=com.docker.compose.project=' + $project + ') ]]'
  Invoke-Controller $volumeCheck -Quiet | Out-Null
  $report.sourceVolumesDestroyed = $true

  Assert-ControllerFailure "bash /workspace/deploy/restore-backup-linux.sh '$backupControlRoot/backup.env' '$snapshotID' '$restoreRoot'" "Snapshot restore succeeded without confirmation."
  Assert-ControllerFailure "bash /workspace/deploy/restore-backup-linux.sh '$backupControlRoot/backup.env' latest '$restoreRoot' --confirm-disaster-restore" "Snapshot restore accepted the latest alias."
  Invoke-Controller "install -d -o 0 -g 0 -m 0700 '$linuxRoot/non-empty'; printf occupied > '$linuxRoot/non-empty/marker'; chmod 0600 '$linuxRoot/non-empty/marker'" -Quiet | Out-Null
  $nonEmptyOwner = New-ResticOwner "nonempty"
  Assert-ControllerFailure (Use-ResticOwner $nonEmptyOwner "bash /workspace/deploy/restore-backup-linux.sh '$backupControlRoot/backup.env' '$snapshotID' '$linuxRoot/non-empty' --confirm-disaster-restore") "Snapshot restore accepted a non-empty target."
  $report.nonEmptyTargetRejected = $true
  $wrongPasswordOwner = New-ResticOwner "wrong-password"
  Assert-ControllerFailure (Use-ResticOwner $wrongPasswordOwner "bash /workspace/deploy/restore-backup-linux.sh '$backupControlRoot/wrong.env' '$snapshotID' '$linuxRoot/wrong-restore' --confirm-disaster-restore") "Snapshot restore accepted a wrong Restic password."
  $report.wrongPasswordRejected = $true
  $wrongSnapshotID = 'e' * 64
  $wrongSnapshotOwner = New-ResticOwner "wrong-snapshot"
  Assert-ControllerFailure (Use-ResticOwner $wrongSnapshotOwner "bash /workspace/deploy/restore-backup-linux.sh '$backupControlRoot/backup.env' '$wrongSnapshotID' '$linuxRoot/wrong-snapshot' --confirm-disaster-restore") "Snapshot restore accepted an unknown full snapshot ID."
  Invoke-Controller "install -d -o 0 -g 0 -m 0700 '$linuxRoot/.llmgateway-restore.ABCDEFGH'; printf residue > '$linuxRoot/.llmgateway-restore.ABCDEFGH/residue'; chmod 0600 '$linuxRoot/.llmgateway-restore.ABCDEFGH/residue'" -Quiet | Out-Null
  $restoreOwner = New-ResticOwner "restore"
  Invoke-Controller (Use-ResticOwner $restoreOwner "bash /workspace/deploy/restore-backup-linux.sh '$backupControlRoot/backup.env' '$snapshotID' '$restoreRoot' --confirm-disaster-restore") -Quiet | Out-Null
  Invoke-Controller "test ! -e '$linuxRoot/.llmgateway-restore.ABCDEFGH'" -Quiet | Out-Null
  $report.staleRestoreRecovered = $true
  $report.confirmationGuards = $true

  Invoke-Controller "source /workspace/deploy/backup-lib.sh; verify_backup_payload '$restoredPayloadRoot'" -Quiet | Out-Null
  $manifestLines = @(Invoke-Controller "cat '$restoredPayloadRoot/backup-manifest'" -Quiet).Output
  if ($manifestLines.Count -ne 7 -or $manifestLines[0] -ne 'format=llmgateway-backup' -or $manifestLines[1] -notmatch '^recovery_point_utc=(.+)$') {
    throw "The restored backup manifest is invalid."
  }
  $manifestRecoveryPoint = $Matches[1]
  if ($manifestRecoveryPoint -ne $markerRecoveryPoint) {
    throw "The freshness marker and restored manifest disagree about the recovery point."
  }
  $recoveryPoint = [DateTime]::ParseExact(
    $manifestRecoveryPoint,
    "yyyy-MM-dd'T'HH:mm:ss'Z'",
    [Globalization.CultureInfo]::InvariantCulture,
    [Globalization.DateTimeStyles]::AssumeUniversal -bor [Globalization.DateTimeStyles]::AdjustToUniversal
  )
  $recoveryPointAge = [int][Math]::Floor(($recoveryStartedAt.ToUniversalTime() - $recoveryPoint).TotalSeconds)
  if ($recoveryPointAge -lt 0) { throw "The manifest recovery point is later than the disaster trigger." }
  $report.recoveryPointAgeSeconds = $recoveryPointAge
  $report.manifestValidated = $true

  Assert-ControllerFailure "bash /workspace/deploy/install-restored-configuration-linux.sh '$restoredConfigurationRoot' '$recoveredConfigurationRoot' '$restoreControlRoot/new-database-url' llmgateway_restored" "Restored configuration installation succeeded without confirmation."
  Assert-ControllerFailure "bash /workspace/deploy/install-restored-configuration-linux.sh '$restoredConfigurationRoot' '$restoredConfigurationRoot/overlap-target' '$restoreControlRoot/new-database-url' llmgateway_restored --confirm-restored-configuration-install" "Restored configuration accepted overlapping source and target paths."
  Assert-ControllerFailure "bash /workspace/deploy/install-restored-configuration-linux.sh '$restoredConfigurationRoot' '$linuxRoot/wrong-configuration' '$restoreControlRoot/wrong-database-url' llmgateway_restored --confirm-restored-configuration-install" "Restored configuration accepted a database URL for another database."
  Invoke-Controller "bash /workspace/deploy/install-restored-configuration-linux.sh '$restoredConfigurationRoot' '$recoveredConfigurationRoot' '$restoreControlRoot/new-database-url' llmgateway_restored --confirm-restored-configuration-install" -Quiet | Out-Null
  Invoke-Controller "source /workspace/deploy/backup-lib.sh; verify_runtime_configuration_tree '$recoveredConfigurationRoot'; verify_backup_payload '$restoredPayloadRoot'" -Quiet | Out-Null
  $report.configurationRestored = $true

  $recoveredEnvironment = "$recoveredConfigurationRoot/deployment.env"
  $recoveredCompose = "source /workspace/deploy/lib.sh; load_llmgateway_environment '$recoveredEnvironment'; export LLMGATEWAY_GATEWAY_IMAGE='$GatewayImage' LLMGATEWAY_CADDY_IMAGE='caddy:2.10.2-alpine' DEPLOY_DIRECTORY='$linuxRoot/deployment'; deployment_compose --file '$linuxRoot/deployment/compose.acceptance.yaml'"
  $restoreDatabaseCommand = "export DEPLOY_DIRECTORY='$linuxRoot/deployment'; bash /workspace/deploy/restore-postgres-linux.sh '$recoveredEnvironment' '$restoredPayloadRoot' llmgateway_restored"
  Assert-ControllerFailure $restoreDatabaseCommand "Database restore succeeded without confirmation."
  $activeLock = Start-MaintenanceLock $linuxRoot
  Assert-ControllerFailure "$restoreDatabaseCommand --confirm-new-database-restore" "Database restore ignored the shared maintenance lock."
  $report.maintenanceLockConflicts++
  Stop-MaintenanceLock $activeLock
  $activeLock = $null
  Invoke-Controller "$restoreDatabaseCommand --confirm-new-database-restore" -Quiet | Out-Null
  Assert-ControllerFailure "$restoreDatabaseCommand --confirm-new-database-restore" "Repeated database restore overwrote the target."
  $report.repeatedDatabaseRestoreRejected = $true

  Invoke-Controller "$recoveredCompose exec -T postgres dropdb --force --username llmgateway llmgateway_restored; $recoveredCompose exec -T postgres createdb --username llmgateway llmgateway_restored; $recoveredCompose exec -T postgres psql --username llmgateway --dbname llmgateway_restored --command 'CREATE TABLE incomplete_restore_marker (id integer PRIMARY KEY)'" -Quiet | Out-Null
  Invoke-Controller "$recoveredCompose up --detach --wait valkey" -Quiet | Out-Null
  $valkeyContainer = ([string]((Invoke-Controller "$recoveredCompose ps --quiet valkey" -Quiet).Output | Select-Object -First 1)).Trim()
  if ($valkeyContainer -notmatch '^[a-f0-9]{12,64}$') { throw "Compose returned an invalid Valkey container ID." }
  Invoke-Controller "docker pause '$valkeyContainer'" -Quiet | Out-Null
  Assert-ControllerFailure "$restoreDatabaseCommand --confirm-new-database-restore --confirm-incomplete-database-replacement" "Database restore accepted a paused non-PostgreSQL service."
  $incompleteMarkerCommand = $recoveredCompose + ' exec -T postgres psql --username llmgateway --dbname llmgateway_restored --tuples-only --no-align --command=''SELECT coalesce(to_regclass($$public.incomplete_restore_marker$$)::text, $$$$) <> $$$$'''
  $incompleteMarker = (Invoke-Controller $incompleteMarkerCommand -Quiet).Output -join ""
  if ($incompleteMarker.Trim() -ne 't') { throw "Unsafe-state rejection changed the incomplete target database." }
  $report.nonPostgresUnsafeStateRejected = $true
  Invoke-Controller "docker unpause '$valkeyContainer'; $recoveredCompose stop valkey" -Quiet | Out-Null
  Invoke-Controller "$restoreDatabaseCommand --confirm-new-database-restore --confirm-incomplete-database-replacement" -Quiet | Out-Null
  $restoredMarkerCommand = $recoveredCompose + ' exec -T postgres psql --username llmgateway --dbname llmgateway_restored --tuples-only --no-align --command=''SELECT coalesce(to_regclass($$public.incomplete_restore_marker$$)::text, $$$$) = $$$$'''
  $restoredMarker = (Invoke-Controller $restoredMarkerCommand -Quiet).Output -join ""
  if ($restoredMarker.Trim() -ne 't') { throw "Explicit incomplete-database replacement did not restore the backup." }
  $report.incompleteDatabaseReplacement = $true
  $report.postgresRestoredToNewDatabase = $true

  Invoke-Controller "$recoveredCompose --profile migration run --rm migrate" -Quiet | Out-Null
  Invoke-Controller "$recoveredCompose up --detach --wait valkey gateway-a gateway-b caddy" -Quiet | Out-Null
  Wait-HTTPSReady $deploymentURL
  Invoke-HeadedJourney "restored" $deploymentURL
  $report.recoveryTimeSeconds = [int][Math]::Ceiling(((Get-Date) - $recoveryStartedAt).TotalSeconds)

  $recoveredLogs = (Invoke-Controller "$recoveredCompose logs --no-color" -Quiet).Output -join "`n"
  if (Contains-Secret $recoveredLogs $secrets) { $report.secretsInLogs = $true }
  if ($report.secretsInLogs) { throw "A disaster-recovery test secret entered runtime logs." }
  if ($report.recoveryPointAgeSeconds -gt $report.rpoTargetSeconds -or $report.recoveryTimeSeconds -gt $report.rtoTargetSeconds) {
    throw "The disaster-recovery exercise exceeded its RPO or RTO target."
  }

  $repositoryCheckOwner = New-ResticOwner "repository-check"
  Invoke-Controller (Use-ResticOwner $repositoryCheckOwner "bash /workspace/deploy/check-restic-repository-linux.sh '$backupControlRoot/backup.env'") -Quiet | Out-Null
  $readDataOwner = New-ResticOwner "read-data"
  Invoke-Controller (Use-ResticOwner $readDataOwner "source /workspace/deploy/backup-lib.sh; load_backup_environment '$backupControlRoot/backup.env'; run_restic check --read-data") -Quiet | Out-Null
  $corruptCommand = @'
pack_file=$(find '__REPOSITORY__/data' -type f -print | sort | head -n 1)
[[ -n $pack_file ]]
first_byte=$(od -An -tu1 -N1 "$pack_file")
replacement=$(( (first_byte + 1) % 256 ))
replacement_octal=$(printf '%03o' "$replacement")
printf '%b' "\\$replacement_octal" | dd of="$pack_file" bs=1 count=1 conv=notrunc status=none
sync -f "$pack_file"
'@.Replace('__REPOSITORY__', $repositoryRoot)
  Invoke-Controller $corruptCommand -Quiet | Out-Null
  $corruptCheckOwner = New-ResticOwner "corrupt-check"
  Assert-ControllerFailure (Use-ResticOwner $corruptCheckOwner "source /workspace/deploy/backup-lib.sh; load_backup_environment '$backupControlRoot/backup.env'; run_restic check --read-data") "Restic read-data check accepted a corrupted pack."
  $report.repositoryCorruptionDetected = $true

  $evidencePath = Join-Path $evidenceDirectory "disaster-recovery-report.json"
  [IO.File]::WriteAllText($evidencePath, ($report | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
} catch {
  $failure = $_
} finally {
  $cleanupFailures = @()
  if ($controller) {
    try {
      if ($null -ne $activeLock) {
        Stop-MaintenanceLock $activeLock
        $activeLock = $null
      }
    } catch { $cleanupFailures += $_.Exception.Message }
    foreach ($owner in @($resticOwners | Select-Object -Unique)) {
      try {
        $resticCleanup = Invoke-Controller "source /workspace/deploy/backup-lib.sh; cleanup_restic_execution '$owner'" -AllowFailure -Quiet
        if ($resticCleanup.ExitCode -ne 0) { throw "Could not clean Restic execution owner $owner." }
      } catch { $cleanupFailures += $_.Exception.Message }
    }
    try {
      $inspection = @(& $docker inspect $controller | ConvertFrom-Json)
      if ($inspection.Count -ne 1 -or $inspection[0].Config.Labels.'llmgateway.test.run' -ne $runID) { throw "Controller ownership does not match." }
      $cleanupCompose = @"
source /workspace/deploy/lib.sh
if [[ -f '$recoveredConfigurationRoot/deployment.env' ]]; then
  load_llmgateway_environment '$recoveredConfigurationRoot/deployment.env'
elif [[ -f '$configurationRoot/deployment.env' ]]; then
  load_llmgateway_environment '$configurationRoot/deployment.env'
else
  export LLMGATEWAY_GATEWAY_IMAGE=cleanup LLMGATEWAY_POSTGRES_IMAGE=cleanup LLMGATEWAY_VALKEY_IMAGE=cleanup LLMGATEWAY_CADDY_IMAGE=cleanup
  export LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION=1 LLMGATEWAY_SITE_ADDRESS=https://localhost LLMGATEWAY_ACME_EMAIL=cleanup@example.invalid
  export LLMGATEWAY_POSTGRES_PASSWORD_FILE=/dev/null LLMGATEWAY_DATABASE_URL_FILE=/dev/null LLMGATEWAY_VALKEY_PASSWORD_FILE=/dev/null LLMGATEWAY_VALKEY_ACL_FILE=/dev/null
  export LLMGATEWAY_MASTER_KEYS_FILE=/dev/null LLMGATEWAY_SESSION_PEPPER_FILE=/dev/null LLMGATEWAY_API_KEY_PEPPER_FILE=/dev/null LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE=/dev/null
fi
export LLMGATEWAY_COMPOSE_PROJECT='$project' DEPLOY_DIRECTORY='$linuxRoot/deployment'
if [[ -f '$linuxRoot/deployment/compose.production.yaml' ]]; then
  deployment_compose --file '$linuxRoot/deployment/compose.acceptance.yaml' down --volumes --remove-orphans --timeout 30
fi
"@
      Invoke-Controller $cleanupCompose -AllowFailure -Quiet | Out-Null
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

Write-Host "Explicit encrypted snapshot recovery, source-volume loss, manifest-bound RPO, headed identity recovery, and corruption detection passed."
