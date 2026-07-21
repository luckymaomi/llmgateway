param(
  [string] $ReleaseImage = "llmgateway:deployment-release",
  [string] $CandidateImage = "llmgateway:deployment-candidate",
  [switch] $SkipBuild
)

$ErrorActionPreference = "Stop"
. "$PSScriptRoot\docker.ps1"
. "$PSScriptRoot\isolated-services.ps1"

$root = Split-Path -Parent $PSScriptRoot
$runID = New-LLMGatewayTestRunID -Purpose "deployment"
$project = "llmgateway-$runID"
if ($project -notmatch '^llmgateway-deployment-[a-z0-9-]+$') { throw "Generated deployment project name is unsafe." }
$buildDirectory = Join-Path $root ".build\$runID"
$evidenceDirectory = Join-Path $root ".build\acceptance-evidence"
$composeFile = Join-Path $root "deploy\compose.production.yaml"
$acceptanceComposeFile = Join-Path $root "deploy\compose.acceptance.yaml"
$backupPath = Join-Path $buildDirectory "pre-upgrade.dump"
$logPath = Join-Path $buildDirectory "deployment.log"
$environmentSnapshot = Save-LLMGatewayEnvironment
$docker = Get-LLMGatewayDockerCommand
$failure = $null
$started = $false
$report = [ordered]@{
  topology = "linux-compose-two-gateways-postgres-valkey-caddy-tls"
  migrationFailureContained = $false
  gatewayFailureContained = $false
  rollingUpgrade = $false
  orderedRestart = $false
  restoredDatabase = $false
  headedBrowserJourneys = 0
  publicMetricsBlocked = $false
  backendMetricsScrape = $false
  secretsInEnvironment = $false
  secretsInLogs = $false
}

function New-RandomHex {
  param([int] $Bytes)
  $buffer = [byte[]]::new($Bytes)
  $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
  try { $generator.GetBytes($buffer) } finally { $generator.Dispose() }
  return (($buffer | ForEach-Object { $_.ToString("x2") }) -join "")
}

function Write-SecretFile {
  param([string] $Name, [string] $Value)
  $path = Join-Path $buildDirectory $Name
  [IO.File]::WriteAllText($path, $Value, [Text.UTF8Encoding]::new($false))
  return $path
}

function Invoke-Compose {
  param([string[]] $Arguments, [switch] $AllowFailure, [switch] $Quiet)
  $previousErrorPreference = $ErrorActionPreference
  try {
    $ErrorActionPreference = "Continue"
    $output = @(& $docker compose --project-name $project --file $composeFile --file $acceptanceComposeFile @Arguments 2>&1)
    $exitCode = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $previousErrorPreference
  }
  if (-not $Quiet) { $output | ForEach-Object { Write-Host $_ } }
  if ($exitCode -ne 0 -and -not $AllowFailure) {
    throw "Docker Compose failed with exit code ${exitCode}: $($Arguments -join ' ')"
  }
  return [pscustomobject]@{ ExitCode = $exitCode; Output = $output }
}

function Get-ComposeContainer {
  param([string] $Service)
  $result = Invoke-Compose -Arguments @("ps", "--quiet", $Service) -Quiet
  $container = [string]($result.Output | Select-Object -First 1)
  if (-not $container) { throw "Compose service $Service has no container." }
  return $container.Trim()
}

function Assert-HTTPSReady {
  $deadline = (Get-Date).AddSeconds(90)
  do {
    $previousErrorPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
      $null = & curl.exe --silent --fail --insecure --output NUL "$env:LLMGATEWAY_DEPLOYMENT_URL/health/ready" 2>$null
      $probeExitCode = $LASTEXITCODE
    } finally {
      $ErrorActionPreference = $previousErrorPreference
    }
    if ($probeExitCode -eq 0) { return }
    Start-Sleep -Milliseconds 500
  } while ((Get-Date) -lt $deadline)
  throw "The production TLS entry did not become ready."
}

function Assert-PublicMetricsBlocked {
  $status = & curl.exe --silent --insecure --output NUL --write-out "%{http_code}" "$env:LLMGATEWAY_DEPLOYMENT_URL/metrics"
  if ($LASTEXITCODE -ne 0 -or [string]$status -ne "404") { throw "The public TLS entry exposed /metrics." }
  $report.publicMetricsBlocked = $true
}

function Invoke-HeadedJourney {
  param([ValidateSet("setup", "restored")][string] $Mode)
  $env:LLMGATEWAY_DEPLOYMENT_MODE = $Mode
  pnpm.cmd --dir web exec playwright test --config playwright.deployment.config.ts
  if ($LASTEXITCODE -ne 0) { throw "The $Mode deployment browser journey failed." }
  $report.headedBrowserJourneys++
}

function Set-DatabaseTarget {
  param([string] $DatabaseName)
  $dsn = "postgres://llmgateway:$databasePassword@postgres:5432/$DatabaseName`?sslmode=disable"
  [IO.File]::WriteAllText($env:LLMGATEWAY_DATABASE_URL_FILE, $dsn, [Text.UTF8Encoding]::new($false))
}

Push-Location $root
try {
  New-Item -ItemType Directory -Force -Path $buildDirectory, $evidenceDirectory | Out-Null
  Clear-LLMGatewayEnvironment

  $databasePassword = New-RandomHex -Bytes 24
  $valkeyPassword = New-RandomHex -Bytes 24
  $masterKeyBytes = [byte[]]::new(32)
  $masterKeyGenerator = [Security.Cryptography.RandomNumberGenerator]::Create()
  try { $masterKeyGenerator.GetBytes($masterKeyBytes) } finally { $masterKeyGenerator.Dispose() }
  $masterKey = [Convert]::ToBase64String($masterKeyBytes)
  $secretValues = @(
    $databasePassword,
    $valkeyPassword,
    $masterKey,
    (New-RandomHex -Bytes 32),
    (New-RandomHex -Bytes 32),
    (New-RandomHex -Bytes 32)
  )
  $env:LLMGATEWAY_POSTGRES_PASSWORD_FILE = Write-SecretFile "postgres-password" $databasePassword
  $env:LLMGATEWAY_DATABASE_URL_FILE = Write-SecretFile "database-url" ""
  Set-DatabaseTarget -DatabaseName "llmgateway"
  $env:LLMGATEWAY_VALKEY_PASSWORD_FILE = Write-SecretFile "valkey-password" $valkeyPassword
  $env:LLMGATEWAY_VALKEY_ACL_FILE = Write-SecretFile "valkey-acl" "user default on >$valkeyPassword ~* &* +@all"
  $env:LLMGATEWAY_MASTER_KEYS_FILE = Write-SecretFile "master-keys" "1:$masterKey"
  $env:LLMGATEWAY_SESSION_PEPPER_FILE = Write-SecretFile "session-pepper" $secretValues[3]
  $env:LLMGATEWAY_API_KEY_PEPPER_FILE = Write-SecretFile "api-key-pepper" $secretValues[4]
  $env:LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE = Write-SecretFile "coordination-secret" $secretValues[5]
  $env:LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION = "1"
  $env:LLMGATEWAY_GATEWAY_IMAGE = $ReleaseImage
  $env:LLMGATEWAY_POSTGRES_IMAGE = "postgres:18.4-alpine"
  $env:LLMGATEWAY_VALKEY_IMAGE = "valkey/valkey:9.1.0-alpine"
  $env:LLMGATEWAY_CADDY_IMAGE = "caddy:2.10.2-alpine"
  $env:LLMGATEWAY_SITE_ADDRESS = "https://localhost"
  $env:LLMGATEWAY_ACME_EMAIL = "staging@example.invalid"
  $env:LLMGATEWAY_HTTP_PORT = [string](Get-LLMGatewayFreeLoopbackPort)
  $env:LLMGATEWAY_HTTPS_PORT = [string](Get-LLMGatewayFreeLoopbackPort)
  $env:LLMGATEWAY_DEPLOYMENT_URL = "https://localhost:$($env:LLMGATEWAY_HTTPS_PORT)"

  if (-not $SkipBuild) {
    & $docker build --build-arg RELEASE_VERSION=deployment-release --tag $ReleaseImage .
    if ($LASTEXITCODE -ne 0) { throw "Release deployment image build failed." }
    & $docker build --build-arg RELEASE_VERSION=deployment-candidate --tag $CandidateImage .
    if ($LASTEXITCODE -ne 0) { throw "Candidate deployment image build failed." }
  }

  $null = Invoke-Compose -Arguments @("config", "--quiet")
  $started = $true
  $null = Invoke-Compose -Arguments @("up", "--detach", "--wait", "postgres", "valkey")
  $null = Invoke-Compose -Arguments @("--profile", "migration", "run", "--rm", "migrate")
  $null = Invoke-Compose -Arguments @("up", "--detach", "--wait", "gateway-a", "gateway-b", "caddy")
  Assert-HTTPSReady
  Assert-PublicMetricsBlocked

  foreach ($service in @("gateway-a", "gateway-b")) {
    $container = Get-ComposeContainer $service
    $facts = & $docker inspect --format '{{.Config.User}}|{{.HostConfig.ReadonlyRootfs}}|{{json .HostConfig.CapDrop}}' $container
    if ($LASTEXITCODE -ne 0 -or $facts -notmatch '^65532:65532\|true\|\["ALL"\]$') {
      throw "$service did not preserve the non-root, read-only, capability-free runtime contract."
    }
    $environment = [string](& $docker inspect --format '{{json .Config.Env}}' $container)
    foreach ($secret in $secretValues + @($databasePassword, $valkeyPassword)) {
      if ($environment.IndexOf($secret, [StringComparison]::Ordinal) -ge 0) { $report.secretsInEnvironment = $true }
    }
  }
  if ($report.secretsInEnvironment) { throw "A deployment secret entered the container environment." }

  Invoke-HeadedJourney -Mode "setup"

  $postgresContainer = Get-ComposeContainer "postgres"
  & powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\backup-postgres.ps1 `
    -OutputPath $backupPath -Container $postgresContainer -DatabaseName llmgateway `
    -DatabaseUser llmgateway -ExpectedComposeProject $project
  if ($LASTEXITCODE -ne 0) { throw "Pre-upgrade PostgreSQL backup failed." }

  Set-DatabaseTarget -DatabaseName "missing_migration_target"
  $migrationFailure = Invoke-Compose -Arguments @("--profile", "migration", "run", "--rm", "migrate") -AllowFailure -Quiet
  if ($migrationFailure.ExitCode -eq 0) { throw "The deliberately invalid migration unexpectedly succeeded." }
  Set-DatabaseTarget -DatabaseName "llmgateway"
  Assert-HTTPSReady
  $report.migrationFailureContained = $true

  $env:LLMGATEWAY_GATEWAY_IMAGE = $CandidateImage
  $env:LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION = "99"
  $gatewayFailure = Invoke-Compose -Arguments @("up", "--detach", "--no-deps", "--force-recreate", "--wait", "--wait-timeout", "25", "gateway-a") -AllowFailure -Quiet
  if ($gatewayFailure.ExitCode -eq 0) { throw "The deliberately invalid Gateway replacement unexpectedly became healthy." }
  Assert-HTTPSReady
  $report.gatewayFailureContained = $true

  $env:LLMGATEWAY_GATEWAY_IMAGE = $ReleaseImage
  $env:LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION = "1"
  $null = Invoke-Compose -Arguments @("up", "--detach", "--no-deps", "--force-recreate", "--wait", "gateway-a")
  Assert-HTTPSReady

  $env:LLMGATEWAY_GATEWAY_IMAGE = $CandidateImage
  foreach ($service in @("gateway-a", "gateway-b")) {
    $null = Invoke-Compose -Arguments @("up", "--detach", "--no-deps", "--force-recreate", "--wait", $service)
    Assert-HTTPSReady
  }
  $candidateID = [string](& $docker image inspect --format '{{.Id}}' $CandidateImage)
  foreach ($service in @("gateway-a", "gateway-b")) {
    $containerID = [string](& $docker inspect --format '{{.Image}}' (Get-ComposeContainer $service))
    if ($containerID.Trim() -ne $candidateID.Trim()) { throw "$service did not run the candidate image." }
  }
  $report.rollingUpgrade = $true

  $null = Invoke-Compose -Arguments @("stop", "caddy", "gateway-a", "gateway-b", "valkey", "postgres")
  $null = Invoke-Compose -Arguments @("start", "postgres", "valkey")
  $null = Invoke-Compose -Arguments @("up", "--detach", "--wait", "postgres", "valkey")
  $null = Invoke-Compose -Arguments @("start", "gateway-a", "gateway-b")
  $null = Invoke-Compose -Arguments @("up", "--detach", "--wait", "gateway-a", "gateway-b")
  $null = Invoke-Compose -Arguments @("start", "caddy")
  Assert-HTTPSReady
  $report.orderedRestart = $true

  & powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\restore-postgres.ps1 `
    -InputPath $backupPath -TargetDatabase llmgateway_restored -Container $postgresContainer `
    -DatabaseUser llmgateway -ExpectedComposeProject $project -ConfirmRestore
  if ($LASTEXITCODE -ne 0) { throw "PostgreSQL restore into the switch target failed." }
  Set-DatabaseTarget -DatabaseName "llmgateway_restored"
  foreach ($service in @("gateway-a", "gateway-b")) {
    $null = Invoke-Compose -Arguments @("up", "--detach", "--no-deps", "--force-recreate", "--wait", $service)
    Assert-HTTPSReady
  }
  Invoke-HeadedJourney -Mode "restored"
  $report.restoredDatabase = $true

  $caddyContainer = Get-ComposeContainer "caddy"
  $backendMetrics = @(& $docker exec $caddyContainer wget -qO- http://gateway-a:8080/metrics 2>$null) -join "`n"
  if ($LASTEXITCODE -ne 0 -or $backendMetrics -notmatch 'llmgateway_admission_requests_total' -or $backendMetrics -notmatch 'llmgateway_provider_attempts_total' -or $backendMetrics -notmatch 'llmgateway_quota_operations_total') {
    throw "The backend metrics scrape did not expose the domain metric contract."
  }
  $report.backendMetricsScrape = $true

  $logs = (Invoke-Compose -Arguments @("logs", "--no-color") -Quiet).Output -join "`n"
  [IO.File]::WriteAllText($logPath, $logs, [Text.UTF8Encoding]::new($false))
  foreach ($secret in $secretValues + @($databasePassword, $valkeyPassword)) {
    if ($logs.IndexOf($secret, [StringComparison]::Ordinal) -ge 0) { $report.secretsInLogs = $true }
  }
  if ($report.secretsInLogs) { throw "A deployment secret entered runtime logs." }

  $evidencePath = Join-Path $evidenceDirectory "deployment-report.json"
  [IO.File]::WriteAllText($evidencePath, ($report | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
} catch {
  $failure = $_
} finally {
  $cleanupFailures = @()
  if ($started) {
    try {
      $containers = (Invoke-Compose -Arguments @("ps", "--all", "--quiet") -AllowFailure -Quiet).Output
      foreach ($container in $containers) {
        if (-not $container) { continue }
        $inspection = @(& $docker inspect $container 2>$null | ConvertFrom-Json)
        $actualProject = if ($inspection.Count -eq 1) { [string]$inspection[0].Config.Labels.'com.docker.compose.project' } else { "" }
        if ($LASTEXITCODE -ne 0 -or $actualProject -ne $project) {
          throw "Refusing deployment cleanup because Compose ownership does not match."
        }
      }
      $down = Invoke-Compose -Arguments @("down", "--volumes", "--remove-orphans", "--timeout", "30") -AllowFailure -Quiet
      if ($down.ExitCode -ne 0) { throw "Docker Compose cleanup failed." }
    } catch { $cleanupFailures += $_.Exception.Message }
  }
  try { Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot } catch { $cleanupFailures += $_.Exception.Message }
  try {
    $resolvedBuild = [IO.Path]::GetFullPath($buildDirectory)
    $resolvedRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
    if (-not $resolvedBuild.StartsWith($resolvedRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
      throw "Refusing to remove an unowned deployment build directory."
    }
    if (Test-Path -LiteralPath $resolvedBuild) { Remove-Item -LiteralPath $resolvedBuild -Recurse -Force }
  } catch { $cleanupFailures += $_.Exception.Message }
  try { Pop-Location } catch { $cleanupFailures += $_.Exception.Message }
  if ($null -ne $failure) {
    if ($cleanupFailures.Count -gt 0) { throw "Deployment test failed: $($failure.Exception.Message) Cleanup also failed: $($cleanupFailures -join '; ')" }
    throw $failure
  }
  if ($cleanupFailures.Count -gt 0) { throw "Deployment cleanup failed: $($cleanupFailures -join '; ')" }
}

Write-Host "Production Compose TLS, headed administrator/member journeys, migration containment, rolling replacement, ordered restart, and restored-database switch passed."
