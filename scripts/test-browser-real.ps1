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
  "LLMGATEWAY_REAL_PROVIDER_BASE_URL"
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

  $providerFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT name || '|' || base_url || '|' || enabled FROM providers WHERE slug = 'browser-fixture'"
  if ($LASTEXITCODE -ne 0 -or $providerFact -ne "Browser Provider Ready|$providerBaseURL|true") {
    throw "The final Provider fact was not persisted in isolated PostgreSQL."
  }
  $auditJSON = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT jsonb_build_object('action', action, 'actorUserId', actor_user_id, 'targetId', target_id, 'requestId', request_id, 'detail', detail) FROM audit_events WHERE target_type = 'provider' ORDER BY created_at, id"
  if ($LASTEXITCODE -ne 0) { throw "Could not read Provider audit events." }
  $auditEvents = @($auditJSON | ForEach-Object { ConvertFrom-Json -InputObject $_ })
  $expectedActions = @(
    "provider.created",
    "provider.updated",
    "provider.updated",
    "provider.status_changed",
    "provider.updated",
    "provider.status_changed",
    "provider.updated",
    "provider.status_changed"
  )
  if ($auditEvents.Count -ne $expectedActions.Count) {
    throw "The Provider lifecycle persisted $($auditEvents.Count) audit events instead of $($expectedActions.Count)."
  }
  $actualActions = @($auditEvents | ForEach-Object { $_.action })
  if (($actualActions -join "|") -ne ($expectedActions -join "|")) {
    throw "The Provider audit action sequence is invalid: $($actualActions -join '|')"
  }
  $targetIDs = @($auditEvents | ForEach-Object { $_.targetId } | Select-Object -Unique)
  if ($targetIDs.Count -ne 1 -or [string]::IsNullOrWhiteSpace($targetIDs[0])) {
    throw "Provider audit events do not share one stable target ID."
  }
  if (@($auditEvents | Where-Object { [string]::IsNullOrWhiteSpace($_.actorUserId) }).Count -ne 0) {
    throw "A Provider audit event is missing its actor."
  }
  if ($auditEvents[0].detail.before -ne $null -or
      $auditEvents[0].detail.after.slug -ne "browser-fixture" -or
      $auditEvents[-1].detail.before.enabled -ne $false -or
      $auditEvents[-1].detail.after.enabled -ne $true) {
    throw "Provider audit details do not describe the created slug and final enabled state."
  }
  if (@($auditEvents | Where-Object { [string]::IsNullOrWhiteSpace($_.requestId) }).Count -ne 0) {
    throw "A Provider audit event is missing its HTTP request ID."
  }

  $identityFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT u.status || '|' || (i.claimed_by = u.id)::text || '|' || octet_length(i.code_prefix)::text FROM users u JOIN invitations i ON i.claimed_by = u.id WHERE lower(u.email) = 'browser-member@example.test'"
  if ($LASTEXITCODE -ne 0 -or $identityFact -ne "active|true|13") {
    throw "The browser member, claimed invitation, or stable invitation prefix was not persisted."
  }
  $invitationFacts = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM invitations) || '|' || (SELECT count(*) FROM invitation_mutations) || '|' || (SELECT count(*) FROM audit_events WHERE action = 'invitation.created') || '|' || (SELECT count(*) FROM invitations invitation WHERE (SELECT count(*) FROM invitation_mutations mutation WHERE mutation.invitation_id = invitation.id) <> 1 OR (SELECT count(*) FROM audit_events audit WHERE audit.action = 'invitation.created' AND audit.target_id = invitation.id::text) <> 1) || '|' || (SELECT count(*) FROM invitation_mutations mutation JOIN invitations invitation ON invitation.id = mutation.invitation_id WHERE mutation.result::text ~ 'invite_[A-Za-z0-9_-]{20,}' OR position('digest' IN lower(mutation.result::text)) > 0 OR position(encode(invitation.code_digest, 'hex') IN mutation.result::text) > 0 OR position(encode(invitation.code_digest, 'base64') IN mutation.result::text) > 0) || '|' || (SELECT count(*) FROM audit_events audit JOIN invitations invitation ON audit.target_id = invitation.id::text WHERE audit.action = 'invitation.created' AND (audit.detail::text ~ 'invite_[A-Za-z0-9_-]{20,}' OR position('digest' IN lower(audit.detail::text)) > 0 OR position(encode(invitation.code_digest, 'hex') IN audit.detail::text) > 0 OR position(encode(invitation.code_digest, 'base64') IN audit.detail::text) > 0))"
  if ($LASTEXITCODE -ne 0 -or $invitationFacts -ne "1|1|1|0|0|0") {
    throw "The browser invitation lifecycle did not preserve one singular, secret-free mutation and audit fact: $invitationFacts"
  }
  $catalogFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM models WHERE provider_id = (SELECT id FROM providers WHERE slug = 'browser-fixture')) || '|' || (SELECT count(*) FROM config_revision_models WHERE revision_id = (SELECT revision_id FROM active_config WHERE singleton = true)) || '|' || (SELECT count(*) FROM config_revision_credentials WHERE revision_id = (SELECT revision_id FROM active_config WHERE singleton = true)) || '|' || (SELECT count(*) FROM config_revision_routes WHERE revision_id = (SELECT revision_id FROM active_config WHERE singleton = true)) || '|' || (SELECT version FROM active_config WHERE singleton = true)"
  if ($LASTEXITCODE -ne 0 -or $catalogFact -ne "3|2|1|2|1") {
    throw "The browser publication did not preserve exactly three live models and a two-model routable active snapshot: $catalogFact"
  }
  $reasoningProfileFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT capabilities->>'reasoning_mode' FROM models WHERE public_name = 'browser-chat') || '|' || (SELECT capabilities->>'reasoning_mode' FROM config_revision_models WHERE public_name = 'browser-chat' AND revision_id = (SELECT revision_id FROM active_config WHERE singleton = true))"
  if ($LASTEXITCODE -ne 0 -or $reasoningProfileFact -ne "toggle|toggle") {
    throw "The browser model reasoning profile did not persist from the live registry into the active revision: $reasoningProfileFact"
  }
  $credentialFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM provider_credentials WHERE name = 'Browser credential') || '|' || (SELECT count(*) FROM credential_mutations mutation JOIN provider_credentials credential ON credential.id = mutation.credential_id WHERE credential.name = 'Browser credential') || '|' || (SELECT count(*) FROM credential_models binding JOIN provider_credentials credential ON credential.id = binding.credential_id WHERE credential.name = 'Browser credential') || '|' || (SELECT count(*) FROM audit_events audit JOIN provider_credentials credential ON credential.id::text = audit.target_id WHERE credential.name = 'Browser credential' AND audit.action = 'credential.created') || '|' || (SELECT count(*) FROM audit_events audit JOIN provider_credentials credential ON credential.id::text = audit.target_id WHERE credential.name = 'Browser credential' AND audit.action = 'credential.updated') || '|' || (SELECT count(*) FROM audit_events audit JOIN provider_credentials credential ON credential.id::text = audit.target_id WHERE credential.name = 'Browser credential' AND audit.action = 'credential.status_changed') || '|' || (SELECT count(*) FROM audit_events audit JOIN provider_credentials credential ON credential.id::text = audit.target_id WHERE credential.name = 'Browser credential' AND audit.action = 'credential.probed') || '|' || (SELECT last_probe_status || '|' || rpm_limit || '|' || status::text FROM provider_credentials WHERE name = 'Browser credential') || '|' || (SELECT count(*) FROM credential_mutations mutation JOIN provider_credentials credential ON credential.id = mutation.credential_id WHERE credential.name = 'Browser credential' AND (mutation.result::text LIKE '%core-upstream-secret%' OR mutation.result::text LIKE '%encrypted_secret%')) || '|' || (SELECT count(*) FROM audit_events audit JOIN provider_credentials credential ON credential.id::text = audit.target_id WHERE credential.name = 'Browser credential' AND audit.detail::text LIKE '%core-upstream-secret%') || '|' || (SELECT string_agg(binding.priority::text || ':' || binding.weight::text, ',' ORDER BY binding.priority) FROM credential_models binding JOIN provider_credentials credential ON credential.id = binding.credential_id WHERE credential.name = 'Browser credential') || '|' || (SELECT string_agg(route.priority::text || ':' || route.weight::text, ',' ORDER BY route.priority) FROM config_revision_routes route JOIN config_revision_credentials credential ON credential.revision_id = route.revision_id AND credential.credential_id = route.credential_id JOIN provider_credentials live ON live.id = credential.credential_id WHERE live.name = 'Browser credential' AND route.revision_id = (SELECT revision_id FROM active_config WHERE singleton = true))"
  if ($LASTEXITCODE -ne 0 -or $credentialFact -ne "1|4|2|1|1|2|1|succeeded|75|active|0|0|10:80,20:30|10:80,20:30") {
    throw "The browser credential lifecycle did not preserve atomic mutations, audits, probe facts, and secret boundaries: $credentialFact"
  }
  $entitlementFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM entitlements entitlement JOIN users owner ON owner.id = entitlement.user_id WHERE lower(owner.email) = 'browser-member@example.test') || '|' || (SELECT count(*) FROM ledger_events event JOIN entitlements entitlement ON entitlement.id = event.entitlement_id JOIN users owner ON owner.id = entitlement.user_id WHERE lower(owner.email) = 'browser-member@example.test' AND event.kind = 'grant') || '|' || (SELECT count(*) FROM audit_events audit JOIN entitlements entitlement ON audit.target_id = entitlement.id::text JOIN users owner ON owner.id = entitlement.user_id WHERE lower(owner.email) = 'browser-member@example.test' AND audit.action = 'quota.entitlement_created') || '|' || (SELECT granted_tokens FROM entitlements entitlement JOIN users owner ON owner.id = entitlement.user_id WHERE lower(owner.email) = 'browser-member@example.test') || '|' || (SELECT coalesce(sum(event.token_delta), 0) FROM ledger_events event JOIN entitlements entitlement ON entitlement.id = event.entitlement_id JOIN users owner ON owner.id = entitlement.user_id WHERE lower(owner.email) = 'browser-member@example.test') || '|' || (SELECT concurrency_limit FROM entitlements entitlement JOIN users owner ON owner.id = entitlement.user_id WHERE lower(owner.email) = 'browser-member@example.test') || '|' || (SELECT model.public_name FROM entitlements entitlement JOIN users owner ON owner.id = entitlement.user_id JOIN models model ON model.id = entitlement.model_id WHERE lower(owner.email) = 'browser-member@example.test') || '|' || (SELECT count(*) FROM audit_events audit JOIN entitlements entitlement ON audit.target_id = entitlement.id::text JOIN users owner ON owner.id = entitlement.user_id WHERE lower(owner.email) = 'browser-member@example.test' AND audit.action = 'quota.entitlement_created' AND (audit.request_id IS NULL OR audit.request_id = ''))"
  $entitlementParts = [string]$entitlementFact -split '\|'
  if ($LASTEXITCODE -ne 0 -or $entitlementParts.Count -ne 8 -or
      $entitlementParts[0] -ne "1" -or $entitlementParts[1] -ne "1" -or $entitlementParts[2] -ne "1" -or
      $entitlementParts[3] -ne "50000" -or [int64]$entitlementParts[4] -ge 50000 -or
      $entitlementParts[5] -ne "2" -or $entitlementParts[6] -ne "browser-chat" -or $entitlementParts[7] -ne "0") {
    throw "The browser quota grant did not preserve one entitlement, append-only grant, request-scoped audit, and exact model scope: $entitlementFact"
  }
  $keyFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM gateway_keys key JOIN users owner ON owner.id = key.user_id WHERE lower(owner.email) = 'browser-member@example.test' AND key.name = 'Browser member Key' AND key.revoked_at IS NOT NULL) || '|' || (SELECT count(*) FROM gateway_key_mutations mutation JOIN gateway_keys key ON key.id = mutation.gateway_key_id WHERE key.name = 'Browser member Key') || '|' || (SELECT count(*) FROM gateway_key_models binding JOIN gateway_keys key ON key.id = binding.gateway_key_id WHERE key.name = 'Browser member Key') || '|' || (SELECT count(*) FROM audit_events WHERE action = 'gateway_key.revoked' AND target_id = (SELECT id::text FROM gateway_keys WHERE name = 'Browser member Key')) || '|' || (SELECT count(*) FROM gateway_keys key JOIN users owner ON owner.id = key.user_id WHERE lower(owner.email) = 'browser-member@example.test' AND key.name = 'Browser member Key replacement' AND key.revoked_at IS NULL) || '|' || (SELECT count(*) FROM gateway_key_mutations mutation JOIN gateway_keys key ON key.id = mutation.gateway_key_id WHERE key.name = 'Browser member Key replacement') || '|' || (SELECT count(*) FROM gateway_key_models binding JOIN gateway_keys key ON key.id = binding.gateway_key_id WHERE key.name = 'Browser member Key replacement') || '|' || (SELECT count(*) FROM audit_events WHERE action = 'gateway_key.replaced' AND target_id = (SELECT id::text FROM gateway_keys WHERE name = 'Browser member Key replacement') AND detail->>'replaces_key_id' = (SELECT id::text FROM gateway_keys WHERE name = 'Browser member Key')) || '|' || (SELECT count(*) FROM gateway_key_mutations mutation JOIN gateway_keys key ON key.id = mutation.gateway_key_id WHERE key.name = 'Browser member Key replacement' AND mutation.result ? 'secret')"
  if ($LASTEXITCODE -ne 0 -or $keyFact -ne "1|1|1|1|1|1|1|1|0") {
    throw "The browser gateway Key replacement did not preserve the overlap, mutation, authorization, audit, and secret boundaries: $keyFact"
  }
  $recoveryFact = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT (SELECT count(*) FROM member_password_reset_mutations mutation JOIN users owner ON owner.id = mutation.user_id WHERE lower(owner.email) = 'browser-member@example.test' AND mutation.result->>'revoked_sessions' = '1') || '|' || (SELECT count(*) FROM audit_events audit JOIN users owner ON owner.id::text = audit.target_id WHERE lower(owner.email) = 'browser-member@example.test' AND audit.action = 'identity.member_password_reset' AND audit.request_id IS NOT NULL) || '|' || (SELECT count(*) FROM audit_events audit JOIN users owner ON owner.id::text = audit.target_id WHERE lower(owner.email) = 'browser-admin@example.test' AND audit.action = 'identity.sessions_revoked' AND (audit.detail->>'revoked_sessions')::bigint >= 1) || '|' || (SELECT count(*) FROM sessions session JOIN users owner ON owner.id = session.user_id WHERE lower(owner.email) IN ('browser-member@example.test', 'browser-admin@example.test') AND session.revoked_at IS NULL AND session.expires_at > now())"
  if ($LASTEXITCODE -ne 0 -or $recoveryFact -ne "1|1|1|0") {
    throw "The browser account recovery did not preserve idempotent reset, session revocation, audit, and logout facts: $recoveryFact"
  }
  $gatewayKeyTestFacts = & $docker exec $postgresContainer psql -v ON_ERROR_STOP=1 -U llmgateway -d $databaseName -Atc `
    "SELECT count(*) || '|' || count(*) FILTER (WHERE request.status = 'completed') || '|' || count(*) FILTER (WHERE reservation.state = 'settled') || '|' || coalesce(sum(reservation.charged_tokens), 0) || '|' || count(*) FILTER (WHERE request.status = 'uncertain') || '|' || count(*) FILTER (WHERE request.status = 'uncertain' AND reservation.state = 'reserved' AND reservation.charged_tokens = 0 AND reservation.usage_source = 'unknown') || '|' || (SELECT count(*) FROM request_attempts attempt JOIN requests inner_request ON inner_request.id = attempt.request_id JOIN gateway_keys inner_key ON inner_key.id = inner_request.gateway_key_id WHERE inner_key.name = 'Browser member Key' AND attempt.status = 'uncertain') FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id JOIN gateway_keys key ON key.id = request.gateway_key_id WHERE key.name = 'Browser member Key' AND request.stream = true"
  $providerCanceled = (Invoke-RestMethod -Uri "$providerAdminURL/stats").canceled
  if ($LASTEXITCODE -ne 0 -or $gatewayKeyTestFacts -ne "2|1|1|6|1|1|1" -or $providerCanceled -lt 1) {
    throw "The real browser Gateway Key test did not preserve one settlement and one upstream-canceled uncertain hold: requests=$gatewayKeyTestFacts providerCanceled=$providerCanceled"
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
  Copy-Item -LiteralPath (Join-Path $buildDirectory "provider-desktop.png") -Destination (Join-Path $evidenceDirectory "provider-desktop.png") -Force
  Copy-Item -LiteralPath (Join-Path $buildDirectory "catalog-desktop.png") -Destination (Join-Path $evidenceDirectory "catalog-desktop.png") -Force
  Copy-Item -LiteralPath (Join-Path $buildDirectory "member-usage-mobile.png") -Destination (Join-Path $evidenceDirectory "member-usage-mobile.png") -Force
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

Write-Host "Real headed publication, identity, Key, and public model acceptance passed."
Write-Host "Latest screenshots: $evidenceDirectory"
