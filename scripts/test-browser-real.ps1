[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\isolated-services.ps1"

function Get-FreeLoopbackPorts {
  param([Parameter(Mandatory = $true)][int] $Count)

  $listeners = @()
  try {
    for ($index = 0; $index -lt $Count; $index++) {
      $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
      $listener.Start()
      $listeners += $listener
    }
    return $listeners | ForEach-Object { ([System.Net.IPEndPoint] $_.LocalEndpoint).Port }
  } finally {
    foreach ($listener in $listeners) {
      $listener.Stop()
    }
  }
}

function Stop-OwnedGateway {
  param(
    [Parameter(Mandatory = $true)][string] $PIDFile,
    [Parameter(Mandatory = $true)][string] $Binary
  )

  if (-not (Test-Path -LiteralPath $PIDFile)) { return }
  $processID = 0
  $rawProcessID = (Get-Content -Raw -LiteralPath $PIDFile).Trim()
  if (-not [int]::TryParse($rawProcessID, [ref] $processID)) {
    throw "The gateway PID file is invalid: $PIDFile"
  }
  $process = Get-Process -Id $processID -ErrorAction SilentlyContinue
  if ($null -eq $process) {
    Remove-Item -LiteralPath $PIDFile -Force
    return
  }
  $expectedBinary = [System.IO.Path]::GetFullPath($Binary)
  $actualBinary = [System.IO.Path]::GetFullPath($process.Path)
  if (-not [string]::Equals($expectedBinary, $actualBinary, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to stop PID $processID because it does not own the acceptance gateway binary."
  }
  Stop-Process -Id $processID -Force
  Wait-Process -Id $processID -Timeout 10 -ErrorAction SilentlyContinue
  if ($null -ne (Get-Process -Id $processID -ErrorAction SilentlyContinue)) {
    throw "The acceptance gateway process $processID did not exit."
  }
  Remove-Item -LiteralPath $PIDFile -Force
}

function Remove-SuccessfulAcceptanceBuild {
  param(
    [Parameter(Mandatory = $true)][string] $Root,
    [Parameter(Mandatory = $true)][string] $BuildDirectory
  )

  $buildRoot = [System.IO.Path]::GetFullPath((Join-Path $Root ".build"))
  $resolved = [System.IO.Path]::GetFullPath($BuildDirectory)
  $expectedPrefix = $buildRoot.TrimEnd([System.IO.Path]::DirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
  if (-not $resolved.StartsWith($expectedPrefix, [System.StringComparison]::OrdinalIgnoreCase) -or
      -not ([System.IO.Path]::GetFileName($resolved)).StartsWith("browser-real-", [System.StringComparison]::Ordinal)) {
    throw "Refusing to remove an acceptance build outside the owned .build directory."
  }
  if (Test-Path -LiteralPath $resolved) {
    Remove-Item -LiteralPath $resolved -Recurse -Force
  }
}

$root = Split-Path -Parent $PSScriptRoot
$runID = New-LLMGatewayTestRunID -Purpose "browser"
$postgresContainer = ""
$valkeyContainer = ""
$providerProcess = $null
$postgresPassword = "browser-postgres-fixture"
$valkeyPassword = "browser-valkey-fixture"
$databaseName = "llmgateway_browser"
$buildDirectory = Join-Path $root ".build\browser-real-$runID"
$evidenceDirectory = Join-Path $root ".build\acceptance-evidence"
$runningOnWindows = $env:OS -eq "Windows_NT"
$binaryName = if ($runningOnWindows) { "gateway.exe" } else { "gateway" }
$binaryPath = Join-Path $buildDirectory $binaryName
$providerBinaryName = if ($runningOnWindows) { "fixture-provider.exe" } else { "fixture-provider" }
$providerBinaryPath = Join-Path $buildDirectory $providerBinaryName
$providerCertificatePath = Join-Path $buildDirectory "provider-ca.pem"
$providerStdoutPath = Join-Path $buildDirectory "provider.stdout.log"
$providerStderrPath = Join-Path $buildDirectory "provider.stderr.log"
$gatewayPIDFile = Join-Path $buildDirectory "gateway.pid"
$pnpmCommand = if ($runningOnWindows) { "pnpm.cmd" } else { "pnpm" }
$environmentNames = @(
  "LLMGATEWAY_PROFILE",
  "LLMGATEWAY_HTTP_ADDRESS",
  "LLMGATEWAY_DATABASE_URL",
  "LLMGATEWAY_DATABASE_MIGRATE_ON_START",
  "LLMGATEWAY_VALKEY_ADDRESS",
  "LLMGATEWAY_VALKEY_PASSWORD",
  "LLMGATEWAY_VALKEY_DATABASE",
  "LLMGATEWAY_MASTER_KEYS",
  "LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION",
  "LLMGATEWAY_SESSION_PEPPER",
  "LLMGATEWAY_API_KEY_PEPPER",
  "LLMGATEWAY_COOKIE_SECURE",
  "LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS",
  "LLMGATEWAY_PROVIDER_CA_BUNDLE_FILE",
  "LLMGATEWAY_LOG_LEVEL",
  "LLMGATEWAY_REAL_GATEWAY_BINARY",
  "LLMGATEWAY_REAL_GATEWAY_URL",
  "LLMGATEWAY_REAL_GATEWAY_LOG_DIR",
  "LLMGATEWAY_REAL_GATEWAY_PID_FILE",
  "LLMGATEWAY_REAL_PROVIDER_BASE_URL",
  "LLMGATEWAY_REAL_DOCKER_COMMAND",
  "LLMGATEWAY_REAL_POSTGRES_CONTAINER"
)
$environmentSnapshot = @{}
foreach ($name in $environmentNames) {
  $item = Get-Item "Env:$name" -ErrorAction SilentlyContinue
  if ($null -eq $item) {
    $environmentSnapshot[$name] = @{ Exists = $false; Value = "" }
  } else {
    $environmentSnapshot[$name] = @{ Exists = $true; Value = $item.Value }
  }
}

$primaryFailure = $null
$cleanupFailures = [System.Collections.Generic.List[string]]::new()
$acceptancePassed = $false

Push-Location $root
try {
  $docker = Get-LLMGatewayDockerCommand
  New-Item -ItemType Directory -Force $buildDirectory | Out-Null

  $postgres = Start-LLMGatewayTestPostgres -RunID $runID -DatabaseName $databaseName -Password $postgresPassword
  $postgresContainer = $postgres.Container
  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $valkeyPassword
  $valkeyContainer = $valkey.Container
  $applicationPorts = @(Get-FreeLoopbackPorts -Count 3)
  $gatewayPort = $applicationPorts[0]
  $providerPort = $applicationPorts[1]
  $providerAdminPort = $applicationPorts[2]
  $providerBaseURL = "https://127.0.0.1:$providerPort/v1"
  $providerAdminURL = "http://127.0.0.1:$providerAdminPort"

  $env:LLMGATEWAY_PROFILE = "test"
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$gatewayPort"
  $env:LLMGATEWAY_DATABASE_URL = $postgres.DatabaseURL
  $env:LLMGATEWAY_DATABASE_MIGRATE_ON_START = "true"
  $env:LLMGATEWAY_VALKEY_ADDRESS = $valkey.Address
  $env:LLMGATEWAY_VALKEY_PASSWORD = $valkeyPassword
  $env:LLMGATEWAY_VALKEY_DATABASE = "0"
  $env:LLMGATEWAY_MASTER_KEYS = "1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  $env:LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION = "1"
  $env:LLMGATEWAY_SESSION_PEPPER = "llmgateway-browser-session-pepper-000000"
  $env:LLMGATEWAY_API_KEY_PEPPER = "llmgateway-browser-api-key-pepper-000000"
  $env:LLMGATEWAY_COOKIE_SECURE = "false"
  $env:LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS = "198.18.0.0/15"
  $env:LLMGATEWAY_PROVIDER_CA_BUNDLE_FILE = $providerCertificatePath
  $env:LLMGATEWAY_LOG_LEVEL = "info"
  $env:LLMGATEWAY_REAL_GATEWAY_BINARY = $binaryPath
  $env:LLMGATEWAY_REAL_GATEWAY_URL = "http://127.0.0.1:$gatewayPort"
  $env:LLMGATEWAY_REAL_GATEWAY_LOG_DIR = $buildDirectory
  $env:LLMGATEWAY_REAL_GATEWAY_PID_FILE = $gatewayPIDFile
  $env:LLMGATEWAY_REAL_PROVIDER_BASE_URL = $providerBaseURL
  $env:LLMGATEWAY_REAL_DOCKER_COMMAND = $docker
  $env:LLMGATEWAY_REAL_POSTGRES_CONTAINER = $postgresContainer

  & go build -trimpath -o $providerBinaryPath .\scripts\fixtures\provider
  if ($LASTEXITCODE -ne 0) { throw "Could not build the real browser Provider fixture." }
  $providerStartArguments = @{
    FilePath               = $providerBinaryPath
    ArgumentList           = @(
      "-address", "127.0.0.1:$providerPort",
      "-admin-address", "127.0.0.1:$providerAdminPort",
      "-certificate-out", $providerCertificatePath,
      "-certificate-ip", "127.0.0.1"
    )
    PassThru               = $true
    RedirectStandardOutput = $providerStdoutPath
    RedirectStandardError  = $providerStderrPath
  }
  if ($runningOnWindows) { $providerStartArguments.WindowStyle = "Hidden" }
  $providerProcess = Start-Process @providerStartArguments
  $providerReady = $false
  $providerDeadline = (Get-Date).AddSeconds(30)
  do {
    if ($providerProcess.HasExited) { throw "Real browser Provider fixture exited before readiness." }
    try {
      $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats" -TimeoutSec 2
      $providerReady = $providerStats.held -eq 0
    } catch {
      Start-Sleep -Milliseconds 100
    }
  } while (-not $providerReady -and (Get-Date) -lt $providerDeadline)
  if (-not $providerReady -or -not (Test-Path -LiteralPath $providerCertificatePath)) {
    throw "Real browser Provider fixture did not become ready."
  }

  & $pnpmCommand --dir web run build
  if ($LASTEXITCODE -ne 0) { throw "Could not build the real browser frontend." }
  & go build -tags webembed -trimpath -o $binaryPath .\cmd\gateway
  if ($LASTEXITCODE -ne 0) { throw "Could not build the embedded real browser gateway binary." }

  & $pnpmCommand --dir web run test:e2e:real
  if ($LASTEXITCODE -ne 0) {
    throw "Real headed browser acceptance failed. Gateway logs are in $buildDirectory."
  }

  $catalogFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT count(*) || '|' || min(base_url) FROM providers WHERE catalog_id = 'siliconflow' AND slug = 'siliconflow'"
  if ($LASTEXITCODE -ne 0 -or $catalogFact -ne "1|$providerBaseURL") {
    throw "The code-owned Provider catalog was not synchronized and redirected inside the isolated database: $catalogFact"
  }
  $poolFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM resource_pools pool JOIN providers provider ON provider.id = pool.provider_id WHERE pool.name = 'Browser Pool' AND pool.slug ~ '^pool-[a-f0-9]{32}$' AND pool.status = 'active' AND provider.catalog_id = 'siliconflow') || '|' || (SELECT count(*) FROM resource_pool_models binding JOIN resource_pools pool ON pool.id = binding.resource_pool_id JOIN models model ON model.id = binding.model_id WHERE pool.name = 'Browser Pool' AND model.public_name = 'qwen3.5-9b') || '|' || (SELECT count(*) FROM resource_pool_mutations mutation JOIN resource_pools pool ON pool.id = mutation.resource_pool_id WHERE pool.name = 'Browser Pool' AND mutation.action = 'resource_pool.create') || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'resource_pool.create' AND audit.target_id = (SELECT id::text FROM resource_pools WHERE name = 'Browser Pool') AND audit.request_id IS NOT NULL)"
  if ($LASTEXITCODE -ne 0 -or $poolFact -ne "1|1|1|1") {
    throw "The browser resource pool did not preserve its Provider, model, mutation, and audit facts: $poolFact"
  }
  $credentialFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM provider_credentials credential JOIN resource_pools pool ON pool.id = credential.resource_pool_id WHERE credential.name = 'Browser Upstream Key' AND credential.status = 'active' AND pool.name = 'Browser Pool') || '|' || (SELECT count(*) FROM credential_mutations mutation JOIN provider_credentials credential ON credential.id = mutation.credential_id WHERE credential.name = 'Browser Upstream Key' AND mutation.action IN ('credential.create', 'credential.update')) || '|' || (SELECT count(*) FROM credential_models binding JOIN provider_credentials credential ON credential.id = binding.credential_id JOIN models model ON model.id = binding.model_id WHERE credential.name = 'Browser Upstream Key' AND model.public_name = 'qwen3.5-9b') || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'credential.create' AND audit.target_id = (SELECT id::text FROM provider_credentials WHERE name = 'Browser Upstream Key')) || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'credential.update' AND audit.target_id = (SELECT id::text FROM provider_credentials WHERE name = 'Browser Upstream Key')) || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'credential.probed' AND audit.target_id = (SELECT id::text FROM provider_credentials WHERE name = 'Browser Upstream Key') AND audit.request_id IS NOT NULL) || '|' || (SELECT last_probe_status || '|' || last_probe_kind FROM provider_credentials WHERE name = 'Browser Upstream Key') || '|' || (SELECT count(*) FROM credential_mutations mutation JOIN provider_credentials credential ON credential.id = mutation.credential_id WHERE credential.name = 'Browser Upstream Key' AND (mutation.result::text LIKE '%core-upstream-secret%' OR mutation.result::text LIKE '%browser-upstream-secret-replaced%' OR mutation.result::text LIKE '%encrypted_secret%')) || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.target_id = (SELECT id::text FROM provider_credentials WHERE name = 'Browser Upstream Key') AND (audit.detail::text LIKE '%core-upstream-secret%' OR audit.detail::text LIKE '%browser-upstream-secret-replaced%'))"
  if ($LASTEXITCODE -ne 0 -or $credentialFact -ne "1|2|1|1|1|1|succeeded|generation|0|0") {
    throw "The browser upstream API Key did not preserve replacement, probe, routing, audit, and secret boundaries: $credentialFact"
  }
  $planFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM service_plans plan JOIN service_plan_versions version ON version.id = plan.current_version_id WHERE plan.name = 'Browser Plan' AND plan.slug ~ '^plan-[a-f0-9]{32}$' AND plan.status = 'active' AND version.version = 1 AND version.token_quota = 50000) || '|' || (SELECT count(*) FROM service_plan_version_routes route JOIN service_plan_versions version ON version.id = route.service_plan_version_id JOIN service_plans plan ON plan.id = version.service_plan_id JOIN models model ON model.id = route.model_id JOIN resource_pools pool ON pool.id = route.resource_pool_id WHERE plan.name = 'Browser Plan' AND model.public_name = 'qwen3.5-9b' AND pool.name = 'Browser Pool') || '|' || (SELECT count(*) FROM service_plan_mutations mutation JOIN service_plans plan ON plan.id = mutation.service_plan_id WHERE plan.name = 'Browser Plan' AND mutation.action = 'service_plan.create') || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'service_plan.create' AND audit.target_id = (SELECT id::text FROM service_plans WHERE name = 'Browser Plan') AND audit.request_id IS NOT NULL)"
  if ($LASTEXITCODE -ne 0 -or $planFact -ne "1|1|1|1") {
    throw "The browser plan did not preserve one immutable published version and route: $planFact"
  }
  $subscriptionFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM users member WHERE lower(member.email) = 'browser-member@example.test' AND member.display_name = 'Browser Member' AND member.role = 'member' AND member.status = 'active') || '|' || (SELECT count(*) FROM member_mutations mutation JOIN users member ON member.id = mutation.user_id WHERE lower(member.email) = 'browser-member@example.test' AND mutation.action = 'member.create') || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'identity.member_created' AND audit.target_id = (SELECT id::text FROM users WHERE lower(email) = 'browser-member@example.test') AND audit.request_id IS NOT NULL) || '|' || (SELECT count(*) FROM subscriptions subscription JOIN users member ON member.id = subscription.user_id JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id JOIN service_plans plan ON plan.id = version.service_plan_id WHERE lower(member.email) = 'browser-member@example.test' AND plan.name = 'Browser Plan' AND version.version = 1 AND subscription.status = 'active' AND subscription.granted_tokens = 50000) || '|' || (SELECT count(*) FROM subscription_mutations mutation JOIN subscriptions subscription ON subscription.id = mutation.subscription_id JOIN users member ON member.id = subscription.user_id WHERE lower(member.email) = 'browser-member@example.test' AND mutation.action = 'subscription.create') || '|' || (SELECT count(*) FROM ledger_events event JOIN subscriptions subscription ON subscription.id = event.subscription_id JOIN users member ON member.id = subscription.user_id WHERE lower(member.email) = 'browser-member@example.test' AND event.kind = 'grant' AND event.token_delta = 50000) || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'subscription.create' AND audit.target_id = (SELECT subscription.id::text FROM subscriptions subscription JOIN users member ON member.id = subscription.user_id WHERE lower(member.email) = 'browser-member@example.test') AND audit.request_id IS NOT NULL)"
  if ($LASTEXITCODE -ne 0 -or $subscriptionFact -ne "1|1|1|1|1|1|1") {
    throw "The browser member and frozen-version subscription facts were not persisted atomically: $subscriptionFact"
  }
  $keyFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM gateway_keys key JOIN users owner ON owner.id = key.user_id WHERE lower(owner.email) = 'browser-member@example.test' AND key.name IN ('Browser Admin-Created Key', 'Browser Member-Created Key') AND key.revoked_at IS NULL) || '|' || (SELECT count(*) FROM gateway_key_mutations mutation JOIN gateway_keys key ON key.id = mutation.gateway_key_id WHERE key.name IN ('Browser Admin-Created Key', 'Browser Member-Created Key')) || '|' || (SELECT count(*) FROM gateway_key_models binding JOIN gateway_keys key ON key.id = binding.gateway_key_id JOIN models model ON model.id = binding.model_id WHERE key.name IN ('Browser Admin-Created Key', 'Browser Member-Created Key') AND model.public_name = 'qwen3.5-9b') || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'gateway_key.created' AND audit.target_id IN (SELECT id::text FROM gateway_keys WHERE name IN ('Browser Admin-Created Key', 'Browser Member-Created Key')) AND audit.request_id IS NOT NULL) || '|' || (SELECT count(DISTINCT actor.role) FROM audit_events audit JOIN users actor ON actor.id = audit.actor_user_id WHERE audit.action = 'gateway_key.created' AND audit.target_id IN (SELECT id::text FROM gateway_keys WHERE name IN ('Browser Admin-Created Key', 'Browser Member-Created Key'))) || '|' || (SELECT count(*) FROM gateway_key_mutations mutation JOIN gateway_keys key ON key.id = mutation.gateway_key_id WHERE key.name IN ('Browser Admin-Created Key', 'Browser Member-Created Key') AND mutation.result ? 'secret')"
  if ($LASTEXITCODE -ne 0 -or $keyFact -ne "2|2|2|2|2|0") {
    throw "Administrator and member API key creation did not preserve ownership, model scope, audit, and secret boundaries: $keyFact"
  }
  $gatewayKeyTestFacts = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT count(*) FILTER (WHERE request.status = 'completed') || '|' || count(*) FILTER (WHERE request.status = 'completed' AND (reservation.state <> 'settled' OR reservation.charged_tokens <> 6)) FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id JOIN gateway_keys key ON key.id = request.gateway_key_id WHERE key.name = 'Browser Admin-Created Key' AND request.stream = true"
  $gatewayKeyTestParts = [string]$gatewayKeyTestFacts -split '\|'
  if ($LASTEXITCODE -ne 0 -or $gatewayKeyTestParts.Count -ne 2 -or [int]$gatewayKeyTestParts[0] -lt 1 -or $gatewayKeyTestParts[1] -ne "0") {
    throw "The real browser API key test did not persist a completed six-Token settlement: $gatewayKeyTestFacts"
  }
  $activeSessionCount = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND expires_at > now()"
  if ($LASTEXITCODE -ne 0 -or $activeSessionCount -ne "0") {
    throw "A browser acceptance session remained active after administrator and member logout."
  }

  $runtimeLogs = @(Get-ChildItem -LiteralPath $buildDirectory -File -Filter "*.log" | Select-Object -ExpandProperty FullName)
  if ($runtimeLogs.Count -gt 0 -and
      (Select-String -LiteralPath $runtimeLogs -SimpleMatch -Quiet -Pattern "core-upstream-secret")) {
    throw "The Provider credential appeared in a browser acceptance runtime log."
  }

  New-Item -ItemType Directory -Force $evidenceDirectory | Out-Null
  foreach ($staleEvidenceName in @("catalog-desktop.png", "provider-desktop.png")) {
    $staleEvidencePath = Join-Path $evidenceDirectory $staleEvidenceName
    if (Test-Path -LiteralPath $staleEvidencePath) {
      Remove-Item -LiteralPath $staleEvidencePath -Force
    }
  }
  foreach ($screenshotName in @(
    "administrator-setup-success-desktop.png",
    "getting-started-desktop.png",
    "onboarding-tour-desktop.png",
    "resource-pool-form-desktop.png",
    "upstream-key-form-desktop.png",
    "upstream-keys-desktop.png",
    "plan-form-desktop.png",
    "subscription-form-desktop.png",
    "api-key-form-desktop.png",
    "members-desktop.png",
    "operations-desktop.png",
    "api-log-detail-desktop.png",
    "quota-records-desktop.png",
    "costs-desktop.png",
    "site-settings-desktop.png",
    "administrator-dashboard-desktop.png",
    "member-dashboard-desktop.png",
    "member-api-logs-desktop.png",
    "member-quota-records-desktop.png"
  )) {
    Copy-Item -LiteralPath (Join-Path $buildDirectory $screenshotName) -Destination (Join-Path $evidenceDirectory $screenshotName) -Force
  }
  $acceptancePassed = $true
} catch {
  $primaryFailure = $_
} finally {
  try {
    Stop-OwnedGateway -PIDFile $gatewayPIDFile -Binary $binaryPath
  } catch {
    $cleanupFailures.Add("gateway cleanup: $($_.Exception.Message)")
  }
  try {
    if ($providerProcess -and -not $providerProcess.HasExited) {
      $expectedProviderBinary = [System.IO.Path]::GetFullPath($providerBinaryPath)
      $actualProviderBinary = [System.IO.Path]::GetFullPath($providerProcess.Path)
      if (-not [string]::Equals($expectedProviderBinary, $actualProviderBinary, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to stop a Provider process that does not own the acceptance fixture binary."
      }
      Stop-Process -Id $providerProcess.Id -Force
      $null = $providerProcess.WaitForExit(5000)
    }
  } catch {
    $cleanupFailures.Add("Provider fixture cleanup: $($_.Exception.Message)")
  }
  try {
    Stop-LLMGatewayTestContainer -Container $valkeyContainer -RunID $runID
  } catch {
    $cleanupFailures.Add("Valkey cleanup: $($_.Exception.Message)")
  }
  try {
    Stop-LLMGatewayTestContainer -Container $postgresContainer -RunID $runID
  } catch {
    $cleanupFailures.Add("PostgreSQL cleanup: $($_.Exception.Message)")
  }
  try {
    foreach ($name in $environmentNames) {
      $previous = $environmentSnapshot[$name]
      if ($previous.Exists) {
        Set-Item "Env:$name" $previous.Value
      } else {
        Remove-Item "Env:$name" -ErrorAction SilentlyContinue
      }
    }
  } catch {
    $cleanupFailures.Add("environment restoration: $($_.Exception.Message)")
  }
  try {
    Pop-Location
  } catch {
    $cleanupFailures.Add("working-directory restoration: $($_.Exception.Message)")
  }
  if ($acceptancePassed -and $cleanupFailures.Count -eq 0) {
    try {
      Remove-SuccessfulAcceptanceBuild -Root $root -BuildDirectory $buildDirectory
    } catch {
      $cleanupFailures.Add("build cleanup: $($_.Exception.Message)")
    }
  }
}

if ($null -ne $primaryFailure -or $cleanupFailures.Count -ne 0) {
  $parts = [System.Collections.Generic.List[string]]::new()
  if ($null -ne $primaryFailure) {
    $parts.Add("acceptance: $($primaryFailure.Exception.Message)")
  }
  foreach ($failure in $cleanupFailures) {
    $parts.Add($failure)
  }
  throw ($parts -join [Environment]::NewLine)
}

Write-Host "Real headed resource, subscription, API key, and public model acceptance passed."
Write-Host "Latest screenshots: $evidenceDirectory"
