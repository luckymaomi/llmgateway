$ErrorActionPreference = "Stop"

. "$PSScriptRoot\isolated-services.ps1"

function Assert-HTTPFailureStatus {
  param(
    [Parameter(Mandatory = $true)][scriptblock] $Action,
    [Parameter(Mandatory = $true)][int] $ExpectedStatus,
    [Parameter(Mandatory = $true)][string] $FailureMessage
  )

  try {
    & $Action | Out-Null
  } catch {
    $response = $_.Exception.Response
    if ($null -ne $response -and [int] $response.StatusCode -eq $ExpectedStatus) { return }
    throw
  }
  throw $FailureMessage
}

function Wait-LLMGatewayReady {
  param(
    [Parameter(Mandatory = $true)][System.Diagnostics.Process] $Process,
    [Parameter(Mandatory = $true)][string] $BaseURL
  )

  $deadline = (Get-Date).AddSeconds(30)
  do {
    if ($Process.HasExited) { throw "Gateway exited before becoming ready." }
    try {
      $health = Invoke-RestMethod -Uri "$BaseURL/health/ready" -TimeoutSec 2
      if ($health.status -eq "ready") { return }
    } catch {
      Start-Sleep -Milliseconds 200
    }
  } while ((Get-Date) -lt $deadline)
  throw "Gateway did not become ready."
}

function Set-ProviderFixtureCatalog {
  param(
    [Parameter(Mandatory = $true)][string] $Docker,
    [Parameter(Mandatory = $true)][string] $Container,
    [Parameter(Mandatory = $true)][string] $ProviderBaseURL
  )

  & $Docker exec $Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -c `
    "UPDATE providers SET base_url = '$ProviderBaseURL' WHERE catalog_id = 'siliconflow'; UPDATE models SET upstream_name = 'fixture-chat' WHERE provider_id = (SELECT id FROM providers WHERE catalog_id = 'siliconflow');" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Could not redirect the code-owned Provider catalog to the fixture." }
}

function New-MutationHeaders {
  param(
    [Parameter(Mandatory = $true)][string] $CSRF
  )
  return @{
    "X-CSRF-Token" = $CSRF
    "Idempotency-Key" = [guid]::NewGuid().ToString()
  }
}

Push-Location (Join-Path $PSScriptRoot "..")
$runID = New-LLMGatewayTestRunID -Purpose "core"
$postgres = $null
$valkey = $null
$gatewayProcess = $null
$providerProcess = $null
$environmentSnapshot = Save-LLMGatewayEnvironment
$testFailure = $null
$runningOnWindows = $env:OS -eq "Windows_NT"
$binaryName = if ($runningOnWindows) { "gateway.exe" } else { "gateway" }
$providerBinaryName = if ($runningOnWindows) { "fixture-provider.exe" } else { "fixture-provider" }
$buildDirectory = Join-Path (Get-Location) ".build\core-$runID"
$binaryPath = Join-Path $buildDirectory $binaryName
$providerBinaryPath = Join-Path $buildDirectory $providerBinaryName
$stdoutPath = Join-Path $buildDirectory "gateway.stdout.log"
$stderrPath = Join-Path $buildDirectory "gateway.stderr.log"
$providerStdoutPath = Join-Path $buildDirectory "provider.stdout.log"
$providerStderrPath = Join-Path $buildDirectory "provider.stderr.log"
$providerCertificatePath = Join-Path $buildDirectory "provider-ca.pem"

try {
  Clear-LLMGatewayEnvironment
  New-Item -ItemType Directory -Force $buildDirectory | Out-Null
  $postgres = Start-LLMGatewayTestPostgres -RunID $runID -DatabaseName "llmgateway_core" -Password "core-postgres-fixture"
  $docker = Get-LLMGatewayDockerCommand
  $quotaDatabaseName = "llmgateway_core_quota"
  & $docker exec $postgres.Container createdb -U llmgateway $quotaDatabaseName
  if ($LASTEXITCODE -ne 0) { throw "Could not create the isolated quota test database." }
  $quotaDatabaseURL = $postgres.DatabaseURL.Replace("/llmgateway_core?", "/$quotaDatabaseName`?")
  if ($quotaDatabaseURL -eq $postgres.DatabaseURL) { throw "Could not derive the quota test database URL." }

  $valkeyPassword = "core-valkey-fixture"
  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $valkeyPassword
  $gatewayPort = Get-LLMGatewayFreeLoopbackPort
  $providerPort = Get-LLMGatewayFreeLoopbackPort
  $providerAdminPort = Get-LLMGatewayFreeLoopbackPort
  $baseURL = "http://127.0.0.1:$gatewayPort"
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
  $env:LLMGATEWAY_SESSION_PEPPER = "llmgateway-core-session-pepper-00000"
  $env:LLMGATEWAY_API_KEY_PEPPER = "llmgateway-core-api-key-pepper-00000"
  $env:LLMGATEWAY_COOKIE_SECURE = "false"
  $env:LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS = "198.18.0.0/15"
  $env:LLMGATEWAY_PROVIDER_CA_BUNDLE_FILE = $providerCertificatePath
  $env:LLMGATEWAY_REQUEST_MAX_ACTIVE = "2"
  $env:LLMGATEWAY_REQUEST_MAX_ACTIVE_PER_USER = "2"
  $env:LLMGATEWAY_REQUEST_MAX_QUEUE_WAIT = "5s"
  $env:LLMGATEWAY_REQUEST_LEASE_TTL = "3s"
  $env:LLMGATEWAY_REQUEST_EXECUTION_HEARTBEAT_INTERVAL = "100ms"
  $env:LLMGATEWAY_REQUEST_EXECUTION_STALE_AFTER = "1s"
  $env:LLMGATEWAY_REQUEST_RECOVERY_INTERVAL = "200ms"
  $env:LLMGATEWAY_TEST_DATABASE_URL = $quotaDatabaseURL
  $env:LLMGATEWAY_TEST_VALKEY_ADDRESS = $valkey.Address
  $env:LLMGATEWAY_TEST_VALKEY_PASSWORD = $valkeyPassword
  $env:LLMGATEWAY_TEST_VALKEY_DATABASE = "1"
  $env:LLMGATEWAY_TEST_VALKEY_REQUIRED = "true"

  & go test ./internal/quota -run '^TestPersistentSubscriptionQuotaIsIdempotentAndAtomic$' -count=1
  if ($LASTEXITCODE -ne 0) { throw "Persistent quota integration test failed." }
  & go test ./internal/store -run '^TestRequestExecutionClaimFencesStaleWritersAndRecoveryHoldsReservation$' -count=1
  if ($LASTEXITCODE -ne 0) { throw "Request execution fencing integration test failed." }
  & go test ./internal/coordination -run '^TestValkey' -count=1
  if ($LASTEXITCODE -ne 0) { throw "Valkey coordination integration tests failed." }

  & go build -trimpath -o $binaryPath .\cmd\gateway
  if ($LASTEXITCODE -ne 0) { throw "Could not build gateway for core test." }
  & go build -trimpath -o $providerBinaryPath .\scripts\fixtures\provider
  if ($LASTEXITCODE -ne 0) { throw "Could not build the controlled Provider fixture." }

  $providerStartArguments = @{
    FilePath = $providerBinaryPath
    ArgumentList = @(
      "-address", "127.0.0.1:$providerPort",
      "-admin-address", "127.0.0.1:$providerAdminPort",
      "-certificate-out", $providerCertificatePath,
      "-certificate-ip", "127.0.0.1"
    )
    PassThru = $true
    RedirectStandardOutput = $providerStdoutPath
    RedirectStandardError = $providerStderrPath
  }
  if ($runningOnWindows) { $providerStartArguments.WindowStyle = "Hidden" }
  $providerProcess = Start-Process @providerStartArguments
  $providerDeadline = (Get-Date).AddSeconds(30)
  do {
    if ($providerProcess.HasExited) { throw "Controlled Provider fixture exited before readiness." }
    try {
      $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats" -TimeoutSec 2
      if ($providerStats.held -eq 0 -and (Test-Path $providerCertificatePath)) { break }
    } catch {
      Start-Sleep -Milliseconds 100
    }
  } while ((Get-Date) -lt $providerDeadline)
  if (-not (Test-Path $providerCertificatePath)) { throw "Controlled Provider fixture did not become ready." }

  $gatewayStartArguments = @{
    FilePath = $binaryPath
    PassThru = $true
    RedirectStandardOutput = $stdoutPath
    RedirectStandardError = $stderrPath
  }
  if ($runningOnWindows) { $gatewayStartArguments.WindowStyle = "Hidden" }
  $gatewayProcess = Start-Process @gatewayStartArguments
  Wait-LLMGatewayReady -Process $gatewayProcess -BaseURL $baseURL
  Set-ProviderFixtureCatalog -Docker $docker -Container $postgres.Container -ProviderBaseURL $providerBaseURL

  $adminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $setupResponse = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/api/control/setup" -WebSession $adminSession `
    -ContentType "application/json" -Body (@{ email = "owner@example.test" } | ConvertTo-Json)
  $setup = $setupResponse.Content | ConvertFrom-Json
  $adminCSRF = [string]$setup.data.csrfToken
  $initialAdministratorPassword = [string]$setup.data.initialPassword
  if ($setup.data.role -ne "administrator" -or [string]::IsNullOrWhiteSpace($initialAdministratorPassword) -or
      [string]$setupResponse.Headers["Cache-Control"] -ne "no-store") {
    throw "Setup did not establish a no-store administrator session with one-time credentials."
  }

  $otherAdminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $otherAdmin = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/session" -WebSession $otherAdminSession `
    -ContentType "application/json" -Body (@{ email = "owner@example.test"; password = $initialAdministratorPassword } | ConvertTo-Json)
  if ($otherAdmin.data.role -ne "administrator") { throw "Initial administrator password did not authenticate." }
  $replacementAdministratorPassword = "core-administrator-replacement-password"
  $passwordChange = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/password" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" `
    -Body (@{ currentPassword = $initialAdministratorPassword; replacementPassword = $replacementAdministratorPassword } | ConvertTo-Json)
  if ([int]$passwordChange.data.revokedSessions -ne 1) { throw "Administrator password replacement did not revoke the second session." }
  Assert-HTTPFailureStatus -ExpectedStatus 401 -FailureMessage "Old administrator password remained valid." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/session" -ContentType "application/json" `
      -Body (@{ email = "owner@example.test"; password = $initialAdministratorPassword } | ConvertTo-Json)
  }
  Assert-HTTPFailureStatus -ExpectedStatus 401 -FailureMessage "The replaced administrator session remained valid." -Action {
    Invoke-RestMethod -Uri "$baseURL/api/control/session" -WebSession $otherAdminSession
  }
  $initialAdministratorPassword = $null

  $providers = Invoke-RestMethod -Uri "$baseURL/api/control/providers" -WebSession $adminSession
  $provider = @($providers.data | Where-Object { $_.catalog_id -eq "siliconflow" }) | Select-Object -First 1
  $models = Invoke-RestMethod -Uri "$baseURL/api/control/models" -WebSession $adminSession
  $model = @($models.data | Where-Object { $_.provider_id -eq $provider.id }) | Select-Object -First 1
  if ($null -eq $provider -or $null -eq $model) { throw "The code-owned Provider and model catalog was not available." }

  $pool = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/resource-pools" -WebSession $adminSession `
    -Headers (New-MutationHeaders -CSRF $adminCSRF) -ContentType "application/json" `
    -Body (@{ providerId = $provider.id; slug = "core-pool"; name = "Core Pool"; modelIds = @($model.id) } | ConvertTo-Json -Depth 5)
  if ($pool.data.slug -ne "core-pool" -or $pool.data.status -ne "active") { throw "Resource pool creation did not become live." }

  $credentialSecret = "core-upstream-secret"
  $credential = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/credentials" -WebSession $adminSession `
    -Headers (New-MutationHeaders -CSRF $adminCSRF) -ContentType "application/json" `
    -Body (@{ resourcePoolId = $pool.data.id; name = "Core Upstream Key"; secret = $credentialSecret; modelBindings = @(@{ model_id = $model.id; priority = 10; weight = 100 }); rpmLimit = 60; tpmLimit = 50000; concurrencyLimit = 2 } | ConvertTo-Json -Depth 6)
  if ($credential.data.status -ne "active" -or $credential.data.model_bindings.Count -ne 1) { throw "Upstream API Key creation did not persist routing eligibility." }
  $probe = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/credentials/$($credential.data.id)/probe" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" `
    -Body (@{ modelId = $model.id } | ConvertTo-Json)
  if ($probe.data.execution.status -ne "succeeded" -or [string]::IsNullOrWhiteSpace([string]$probe.data.execution.request_id)) {
    throw "Upstream API Key probe did not return a successful execution and Request ID."
  }

  $null = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/model-prices" -WebSession $adminSession `
    -Headers (New-MutationHeaders -CSRF $adminCSRF) -ContentType "application/json" `
    -Body (@{ modelId = $model.id; currency = "USD"; inputPricePerMillionTokens = "0.1"; outputPricePerMillionTokens = "0.2"; effectiveAt = (Get-Date).ToUniversalTime().AddMinutes(-1).ToString("o") } | ConvertTo-Json)

  $plan = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/plans" -WebSession $adminSession `
    -Headers (New-MutationHeaders -CSRF $adminCSRF) -ContentType "application/json" `
    -Body (@{ slug = "core-plan"; name = "Core Plan"; description = ""; kind = "token"; tokenQuota = 50000; validityDays = 30; concurrencyLimit = 2; rpmLimit = 60; tpmLimit = 50000; routes = @(@{ modelId = $model.id; resourcePoolId = $pool.data.id }) } | ConvertTo-Json -Depth 6)
  if ($plan.data.current_version.version -ne 1 -or $plan.data.current_version.routes.Count -ne 1) { throw "Plan publication did not create one immutable routed version." }

  $createdMember = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/members" -WebSession $adminSession `
    -Headers (New-MutationHeaders -CSRF $adminCSRF) -ContentType "application/json" `
    -Body (@{ email = "core-member@example.test"; displayName = "Core Member" } | ConvertTo-Json)
  $member = $createdMember.data.member
  $memberPassword = [string]$createdMember.data.initialPassword
  if ($member.status -ne "active" -or [string]::IsNullOrWhiteSpace($memberPassword)) { throw "Member creation did not return an active member and one-time password." }

  $subscription = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/subscriptions" -WebSession $adminSession `
    -Headers (New-MutationHeaders -CSRF $adminCSRF) -ContentType "application/json" `
    -Body (@{ userId = $member.id; servicePlanId = $plan.data.id; grantedTokens = 50000; startsAt = (Get-Date).ToUniversalTime().AddMinutes(-1).ToString("o"); expiresAt = (Get-Date).ToUniversalTime().AddDays(30).ToString("o"); notes = "" } | ConvertTo-Json)
  if ($subscription.data.status -ne "active" -or $subscription.data.service_plan_version_id -ne $plan.data.current_version.id) {
    throw "Subscription did not freeze the published plan version."
  }

  $memberSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $memberLogin = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/session" -WebSession $memberSession `
    -ContentType "application/json" -Body (@{ email = $member.email; password = $memberPassword } | ConvertTo-Json)
  $memberCSRF = [string]$memberLogin.data.csrfToken
  $memberPassword = $null
  Assert-HTTPFailureStatus -ExpectedStatus 403 -FailureMessage "A member read the administrator member collection." -Action {
    Invoke-RestMethod -Uri "$baseURL/api/control/members" -WebSession $memberSession
  }

  $createdKey = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/keys" -WebSession $memberSession `
    -Headers (New-MutationHeaders -CSRF $memberCSRF) -ContentType "application/json" `
    -Body (@{ ownerId = $member.id; name = "Core Member Key"; authorizedModelIds = @($model.id) } | ConvertTo-Json -Depth 5)
  $gatewayKeySecret = [string]$createdKey.data.secret
  if ($gatewayKeySecret -notmatch '^llmg_[A-Za-z0-9_-]+$') { throw "Member API key creation did not return the one-time secret." }

  $publicModels = Invoke-RestMethod -Uri "$baseURL/v1/models" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if (-not @($publicModels.data.id).Contains([string]$model.public_name)) { throw "Authorized model was absent from the public catalog." }
  $chatBody = @{ model = $model.public_name; messages = @(@{ role = "user"; content = "hello" }); stream = $false } | ConvertTo-Json -Depth 6
  $requestIdempotencyKey = [guid]::NewGuid().ToString()
  $chat = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/chat/completions" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $requestIdempotencyKey } `
    -ContentType "application/json" -Body $chatBody
  if ($chat.choices[0].message.content -ne "fixture response" -or [int]$chat.usage.total_tokens -ne 6) {
    throw "Public chat did not preserve the Provider response and six-Token usage."
  }
  Assert-HTTPFailureStatus -ExpectedStatus 409 -FailureMessage "An identical accepted request was dispatched again." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $requestIdempotencyKey } `
      -ContentType "application/json" -Body $chatBody
  }
  $conflictingChatBody = @{ model = $model.public_name; messages = @(@{ role = "user"; content = "different" }); stream = $false } | ConvertTo-Json -Depth 6
  Assert-HTTPFailureStatus -ExpectedStatus 409 -FailureMessage "A reused request idempotency key accepted different input." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $requestIdempotencyKey } `
      -ContentType "application/json" -Body $conflictingChatBody
  }

  Stop-Process -Id $gatewayProcess.Id -Force
  $null = $gatewayProcess.WaitForExit(5000)
  $gatewayProcess = Start-Process @gatewayStartArguments
  Wait-LLMGatewayReady -Process $gatewayProcess -BaseURL $baseURL
  Set-ProviderFixtureCatalog -Docker $docker -Container $postgres.Container -ProviderBaseURL $providerBaseURL
  $restartChat = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/chat/completions" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $chatBody
  if ([int]$restartChat.usage.total_tokens -ne 6) { throw "Gateway restart did not recover the subscription request path." }

  $credential = Invoke-RestMethod -Uri "$baseURL/api/control/credentials?includeRetired=true" -WebSession $adminSession
  $credential = @($credential.data | Where-Object { $_.name -eq "Core Upstream Key" }) | Select-Object -First 1
  $disabledCredential = Invoke-RestMethod -Method Put -Uri "$baseURL/api/control/credentials/$($credential.id)/status" -WebSession $adminSession `
    -Headers (New-MutationHeaders -CSRF $adminCSRF) -ContentType "application/json" `
    -Body (@{ status = "disabled"; expectedUpdatedAt = $credential.updated_at } | ConvertTo-Json)
  Assert-HTTPFailureStatus -ExpectedStatus 503 -FailureMessage "A request bypassed the disabled upstream API Key." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
      -ContentType "application/json" -Body $chatBody
  }
  $null = Invoke-RestMethod -Method Put -Uri "$baseURL/api/control/credentials/$($credential.id)/status" -WebSession $adminSession `
    -Headers (New-MutationHeaders -CSRF $adminCSRF) -ContentType "application/json" `
    -Body (@{ status = "active"; expectedUpdatedAt = $disabledCredential.data.updated_at } | ConvertTo-Json)

  $requestFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT request.status || '|' || request.input_tokens || '|' || request.output_tokens || '|' || reservation.state || '|' || reservation.charged_tokens || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'completed') FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE request.gateway_key_id = '$($createdKey.data.key.id)' AND request.idempotency_key = '$requestIdempotencyKey'"
  if ($LASTEXITCODE -ne 0 -or $requestFacts -ne "completed|4|2|settled|6|1") {
    throw "Request, attempt, and subscription ledger facts did not settle exactly once: $requestFacts"
  }
  $secretFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT (SELECT count(*) FROM credential_mutations WHERE result::text LIKE '%$credentialSecret%') || '|' || (SELECT count(*) FROM audit_events WHERE detail::text LIKE '%$credentialSecret%') || '|' || (SELECT count(*) FROM gateway_key_mutations WHERE result ? 'secret')"
  if ($LASTEXITCODE -ne 0 -or $secretFacts -ne "0|0|0") { throw "A one-time secret crossed a mutation or audit boundary: $secretFacts" }

  $revokedKey = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/keys/$($createdKey.data.key.id)/revoke" -WebSession $memberSession `
    -Headers @{ "X-CSRF-Token" = $memberCSRF }
  if ($revokedKey.data.status -ne "revoked") { throw "Member API key revocation did not become visible." }
  Assert-HTTPFailureStatus -ExpectedStatus 401 -FailureMessage "A revoked API key still read the public catalog." -Action {
    Invoke-RestMethod -Uri "$baseURL/v1/models" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  }

  $runtimeLogs = @(Get-ChildItem -LiteralPath $buildDirectory -File -Filter "*.log" | Select-Object -ExpandProperty FullName)
  if ($runtimeLogs.Count -gt 0 -and (Select-String -LiteralPath $runtimeLogs -SimpleMatch -Quiet -Pattern @($credentialSecret, $gatewayKeySecret))) {
    throw "A Provider or member API key secret appeared in a core runtime log."
  }
  $credentialSecret = $null
  $gatewayKeySecret = $null
  Invoke-RestMethod -Method Delete -Uri "$baseURL/api/control/session" -WebSession $memberSession -Headers @{ "X-CSRF-Token" = $memberCSRF } | Out-Null
  Invoke-RestMethod -Method Delete -Uri "$baseURL/api/control/session" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } | Out-Null
  $activeSessionCount = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND expires_at > now()"
  if ($LASTEXITCODE -ne 0 -or $activeSessionCount -ne "0") { throw "A core acceptance session remained active after logout." }
} catch {
  $testFailure = $_
  foreach ($logPath in @($stdoutPath, $stderrPath, $providerStdoutPath, $providerStderrPath)) {
    if (Test-Path $logPath) {
      try { Get-Content $logPath | Write-Host } catch { Write-Host "Could not read $logPath" }
    }
  }
} finally {
  $cleanupFailures = @()
  foreach ($item in @(
    @{ Name = "gateway"; Process = $gatewayProcess },
    @{ Name = "Provider fixture"; Process = $providerProcess }
  )) {
    try {
      if ($item.Process -and -not $item.Process.HasExited) {
        Stop-Process -Id $item.Process.Id -Force -ErrorAction SilentlyContinue
        $null = $item.Process.WaitForExit(5000)
      }
    } catch {
      $cleanupFailures += "$($item.Name) cleanup: $($_.Exception.Message)"
    }
  }
  try { Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot } catch { $cleanupFailures += "environment restore: $($_.Exception.Message)" }
  if ($null -ne $valkey) {
    try { Stop-LLMGatewayTestContainer -Container $valkey.Container -RunID $runID } catch { $cleanupFailures += "Valkey cleanup: $($_.Exception.Message)" }
  }
  if ($null -ne $postgres) {
    try { Stop-LLMGatewayTestContainer -Container $postgres.Container -RunID $runID } catch { $cleanupFailures += "PostgreSQL cleanup: $($_.Exception.Message)" }
  }
  try { Pop-Location } catch { $cleanupFailures += "location restore: $($_.Exception.Message)" }
  if ($null -ne $testFailure) {
    if ($cleanupFailures.Count -gt 0) { throw "Core gateway flow failed: $($testFailure.Exception.Message) Cleanup also failed: $($cleanupFailures -join '; ')" }
    throw "Core gateway flow failed: $($testFailure.Exception.Message) at $($testFailure.ScriptStackTrace)"
  }
  if ($cleanupFailures.Count -gt 0) { throw "Core gateway cleanup failed: $($cleanupFailures -join '; ')" }
}

Write-Host "Core subscription eligibility, public API idempotency, restart recovery, fail-closed routing, ledger, identity, and secret-boundary flow passed."
