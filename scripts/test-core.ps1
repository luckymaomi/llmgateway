$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Net.Http

. "$PSScriptRoot\isolated-services.ps1"

function Assert-HTTPFailureStatus {
  param(
    [Parameter(Mandatory = $true)][scriptblock] $Action,
    [Parameter(Mandatory = $true)][int] $ExpectedStatus,
    [Parameter(Mandatory = $true)][string] $FailureMessage,
    [switch] $RequireRetryAfter
  )

  try {
    & $Action | Out-Null
  } catch {
    $response = $_.Exception.Response
    if ($null -ne $response -and [int] $response.StatusCode -eq $ExpectedStatus) {
      if ($RequireRetryAfter) {
        $retryAfter = @($response.Headers.GetValues("Retry-After")) | Select-Object -First 1
        $retrySeconds = 0
        $retryAt = [DateTimeOffset]::MinValue
        $hasPositiveDelay = [int]::TryParse($retryAfter, [ref] $retrySeconds) -and $retrySeconds -gt 0
        $hasFutureTime = [DateTimeOffset]::TryParse($retryAfter, [ref] $retryAt) -and $retryAt -gt [DateTimeOffset]::UtcNow
        if (-not $hasPositiveDelay -and -not $hasFutureTime) {
          throw "$FailureMessage Retry-After was missing or did not identify a future recovery time."
        }
      }
      return
    }
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
    if ($Process.HasExited) {
      throw "Gateway exited before becoming ready."
    }
    try {
      $health = Invoke-RestMethod -Uri "$BaseURL/health/ready" -TimeoutSec 2
      if ($health.status -eq "ready") {
        return
      }
    } catch {
      Start-Sleep -Milliseconds 200
    }
  } while ((Get-Date) -lt $deadline)
  throw "Gateway did not become ready."
}

function Start-LLMGatewayChatCall {
  param(
    [Parameter(Mandatory = $true)][string] $BaseURL,
    [Parameter(Mandatory = $true)][string] $GatewayKey,
    [Parameter(Mandatory = $true)][string] $IdempotencyKey,
    [Parameter(Mandatory = $true)][string] $Body
  )

  $client = [System.Net.Http.HttpClient]::new()
  $client.Timeout = [TimeSpan]::FromSeconds(15)
  $source = [System.Threading.CancellationTokenSource]::new()
  $request = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Post, "$BaseURL/v1/chat/completions")
  $request.Headers.Add("Authorization", "Bearer $GatewayKey")
  $request.Headers.Add("Idempotency-Key", $IdempotencyKey)
  $request.Content = [System.Net.Http.StringContent]::new($Body, [System.Text.Encoding]::UTF8, "application/json")
  [pscustomobject]@{
    Client  = $client
    Source  = $source
    Request = $request
    Task    = $client.SendAsync($request, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead, $source.Token)
  }
}

function Close-LLMGatewayChatCall {
  param(
    [Parameter(Mandatory = $true)] $Call,
    [switch] $Cancel
  )

  if ($Cancel) {
    $Call.Source.Cancel()
  }
  if ($Call.Task.IsCompleted) {
    try {
      $response = $Call.Task.GetAwaiter().GetResult()
      $response.Dispose()
    } catch {
      if (-not $Cancel) {
        throw
      }
    }
  }
  $Call.Request.Dispose()
  $Call.Client.Dispose()
  $Call.Source.Dispose()
}

Push-Location (Join-Path $PSScriptRoot "..")
$runID = New-LLMGatewayTestRunID -Purpose "core"
$postgres = $null
$valkey = $null
$process = $null
$secondProcess = $null
$providerProcess = $null
$valkeyPaused = $false
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
$secondStdoutPath = Join-Path $buildDirectory "gateway-second.stdout.log"
$secondStderrPath = Join-Path $buildDirectory "gateway-second.stderr.log"
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
  if ($LASTEXITCODE -ne 0) {
    throw "Could not create the isolated quota test database."
  }
  $quotaDatabaseURL = $postgres.DatabaseURL.Replace("/llmgateway_core?", "/$quotaDatabaseName`?")
  if ($quotaDatabaseURL -eq $postgres.DatabaseURL) {
    throw "Could not derive the isolated quota test database URL."
  }
  $valkeyPassword = "core-valkey-fixture"
  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $valkeyPassword
  $port = Get-LLMGatewayFreeLoopbackPort
  $baseURL = "http://127.0.0.1:$port"
  $providerHost = "127.0.0.1"
  $providerPort = Get-LLMGatewayFreeLoopbackPort
  $providerAdminPort = Get-LLMGatewayFreeLoopbackPort
  $providerBaseURL = "https://${providerHost}:$providerPort/v1"
  $providerAdminURL = "http://127.0.0.1:$providerAdminPort"

  $env:LLMGATEWAY_PROFILE = "test"
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$port"
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
  $env:LLMGATEWAY_PROVIDER_CA_BUNDLE_FILE = $providerCertificatePath
  $env:LLMGATEWAY_REQUEST_MAX_ACTIVE = "2"
  $env:LLMGATEWAY_REQUEST_MAX_ACTIVE_PER_USER = "2"
  $env:LLMGATEWAY_REQUEST_MAX_QUEUE_WAIT = "10s"
  $env:LLMGATEWAY_REQUEST_LEASE_TTL = "3s"
  $env:LLMGATEWAY_REQUEST_EXECUTION_HEARTBEAT_INTERVAL = "100ms"
  $env:LLMGATEWAY_REQUEST_EXECUTION_STALE_AFTER = "1s"
  $env:LLMGATEWAY_REQUEST_RECOVERY_INTERVAL = "200ms"
  $env:LLMGATEWAY_RESPONSES_POLL_INTERVAL = "50ms"
  $env:LLMGATEWAY_RESPONSES_HEARTBEAT_INTERVAL = "100ms"
  $env:LLMGATEWAY_RESPONSES_STALE_AFTER = "1s"
  $env:LLMGATEWAY_TEST_DATABASE_URL = $quotaDatabaseURL
  $env:LLMGATEWAY_TEST_VALKEY_ADDRESS = $valkey.Address
  $env:LLMGATEWAY_TEST_VALKEY_PASSWORD = $valkeyPassword
  $env:LLMGATEWAY_TEST_VALKEY_DATABASE = "1"
  $env:LLMGATEWAY_TEST_VALKEY_REQUIRED = "true"

  & go test ./internal/quota -run '^TestPersistentQuotaLifecycleAndConcurrentReservations$' -count=1
  if ($LASTEXITCODE -ne 0) {
    throw "Persistent quota integration test failed."
  }
  & go test ./internal/store -run '^TestRequestExecutionClaimFencesStaleWritersAndRecoveryHoldsReservation$' -count=1 -v
  if ($LASTEXITCODE -ne 0) {
    throw "Request execution fencing integration test failed."
  }
  & go test ./internal/coordination -run '^TestValkey' -count=1
  if ($LASTEXITCODE -ne 0) {
    throw "Valkey coordination integration tests failed."
  }

  & go build -trimpath -o $binaryPath .\cmd\gateway
  if ($LASTEXITCODE -ne 0) {
    throw "Could not build gateway for core test."
  }
  & go build -trimpath -o $providerBinaryPath .\scripts\fixtures\provider
  if ($LASTEXITCODE -ne 0) {
    throw "Could not build the controlled Provider fixture."
  }
  $providerStartArguments = @{
    FilePath               = $providerBinaryPath
    ArgumentList           = @(
      "-address", "${providerHost}:$providerPort",
      "-admin-address", "127.0.0.1:$providerAdminPort",
      "-certificate-out", $providerCertificatePath,
      "-certificate-ip", $providerHost
    )
    PassThru               = $true
    RedirectStandardOutput = $providerStdoutPath
    RedirectStandardError  = $providerStderrPath
  }
  if ($runningOnWindows) {
    $providerStartArguments.WindowStyle = "Hidden"
  }
  $providerProcess = Start-Process @providerStartArguments
  $providerReady = $false
  $providerDeadline = (Get-Date).AddSeconds(30)
  do {
    if ($providerProcess.HasExited) {
      throw "Controlled Provider fixture exited before becoming ready."
    }
    try {
      $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats" -TimeoutSec 2
      $providerReady = $providerStats.held -eq 0
    } catch {
      Start-Sleep -Milliseconds 100
    }
  } while (-not $providerReady -and (Get-Date) -lt $providerDeadline)
  if (-not $providerReady -or -not (Test-Path $providerCertificatePath)) {
    throw "Controlled Provider fixture did not become ready."
  }
  $startArguments = @{
    FilePath               = $binaryPath
    PassThru               = $true
    RedirectStandardOutput = $stdoutPath
    RedirectStandardError  = $stderrPath
  }
  if ($runningOnWindows) {
    $startArguments.WindowStyle = "Hidden"
  }
  $process = Start-Process @startArguments
  Wait-LLMGatewayReady -Process $process -BaseURL $baseURL

  $setupStatus = Invoke-RestMethod -Uri "$baseURL/api/control/setup/status"
  if (-not $setupStatus.data.required) {
    throw "Fresh isolated storage did not require administrator setup."
  }

  $adminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $bootstrapBody = @{
    email = "owner@example.test"
  } | ConvertTo-Json
  $bootstrapResponse = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/api/control/setup" -WebSession $adminSession -ContentType "application/json" -Body $bootstrapBody
  $bootstrap = $bootstrapResponse.Content | ConvertFrom-Json
  $initialAdministratorPassword = [string]$bootstrap.data.initialPassword
  if ($bootstrap.data.role -ne "administrator" -or -not $bootstrap.data.csrfToken -or
      [string]::IsNullOrWhiteSpace($initialAdministratorPassword) -or
      [string]$bootstrapResponse.Headers["Cache-Control"] -ne "no-store") {
    throw "Setup did not establish a no-store administrator session with one-time credentials."
  }
  $adminCSRF = $bootstrap.data.csrfToken
  $bootstrap.data.initialPassword = $null

  $adminCurrent = Invoke-RestMethod -Uri "$baseURL/api/control/session" -WebSession $adminSession
  if ($adminCurrent.data.userId -ne $bootstrap.data.userId) {
    throw "Administrator session was not readable after setup."
  }

  $secondaryAdministratorSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $administratorLoginBody = @{ email = "owner@example.test"; password = $initialAdministratorPassword } | ConvertTo-Json
  $secondaryAdministrator = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/session" -WebSession $secondaryAdministratorSession `
    -ContentType "application/json" -Body $administratorLoginBody
  if ($secondaryAdministrator.data.role -ne "administrator") {
    throw "The generated initial administrator password did not authenticate."
  }
  $replacementAdministratorPassword = "core-administrator-replacement-password"
  $passwordChangeBody = @{
    currentPassword = $initialAdministratorPassword
    replacementPassword = $replacementAdministratorPassword
  } | ConvertTo-Json
  $passwordChange = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/password" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $passwordChangeBody
  if ([int]$passwordChange.data.revokedSessions -ne 1) {
    throw "Administrator password change did not revoke the other active session."
  }
  $passwordChangeBody = $null
  $administratorLoginBody = $null
  Assert-HTTPFailureStatus -ExpectedStatus 401 -FailureMessage "The generated administrator password remained valid after replacement." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/session" -ContentType "application/json" `
      -Body (@{ email = "owner@example.test"; password = $initialAdministratorPassword } | ConvertTo-Json)
  }
  Assert-HTTPFailureStatus -ExpectedStatus 401 -FailureMessage "Password change did not revoke the other administrator session." -Action {
    Invoke-RestMethod -Uri "$baseURL/api/control/session" -WebSession $secondaryAdministratorSession
  }
  $replacementSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $replacementLogin = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/session" -WebSession $replacementSession `
    -ContentType "application/json" -Body (@{ email = "owner@example.test"; password = $replacementAdministratorPassword } | ConvertTo-Json)
  if ($replacementLogin.data.role -ne "administrator") {
    throw "The replacement administrator password did not authenticate."
  }
  Invoke-RestMethod -Method Delete -Uri "$baseURL/api/control/session" -WebSession $replacementSession `
    -Headers @{ "X-CSRF-Token" = $replacementLogin.data.csrfToken } | Out-Null
  $initialAdministratorPassword = $null
  $replacementAdministratorPassword = $null

  $providerBody = @{
    slug    = "core-openai"
    name    = "Core OpenAI-compatible"
    kind    = "openai-compatible"
    baseUrl = $providerBaseURL
  } | ConvertTo-Json
  $provider = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/providers" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $providerBody
  if ($provider.data.slug -ne "core-openai") {
    throw "Provider creation did not persist the requested slug."
  }

  $modelBody = @{
    providerId      = $provider.data.id
    alias           = "core-chat"
    upstreamModelId = "fixture-chat"
    resourceDomain  = "free"
    capabilities    = @("streaming")
    contextTokens   = 8192
  } | ConvertTo-Json
  $model = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/models" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $modelBody
  if ($model.data.alias -ne "core-chat") {
    throw "Model creation did not persist the public alias."
  }
  $ungrantedModelBody = @{
    providerId      = $provider.data.id
    alias           = "core-not-granted"
    upstreamModelId = "fixture-not-granted"
    resourceDomain  = "free"
    capabilities    = @("streaming")
    contextTokens   = 8192
  } | ConvertTo-Json
  $ungrantedModel = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/models" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $ungrantedModelBody
  if ($ungrantedModel.data.alias -ne "core-not-granted") {
    throw "Second model creation did not persist the public alias."
  }
  $ungrantedPriceBody = @{
    modelId = $ungrantedModel.data.id; currency = "USD"; inputPricePerMillionTokens = "0"; outputPricePerMillionTokens = "0"
    effectiveAt = (Get-Date).ToUniversalTime().AddMinutes(-1).ToString("o")
  } | ConvertTo-Json
  $null = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/model-prices" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $ungrantedPriceBody

  $credentialBody = @{
    providerId       = $provider.data.id
    label            = "Core fixture credential"
    secret           = "core-upstream-secret"
    resourceDomain   = "free"
    modelBindings     = @(
      @{ modelId = $model.data.id; priority = 10; weight = 70 },
      @{ modelId = $ungrantedModel.data.id; priority = 20; weight = 30 }
    )
    rpmLimit         = 60
    tpmLimit         = 100000
    concurrencyLimit = 2
  } | ConvertTo-Json -Depth 5
  $credentialIdempotencyKey = [guid]::NewGuid().ToString()
  $credential = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/credentials" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $credentialIdempotencyKey } `
    -ContentType "application/json" -Body $credentialBody
  $credentialBindings = @($credential.data.modelBindings | Sort-Object priority)
  if ($credentialBindings.Count -ne 2 -or
      $credentialBindings[0].modelName -ne "core-chat" -or
      $credentialBindings[0].priority -ne 10 -or
      $credentialBindings[0].weight -ne 70 -or
      $credentialBindings[1].modelName -ne "core-not-granted" -or
      $credentialBindings[1].priority -ne 20 -or
      $credentialBindings[1].weight -ne 30) {
    throw "Credential creation did not atomically persist both model routing bindings."
  }
  $credentialReplay = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/credentials" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $credentialIdempotencyKey } `
    -ContentType "application/json" -Body $credentialBody
  if ($credentialReplay.data.id -ne $credential.data.id) {
    throw "Credential replay did not return the original credential fact."
  }
  $conflictingCredentialBody = @{
    providerId       = $provider.data.id
    label            = "Different credential input"
    secret           = "core-upstream-secret"
    resourceDomain   = "free"
    modelBindings     = @(
      @{ modelId = $model.data.id; priority = 10; weight = 70 },
      @{ modelId = $ungrantedModel.data.id; priority = 20; weight = 30 }
    )
    rpmLimit         = 60
    tpmLimit         = 100000
    concurrencyLimit = 2
  } | ConvertTo-Json -Depth 5
  Assert-HTTPFailureStatus -ExpectedStatus 409 -FailureMessage "A reused credential idempotency key accepted different input." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/credentials" -WebSession $adminSession `
      -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $credentialIdempotencyKey } `
      -ContentType "application/json" -Body $conflictingCredentialBody
  }

  $enableProviderBody = @{
    enabled           = $true
    expectedUpdatedAt = $provider.data.updatedAt
  } | ConvertTo-Json
  $enabledProvider = Invoke-RestMethod -Method Put -Uri "$baseURL/api/control/providers/$($provider.data.id)/status" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $enableProviderBody
  if ($enabledProvider.data.status -ne "enabled") {
    throw "Provider activation did not become visible before configuration capture."
  }

  $credentialFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT (SELECT count(*) FROM provider_credentials WHERE id = '$($credential.data.id)') || '|' || (SELECT count(*) FROM credential_mutations WHERE credential_id = '$($credential.data.id)') || '|' || (SELECT count(*) FROM credential_models WHERE credential_id = '$($credential.data.id)') || '|' || (SELECT count(*) FROM audit_events WHERE action = 'credential.created' AND target_id = '$($credential.data.id)') || '|' || (SELECT string_agg(priority::text || ':' || weight::text, ',' ORDER BY priority) FROM credential_models WHERE credential_id = '$($credential.data.id)')"
  if ($LASTEXITCODE -ne 0 -or $credentialFacts -ne "1|1|2|1|10:70,20:30") {
    throw "Credential mutation facts were not atomic and singular: $credentialFacts"
  }
  $eligibleCredentialCount = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT count(*) FROM provider_credentials c JOIN providers p ON p.id = c.provider_id WHERE p.enabled AND c.status <> 'disabled' AND EXISTS (SELECT 1 FROM credential_models cm JOIN models m ON m.id = cm.model_id WHERE cm.credential_id = c.id AND m.enabled AND m.provider_id = c.provider_id AND m.resource_domain = c.resource_domain)"
  if ($LASTEXITCODE -ne 0 -or $eligibleCredentialCount -ne "1") {
    throw "The registry did not expose exactly one capture-eligible credential: $eligibleCredentialCount"
  }

  $activeBeforePublish = Invoke-RestMethod -Uri "$baseURL/api/control/configuration/active" -WebSession $adminSession
  if ($activeBeforePublish.data.version -ne 0 -or $null -ne $activeBeforePublish.data.revisionId -or $activeBeforePublish.data.models.Count -ne 0) {
    throw "Fresh storage exposed an active configuration before publication."
  }

  $captureIdempotencyKey = [guid]::NewGuid().ToString()
  $revision = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/configuration/revisions" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $captureIdempotencyKey }
  if (-not $revision.data.id -or
      $revision.data.createdBy -ne "Administrator" -or
      $revision.data.providerCount -ne 1 -or
      $revision.data.modelCount -ne 2 -or
      $revision.data.credentialCount -ne 1 -or
      $revision.data.routeCount -ne 2) {
    throw "Configuration capture counts were provider=$($revision.data.providerCount), model=$($revision.data.modelCount), credential=$($revision.data.credentialCount), route=$($revision.data.routeCount), while eligible credentials=$eligibleCredentialCount."
  }
  $revisionReplay = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/configuration/revisions" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $captureIdempotencyKey }
  if ($revisionReplay.data.id -ne $revision.data.id -or $revisionReplay.data.createdBy -ne "Administrator") {
    throw "Configuration capture replay did not return the original revision."
  }

  $publishIdempotencyKey = [guid]::NewGuid().ToString()
  $publishBody = @{ expectedActiveVersion = [int64]$activeBeforePublish.data.version } | ConvertTo-Json
  $published = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/configuration/revisions/$($revision.data.id)/publish" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $publishIdempotencyKey } `
    -ContentType "application/json" -Body $publishBody
  if ($published.data.phase -ne "completed" -or
      $published.data.result.id -ne $revision.data.id -or
      $published.data.result.createdBy -ne "Administrator") {
    throw "Configuration publication did not complete for the captured revision."
  }

  $activeConfiguration = Invoke-RestMethod -Uri "$baseURL/api/control/configuration/active" -WebSession $adminSession
  $activeAliases = @($activeConfiguration.data.models | ForEach-Object { $_.alias })
  if ($activeConfiguration.data.version -ne 1 -or
      $activeConfiguration.data.revisionId -ne $revision.data.id -or
      $activeAliases.Count -ne 2 -or
      $activeAliases -notcontains "core-chat" -or
      $activeAliases -notcontains "core-not-granted") {
    throw "Published configuration was not readable as the complete active catalog."
  }

  $configurationFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT (SELECT count(*) FROM config_revision_providers WHERE revision_id = '$($revision.data.id)') || '|' || (SELECT count(*) FROM config_revision_models WHERE revision_id = '$($revision.data.id)') || '|' || (SELECT count(*) FROM config_revision_credentials WHERE revision_id = '$($revision.data.id)') || '|' || (SELECT count(*) FROM config_revision_routes WHERE revision_id = '$($revision.data.id)') || '|' || (SELECT version FROM active_config WHERE singleton = true AND revision_id = '$($revision.data.id)') || '|' || (SELECT count(*) FROM config_mutations WHERE revision_id = '$($revision.data.id)') || '|' || (SELECT string_agg(priority::text || ':' || weight::text, ',' ORDER BY priority) FROM config_revision_routes WHERE revision_id = '$($revision.data.id)')"
  if ($LASTEXITCODE -ne 0 -or $configurationFacts -ne "1|2|1|2|1|2|10:70,20:30") {
    throw "Configuration capture and publication facts were not atomic and singular: $configurationFacts"
  }

  $invitationBody = @{
    expiresAt = (Get-Date).ToUniversalTime().AddHours(24).ToString("o")
  } | ConvertTo-Json
  # Windows PowerShell persists -Headers into WebRequestSession, so remove the prior mutation key before proving the missing-header contract.
  $adminSession.Headers.Remove("Idempotency-Key") | Out-Null
  Assert-HTTPFailureStatus -ExpectedStatus 400 -FailureMessage "Invitation creation accepted a missing Idempotency-Key." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/invitations" -WebSession $adminSession `
      -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $invitationBody
  }

  $invitationIdempotencyKey = [guid]::NewGuid().ToString()
  $invitation = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/invitations" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $invitationIdempotencyKey } `
    -ContentType "application/json" -Body $invitationBody
  $invitationRecord = $invitation.data.invitation
  $invitationCode = [string]$invitation.data.code
  if ($invitationRecord.status -ne "issued" -or
      -not $invitationRecord.id -or
      $invitationCode.Length -lt 27 -or
      -not $invitationCode.StartsWith("invite_")) {
    throw "Invitation creation did not return its one-time code."
  }
  $expectedInvitationPrefix = $invitationCode.Substring(0, 13)
  if ($invitationRecord.codePrefix -ne $expectedInvitationPrefix -or $invitationRecord.createdBy -ne "Administrator") {
    throw "Invitation creation did not return its stable display prefix."
  }

  $invitationReplay = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/invitations" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $invitationIdempotencyKey } `
    -ContentType "application/json" -Body $invitationBody
  if ($invitationReplay.data.invitation.id -ne $invitationRecord.id -or
      $invitationReplay.data.code -ne $invitationCode) {
    throw "Invitation replay did not recover the original invitation and code."
  }

  $conflictingInvitationBody = @{
    expiresAt = (Get-Date).ToUniversalTime().AddHours(25).ToString("o")
  } | ConvertTo-Json
  Assert-HTTPFailureStatus -ExpectedStatus 409 -FailureMessage "A reused invitation idempotency key accepted different input." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/invitations" -WebSession $adminSession `
      -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $invitationIdempotencyKey } `
      -ContentType "application/json" -Body $conflictingInvitationBody
  }

  $invitationFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT (SELECT count(*) FROM invitations WHERE id = '$($invitationRecord.id)') || '|' || (SELECT count(*) FROM invitation_mutations WHERE actor_user_id = '$($bootstrap.data.userId)' AND idempotency_key = '$invitationIdempotencyKey' AND invitation_id = '$($invitationRecord.id)') || '|' || (SELECT count(*) FROM audit_events WHERE action = 'invitation.created' AND target_id = '$($invitationRecord.id)') || '|' || (SELECT count(*) FROM invitation_mutations mutation WHERE mutation.invitation_id = '$($invitationRecord.id)' AND position('$invitationCode' IN mutation.result::text) > 0) || '|' || (SELECT count(*) FROM audit_events audit WHERE audit.action = 'invitation.created' AND audit.target_id = '$($invitationRecord.id)' AND position('$invitationCode' IN audit.detail::text) > 0)"
  if ($LASTEXITCODE -ne 0 -or $invitationFacts -ne "1|1|1|0|0") {
    throw "Invitation mutation facts were not atomic, singular, and secret-free: $invitationFacts"
  }

  $listedInvitations = Invoke-RestMethod -Uri "$baseURL/api/control/invitations?page=1&pageSize=20" -WebSession $adminSession
  $listedInvitation = $listedInvitations.data.items | Where-Object { $_.id -eq $invitationRecord.id } | Select-Object -First 1
  if (-not $listedInvitation -or
      $listedInvitation.codePrefix -ne $expectedInvitationPrefix -or
      $listedInvitation.createdBy -ne "Administrator" -or
      ($listedInvitation.PSObject.Properties.Name -contains "code")) {
    throw "Invitation list did not retain only the stable, non-secret prefix."
  }

  $memberSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $registerBody = @{
    invitation  = $invitationCode
    email       = "member@example.test"
    displayName = "Member"
    password    = "correct horse battery staple"
  } | ConvertTo-Json
  $registration = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/registrations" -WebSession $memberSession -ContentType "application/json" -Body $registerBody
  $registrationLogs = @(Get-ChildItem -LiteralPath $buildDirectory -File -Filter "*.log" | Select-Object -ExpandProperty FullName)
  if ($registrationLogs.Count -gt 0 -and
      (Select-String -LiteralPath $registrationLogs -SimpleMatch -Quiet -Pattern $invitationCode)) {
    throw "An invitation secret appeared in a core runtime log."
  }
  $invitationCode = $null
  $invitation = $null
  $invitationReplay = $null
  $registerBody = $null
  if ($registration.data.status -ne "pending_review") {
    throw "Registration did not enter pending review."
  }

  $loginBody = @{ email = "member@example.test"; password = "correct horse battery staple" } | ConvertTo-Json
  Assert-HTTPFailureStatus -ExpectedStatus 403 -FailureMessage "A pending member unexpectedly established a session." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/session" -WebSession $memberSession -ContentType "application/json" -Body $loginBody
  }

  $users = Invoke-RestMethod -Uri "$baseURL/api/control/users?status=pending_review&page=1&pageSize=20" -WebSession $adminSession
  $member = $users.data.items | Where-Object { $_.email -eq "member@example.test" } | Select-Object -First 1
  if (-not $member -or $member.id -ne $registration.data.userId) {
    throw "Pending member was not visible to the administrator."
  }
  $approvalBody = @{ decision = "approve" } | ConvertTo-Json
  $approved = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/users/$($member.id)/review" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $approvalBody
  if ($approved.data.status -ne "active") {
    throw "Member approval did not become active."
  }

  $entitlementBody = @{
    ownerId          = $member.id
    planKind         = "token"
    resourceDomain   = "free"
    modelId          = $model.data.id
    grantedTokens    = 50000
    concurrencyLimit = 1
    rpmLimit         = 60
    tpmLimit         = 50000
    startsAt         = (Get-Date).ToUniversalTime().AddMinutes(-1).ToString("o")
    expiresAt        = (Get-Date).ToUniversalTime().AddDays(30).ToString("o")
    reason           = "Core request workflow acceptance"
  } | ConvertTo-Json
  $entitlement = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/entitlements" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $entitlementBody
  if ($entitlement.data.ownerId -ne $member.id -or
      $entitlement.data.modelId -ne $model.data.id -or
      $entitlement.data.balanceTokens -ne 50000) {
    throw "Entitlement creation did not expose the requested quota facts."
  }

  $keyBody = @{
    ownerId           = $member.id
    name              = "Core test"
    authorizedModelIds = @($model.data.id)
    expiresAt         = $null
  } | ConvertTo-Json
  $keyIdempotencyKey = [guid]::NewGuid().ToString()
  $createdKey = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/keys" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $keyIdempotencyKey } `
    -ContentType "application/json" -Body $keyBody
  if (-not $createdKey.data.secret.StartsWith("llmg_") -or
      $createdKey.data.key.ownerId -ne $member.id -or
      $createdKey.data.key.authorizedModelIds.Count -ne 1 -or
      $createdKey.data.key.authorizedModelIds[0] -ne $model.data.id -or
      $createdKey.data.key.authorizedModels.Count -ne 1 -or
      $createdKey.data.key.authorizedModels[0] -ne "core-chat") {
    throw "Administrator key creation did not bind the reviewed member to the selected published model."
  }
  $gatewayKeySecret = $createdKey.data.secret
  $otherKeyBody = @{
    ownerId           = $member.id
    name              = "Core response isolation"
    authorizedModelIds = @($model.data.id)
    expiresAt         = $null
  } | ConvertTo-Json
  $otherKey = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/keys" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $otherKeyBody
  $otherGatewayKeySecret = $otherKey.data.secret
  $gatewayKeyFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT (SELECT count(*) FROM gateway_keys WHERE id = '$($createdKey.data.key.id)' AND user_id = '$($member.id)') || '|' || (SELECT count(*) FROM gateway_key_mutations WHERE gateway_key_id = '$($createdKey.data.key.id)') || '|' || (SELECT count(*) FROM gateway_key_models WHERE gateway_key_id = '$($createdKey.data.key.id)') || '|' || (SELECT count(*) FROM audit_events WHERE action = 'gateway_key.created' AND target_id = '$($createdKey.data.key.id)')"
  if ($LASTEXITCODE -ne 0 -or $gatewayKeyFacts -ne "1|1|1|1") {
    throw "Gateway key mutation facts were not atomic and singular: $gatewayKeyFacts"
  }

  $login = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/session" -WebSession $memberSession -ContentType "application/json" -Body $loginBody
  if ($login.data.role -ne "member" -or -not $login.data.csrfToken -or $login.data.capabilities -notcontains "ledger:read") {
    throw "Approved member could not establish a session."
  }
  $memberCSRF = $login.data.csrfToken

  $keys = Invoke-RestMethod -Uri "$baseURL/api/control/keys?page=1&pageSize=20" -WebSession $memberSession
  $listedKey = $keys.data.items | Where-Object { $_.id -eq $createdKey.data.key.id } | Select-Object -First 1
  if (-not $listedKey -or
      $listedKey.prefix -ne $createdKey.data.key.prefix -or
      $listedKey.authorizedModels.Count -ne 1 -or
      $listedKey.authorizedModels[0] -ne "core-chat" -or
      ($listedKey.PSObject.Properties.Name -contains "secret")) {
    throw "Created gateway key was not listed only by its masked identity."
  }

  Assert-HTTPFailureStatus -ExpectedStatus 403 -FailureMessage "A member unexpectedly created a gateway key." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/keys" -WebSession $memberSession `
      -Headers @{ "X-CSRF-Token" = $memberCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
      -ContentType "application/json" -Body $keyBody
  }

  $publicModels = Invoke-RestMethod -Uri "$baseURL/v1/models" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if ($publicModels.object -ne "list" -or
      $publicModels.data.Count -ne 1 -or
      $publicModels.data[0].id -ne "core-chat" -or
      $publicModels.data[0].object -ne "model" -or
      $publicModels.data[0].owned_by -ne "core-openai") {
    throw "The public model catalog did not expose only the key-authorized published model."
  }

  $chatBody = @{
    model                 = "core-chat"
    messages              = @(@{ role = "user"; content = "hello from the real core flow" })
    max_completion_tokens = 64
  } | ConvertTo-Json -Depth 8
  $missingPriceStatsBefore = Invoke-RestMethod -Uri "$providerAdminURL/stats"
  $missingPriceKey = [guid]::NewGuid().ToString()
  Assert-HTTPFailureStatus -ExpectedStatus 409 -FailureMessage "A request without an effective model price was not rejected." -Action {
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $missingPriceKey } `
      -ContentType "application/json" -Body $chatBody
  }
  $missingPriceStatsAfter = Invoke-RestMethod -Uri "$providerAdminURL/stats"
  $missingPriceRequestCount = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT count(*) FROM requests WHERE gateway_key_id = '$($createdKey.data.key.id)' AND idempotency_key = '$missingPriceKey'"
  if ($missingPriceStatsAfter.completed -ne $missingPriceStatsBefore.completed -or
      $missingPriceStatsAfter.rate_limited -ne $missingPriceStatsBefore.rate_limited -or
      $missingPriceRequestCount -ne "0") {
    throw "A missing model price reached the Provider or persisted a partial request."
  }
  $zeroPriceBody = @{
    modelId = $model.data.id; currency = "USD"; inputPricePerMillionTokens = "3.25"; outputPricePerMillionTokens = "10"
    effectiveAt = (Get-Date).ToUniversalTime().AddMinutes(-1).ToString("o")
  } | ConvertTo-Json
  $zeroPrice = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/model-prices" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $zeroPriceBody
  $chatResponse = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/v1/chat/completions" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $chatBody
  $chatPayload = $chatResponse.Content | ConvertFrom-Json
  $gatewayRequestID = [string]$chatResponse.Headers["X-Gateway-Request-ID"]
  $parsedGatewayRequestID = [guid]::Empty
  if ($chatPayload.model -ne "core-chat" -or
      $chatPayload.choices[0].message.content -ne "fixture response" -or
      $chatPayload.usage.prompt_tokens -ne 4 -or
      $chatPayload.usage.completion_tokens -ne 2 -or
      -not [guid]::TryParse($gatewayRequestID, [ref]$parsedGatewayRequestID)) {
    throw "The public chat completion did not return the canonical fixture response and request identity."
  }
  $completedRequestFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT request.status || '|' || reservation.state || '|' || reservation.charged_tokens || '|' || reservation.usage_source || '|' || coalesce(request.input_tokens, -1) || '|' || coalesce(request.output_tokens, -1) || '|' || request.cost_currency || '|' || request.input_cost_nanos || '|' || request.output_cost_nanos || '|' || request.total_cost_nanos || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'completed') FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE request.id = '$gatewayRequestID'"
  if ($LASTEXITCODE -ne 0 -or $completedRequestFacts -ne "completed|settled|6|authoritative|4|2|USD|13000|20000|33000|1") {
    throw "The successful chat request did not settle one authoritative request/reservation/attempt fact: $completedRequestFacts"
  }
  $newPriceEffectiveAt = (Get-Date).ToUniversalTime().AddMilliseconds(-1).ToString("o")
  $newPriceBody = @{
    modelId = $model.data.id; currency = "USD"; inputPricePerMillionTokens = "6.5"; outputPricePerMillionTokens = "20"
    effectiveAt = $newPriceEffectiveAt
  } | ConvertTo-Json
  $newPriceIdempotencyKey = [guid]::NewGuid().ToString()
  $newPrice = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/model-prices" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $newPriceIdempotencyKey } `
    -ContentType "application/json" -Body $newPriceBody
  $replayedPrice = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/model-prices" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $newPriceIdempotencyKey } `
    -ContentType "application/json" -Body $newPriceBody
  if ($replayedPrice.data.id -ne $newPrice.data.id) { throw "Price idempotency replay created another version." }
  $conflictingPriceBody = @{
    modelId = $model.data.id; currency = "USD"; inputPricePerMillionTokens = "6.5"; outputPricePerMillionTokens = "21"
    effectiveAt = $newPriceEffectiveAt
  } | ConvertTo-Json
  Assert-HTTPFailureStatus -ExpectedStatus 409 -FailureMessage "A conflicting price idempotency replay was accepted." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/model-prices" -WebSession $adminSession `
      -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = $newPriceIdempotencyKey } `
      -ContentType "application/json" -Body $conflictingPriceBody
  }
  $immutablePriceUpdate = Invoke-LLMGatewayNativeProbe {
    & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -c `
      "UPDATE model_price_versions SET currency = 'EUR' WHERE id = '$($zeroPrice.data.id)'"
  }
  if ($immutablePriceUpdate.ExitCode -eq 0) { throw "PostgreSQL allowed an existing model price version to change." }
  $historicalCostFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT cost_currency || '|' || input_rate_nanos_per_million || '|' || output_rate_nanos_per_million || '|' || total_cost_nanos FROM requests WHERE id = '$gatewayRequestID'"
  if ($historicalCostFacts -ne "USD|3250000000|10000000000|33000") { throw "A later price version changed historical request cost." }
  $costSummaries = Invoke-RestMethod -Uri "$baseURL/api/control/costs?search=core-chat&page=1&pageSize=20" -WebSession $adminSession
  $firstCostSummary = @($costSummaries.data.items | Where-Object { $_.modelAlias -eq "core-chat" -and $_.userName -eq "Member" }) | Select-Object -First 1
  if (-not $firstCostSummary -or $firstCostSummary.currency -ne "USD" -or $firstCostSummary.totalCostNanos -ne "33000") {
    throw "Administrator cost aggregation did not expose the frozen request cost."
  }
  Assert-HTTPFailureStatus -ExpectedStatus 403 -FailureMessage "A member unexpectedly read procurement cost facts." -Action {
    Invoke-RestMethod -Uri "$baseURL/api/control/costs?page=1&pageSize=20" -WebSession $memberSession
  }

  $responseBody = @{
    model             = "core-chat"
    input             = "hello from the stored Responses flow"
    max_output_tokens = 64
    store             = $true
  } | ConvertTo-Json -Depth 8
  $createdResponse = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $responseBody
  if ($createdResponse.object -ne "response" -or
      $createdResponse.status -ne "completed" -or
      -not ([string]$createdResponse.id).StartsWith("resp_") -or
      $createdResponse.output_text -ne "fixture response") {
    throw "The stored Responses create call did not return the canonical completed response."
  }
  $retrievedResponse = Invoke-RestMethod -Uri "$baseURL/v1/responses/$($createdResponse.id)" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if ($retrievedResponse.id -ne $createdResponse.id -or $retrievedResponse.output_text -ne "fixture response") {
    throw "The original gateway Key could not retrieve its stored Response."
  }
  $responseInputItems = Invoke-RestMethod -Uri "$baseURL/v1/responses/$($createdResponse.id)/input_items" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if ($responseInputItems.object -ne "list" -or $responseInputItems.data.Count -ne 1 -or $responseInputItems.data[0].role -ne "user") {
    throw "Stored Response input items did not return the immutable request input."
  }
  $continuedResponseBody = @{
    model                = "core-chat"
    input                = "continue stored response"
    previous_response_id = $createdResponse.id
    max_output_tokens    = 64
    store                = $true
  } | ConvertTo-Json -Depth 8
  $continuedResponse = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $continuedResponseBody
  if ($continuedResponse.status -ne "completed" -or $continuedResponse.previous_response_id -ne $createdResponse.id) {
    throw "A stored Response continuation did not preserve and execute its previous response chain."
  }
  Assert-HTTPFailureStatus -ExpectedStatus 400 -FailureMessage "A synchronous Response was incorrectly cancelable." -Action {
    Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses/$($createdResponse.id)/cancel" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  }
  Assert-HTTPFailureStatus -ExpectedStatus 404 -FailureMessage "A different gateway Key read another Key's stored Response." -Action {
    Invoke-RestMethod -Uri "$baseURL/v1/responses/$($createdResponse.id)" -Headers @{ Authorization = "Bearer $otherGatewayKeySecret" }
  }
  $deletedResponse = Invoke-RestMethod -Method Delete -Uri "$baseURL/v1/responses/$($createdResponse.id)" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if (-not $deletedResponse.deleted -or $deletedResponse.id -ne $createdResponse.id -or $deletedResponse.object -ne "response") {
    throw "Response deletion did not return the OpenAI-compatible deletion object."
  }
  Assert-HTTPFailureStatus -ExpectedStatus 404 -FailureMessage "A deleted Response remained retrievable." -Action {
    Invoke-RestMethod -Uri "$baseURL/v1/responses/$($createdResponse.id)" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  }

  $backgroundIdempotencyKey = [guid]::NewGuid().ToString()
  $backgroundBody = @{
    model             = "core-chat"
    input             = "hello from the durable background Responses flow"
    max_output_tokens = 64
    background        = $true
    store             = $true
  } | ConvertTo-Json -Depth 8
  $backgroundResponse = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $backgroundIdempotencyKey } `
    -ContentType "application/json" -Body $backgroundBody
  $backgroundReplay = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $backgroundIdempotencyKey } `
    -ContentType "application/json" -Body $backgroundBody
  if ($backgroundResponse.object -ne "response" -or
      -not ([string]$backgroundResponse.id).StartsWith("resp_") -or
      $backgroundReplay.id -ne $backgroundResponse.id) {
    throw "Background Response acceptance and same-body idempotent replay did not return one stable Response."
  }
  $backgroundCompleted = $null
  $backgroundDeadline = (Get-Date).AddSeconds(15)
  do {
    $backgroundCompleted = Invoke-RestMethod -Uri "$baseURL/v1/responses/$($backgroundResponse.id)" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
    if ($backgroundCompleted.status -eq "completed") {
      break
    }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $backgroundDeadline)
  if ($backgroundCompleted.status -ne "completed" -or $backgroundCompleted.output_text -ne "fixture response") {
    throw "Background Response did not become a retrievable completed response: $($backgroundCompleted | ConvertTo-Json -Compress)"
  }
  $backgroundResponseID = ([guid]([string]$backgroundResponse.id).Substring(5)).ToString()
  $backgroundFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT response.status || '|' || (response.request_id = response.id)::text || '|' || request.status || '|' || reservation.state || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id) || '|' || (SELECT count(*) FROM response_records replay WHERE replay.gateway_key_id = response.gateway_key_id AND replay.idempotency_key = '$backgroundIdempotencyKey') FROM response_records response JOIN requests request ON request.id = response.request_id JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE response.id = '$backgroundResponseID'"
  if ($LASTEXITCODE -ne 0 -or $backgroundFacts -ne "completed|true|completed|settled|1|1") {
    throw "Background Response did not preserve one response/request/attempt/settlement owner fact: $backgroundFacts"
  }

  $backgroundCancelBody = @{
    model             = "core-chat"
    input             = "hold background cancellation"
    max_output_tokens = 64
    background        = $true
    store             = $true
  } | ConvertTo-Json -Depth 8
  $providerCanceledBeforeBackground = (Invoke-RestMethod -Uri "$providerAdminURL/stats").canceled
  $backgroundCancel = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $backgroundCancelBody
  $backgroundCancelDeadline = (Get-Date).AddSeconds(10)
  do {
    $providerActive = (Invoke-RestMethod -Uri "$providerAdminURL/stats").active
    if ($providerActive -ge 1) {
      break
    }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $backgroundCancelDeadline)
  if ($providerActive -lt 1) {
    throw "Background Response did not reach the controlled Provider before cancellation."
  }
  $backgroundCanceled = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses/$($backgroundCancel.id)/cancel" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if ($backgroundCanceled.status -ne "canceled") {
    throw "Background Response cancellation was not persisted."
  }
  $backgroundCancelResponseID = ([guid]([string]$backgroundCancel.id).Substring(5)).ToString()
  $backgroundCancelFacts = $null
  do {
    $backgroundCancelFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
      "SELECT response.status || '|' || request.status || '|' || reservation.state || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'uncertain') FROM response_records response JOIN requests request ON request.id = response.id JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE response.id = '$backgroundCancelResponseID'"
    $providerCanceledAfterBackground = (Invoke-RestMethod -Uri "$providerAdminURL/stats").canceled
    if ($backgroundCancelFacts -eq "canceled|uncertain|reserved|1" -and $providerCanceledAfterBackground -gt $providerCanceledBeforeBackground) {
      break
    }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $backgroundCancelDeadline)
  if ($backgroundCancelFacts -ne "canceled|uncertain|reserved|1" -or $providerCanceledAfterBackground -le $providerCanceledBeforeBackground) {
    throw "Canceled background Response did not preserve canceled presentation and uncertain accounting: response=$backgroundCancelFacts providerCanceled=$providerCanceledAfterBackground"
  }

  $capacityHoldKey = [guid]::NewGuid().ToString()
  $capacityHoldBody = @{
    model                 = "core-chat"
    messages              = @(@{ role = "user"; content = "hold capacity" })
    max_completion_tokens = 64
  } | ConvertTo-Json -Depth 8
  $capacityClient = [System.Net.Http.HttpClient]::new()
  $capacitySource = [System.Threading.CancellationTokenSource]::new()
  $capacityRequest = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Post, "$baseURL/v1/chat/completions")
  $capacityRequest.Headers.Add("Authorization", "Bearer $gatewayKeySecret")
  $capacityRequest.Headers.Add("Idempotency-Key", $capacityHoldKey)
  $capacityRequest.Content = [System.Net.Http.StringContent]::new($capacityHoldBody, [System.Text.Encoding]::UTF8, "application/json")
  $capacityTask = $capacityClient.SendAsync($capacityRequest, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead, $capacitySource.Token)
  $capacityDeadline = (Get-Date).AddSeconds(10)
  do {
    $providerActive = (Invoke-RestMethod -Uri "$providerAdminURL/stats").active
    if ($providerActive -ge 1) {
      break
    }
    Start-Sleep -Milliseconds 100
  } while ((Get-Date) -lt $capacityDeadline)
  if ($providerActive -lt 1) {
    $capacitySource.Cancel()
    throw "The controlled Provider did not hold the first entitlement-capacity request."
  }

  $capacityRejectedKey = [guid]::NewGuid().ToString()
  Assert-HTTPFailureStatus -ExpectedStatus 429 -FailureMessage "A second request exceeded the accepted entitlement concurrency limit." -Action {
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $capacityRejectedKey } `
      -ContentType "application/json" -Body $chatBody
  }
  $capacityRejectedFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT request.status || '|' || reservation.state || '|' || coalesce(request.error_kind, '') || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id) FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE request.gateway_key_id = '$($createdKey.data.key.id)' AND request.idempotency_key = '$capacityRejectedKey'"
  if ($LASTEXITCODE -ne 0 -or $capacityRejectedFacts -ne "failed|released|admission_capacity_exhausted|0") {
    throw "Entitlement capacity rejection did not release the accepted reservation before Provider send: $capacityRejectedFacts"
  }
  $capacitySource.Cancel()
  try {
    $null = $capacityTask.GetAwaiter().GetResult()
  } catch {
    if (-not $capacitySource.IsCancellationRequested) {
      throw
    }
  } finally {
    $capacityRequest.Dispose()
    $capacityClient.Dispose()
    $capacitySource.Dispose()
  }

  $gatewayKeyTestModels = Invoke-RestMethod -Uri "$baseURL/api/control/gateway-key-test/models?gatewayKeyId=$($createdKey.data.key.id)" -WebSession $memberSession
  if ($gatewayKeyTestModels.data.Count -ne 1 -or
      $gatewayKeyTestModels.data[0].alias -ne "core-chat") {
    throw "The Gateway Key test did not use the selected Key's published model catalog."
  }
  $gatewayKeyTestIdempotencyKey = [guid]::NewGuid().ToString()
  $gatewayKeyTestBody = @{
    gatewayKeyId = $createdKey.data.key.id
    model        = "core-chat"
    message      = "hi"
  } | ConvertTo-Json -Depth 8
  $gatewayKeyTestResponse = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/api/control/gateway-key-test/runs" -WebSession $memberSession `
    -Headers @{ "X-CSRF-Token" = $memberCSRF; "Idempotency-Key" = $gatewayKeyTestIdempotencyKey } `
    -ContentType "application/json" -Body $gatewayKeyTestBody
  $gatewayKeyTestEvents = @($gatewayKeyTestResponse.Content -split "`r?`n" | Where-Object { $_.StartsWith("data: ") } | ForEach-Object { $_.Substring(6) | ConvertFrom-Json })
  $gatewayKeyTestContentEvent = $gatewayKeyTestEvents | Where-Object { $_.type -eq "content" } | Select-Object -First 1
  $gatewayKeyTestUsageEvent = $gatewayKeyTestEvents | Where-Object { $_.type -eq "usage" } | Select-Object -First 1
  $gatewayKeyTestCompletedEvent = $gatewayKeyTestEvents | Where-Object { $_.type -eq "completed" } | Select-Object -First 1
  $gatewayKeyTestRaw = $gatewayKeyTestResponse.Content -replace "`r", "" -replace "`n", "|"
  $parsedGatewayKeyTestRequestID = [guid]::Empty
  if ($gatewayKeyTestResponse.Headers["Content-Type"] -notlike "text/event-stream*" -or
      $gatewayKeyTestContentEvent.delta -ne "fixture stream" -or
      $gatewayKeyTestUsageEvent.inputTokens -ne 4 -or
      $gatewayKeyTestUsageEvent.outputTokens -ne 2 -or
      $gatewayKeyTestUsageEvent.source -ne "authoritative" -or
      -not [guid]::TryParse([string]$gatewayKeyTestCompletedEvent.requestId, [ref]$parsedGatewayKeyTestRequestID)) {
    throw "The Gateway Key test did not stream content, authoritative usage, and completion facts: raw=$gatewayKeyTestRaw content=$($gatewayKeyTestContentEvent | ConvertTo-Json -Compress) usage=$($gatewayKeyTestUsageEvent | ConvertTo-Json -Compress) completed=$($gatewayKeyTestCompletedEvent | ConvertTo-Json -Compress) contentType=$($gatewayKeyTestResponse.Headers['Content-Type'])"
  }
  $gatewayKeyTestRequestFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT request.status || '|' || reservation.state || '|' || reservation.charged_tokens || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'completed') FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE request.gateway_key_id = '$($createdKey.data.key.id)' AND request.idempotency_key = '$gatewayKeyTestIdempotencyKey'"
  if ($LASTEXITCODE -ne 0 -or $gatewayKeyTestRequestFacts -ne "completed|settled|6|1") {
    throw "The Gateway Key test did not settle through the shared request workflow: $gatewayKeyTestRequestFacts"
  }

  $streamChatBody = @{
    model    = "core-chat"
    stream   = $true
    messages = @(@{ role = "user"; content = "hello from the real streaming chat" })
  } | ConvertTo-Json -Depth 8
  $streamChatResponse = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/v1/chat/completions" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $streamChatBody
  $streamRaw = $streamChatResponse.Content -replace "`r", "" -replace "`n", "|"
  $streamContentType = [string]$streamChatResponse.Headers["Content-Type"]
  $streamGatewayRequestID = [string]$streamChatResponse.Headers["X-Gateway-Request-ID"]
  if ($streamContentType -notlike "text/event-stream*" -or
      -not $streamChatResponse.Content.Contains("fixture stream") -or
      -not $streamChatResponse.Content.Contains("data: [DONE]") -or
      -not [guid]::TryParse($streamGatewayRequestID, [ref]$parsedGatewayRequestID)) {
    throw "The real streaming Chat did not return SSE content, completion, and gateway request identity: contentType=$streamContentType requestID=$streamGatewayRequestID stream=$streamRaw"
  }
  $streamRequestID = $streamGatewayRequestID
  $streamFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT request.status || '|' || reservation.state || '|' || reservation.charged_tokens || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'completed') FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE request.id = '$streamRequestID'"
  if ($LASTEXITCODE -ne 0 -or $streamFacts -ne "completed|settled|6|1") {
    throw "The real streaming Chat did not settle one request/reservation/attempt fact: $streamFacts"
  }

  $cancelIdempotencyKey = [guid]::NewGuid().ToString()
  $cancelBody = @{
    model    = "core-chat"
    stream   = $true
    messages = @(@{ role = "user"; content = "hold stream then cancel" })
  } | ConvertTo-Json -Depth 8
  $cancelClient = [System.Net.Http.HttpClient]::new()
  $cancelSource = [System.Threading.CancellationTokenSource]::new()
  $cancelRequest = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Post, "$baseURL/v1/chat/completions")
  $cancelRequest.Headers.Add("Authorization", "Bearer $gatewayKeySecret")
  $cancelRequest.Headers.Add("Idempotency-Key", $cancelIdempotencyKey)
  $cancelRequest.Content = [System.Net.Http.StringContent]::new($cancelBody, [System.Text.Encoding]::UTF8, "application/json")
  $cancelResponse = $cancelClient.SendAsync($cancelRequest, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead, $cancelSource.Token).GetAwaiter().GetResult()
  $cancelStream = $cancelResponse.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
  $cancelBuffer = New-Object byte[] 4096
  $cancelRead = $cancelStream.Read($cancelBuffer, 0, $cancelBuffer.Length)
  if ($cancelRead -le 0) {
    throw "The streaming cancellation request did not receive its first SSE bytes."
  }
  $cancelRequestID = [string]$cancelResponse.Headers.GetValues("X-Gateway-Request-ID") | Select-Object -First 1
  $cancelSource.Cancel()
  $cancelStream.Dispose()
  $cancelResponse.Dispose()
  $cancelRequest.Dispose()
  $cancelClient.Dispose()
  $cancelFacts = $null
  $cancelDeadline = (Get-Date).AddSeconds(10)
  do {
    $cancelFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
      "SELECT request.status || '|' || reservation.state || '|' || reservation.charged_tokens || '|' || reservation.usage_source || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'uncertain') FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE request.id = '$cancelRequestID'"
    if ($cancelFacts -eq "uncertain|reserved|0|unknown|1") {
      break
    }
    Start-Sleep -Milliseconds 100
  } while ((Get-Date) -lt $cancelDeadline)
  $providerCanceled = (Invoke-RestMethod -Uri "$providerAdminURL/stats").canceled
  if ($LASTEXITCODE -ne 0 -or $cancelFacts -ne "uncertain|reserved|0|unknown|1" -or $providerCanceled -lt 1) {
    throw "The canceled stream did not preserve an uncertain hold and upstream cancellation fact: request=$cancelFacts providerCanceled=$providerCanceled"
  }

  $uncertainIdempotencyKey = [guid]::NewGuid().ToString()
  $uncertainChatBody = @{
    model                 = "core-chat"
    messages              = @(@{ role = "user"; content = "drop after read" })
    max_completion_tokens = 64
  } | ConvertTo-Json -Depth 8
  Assert-HTTPFailureStatus -ExpectedStatus 409 -FailureMessage "A connection drop after the Provider read the request did not return an uncertain upstream error." -Action {
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $uncertainIdempotencyKey } `
      -ContentType "application/json" -Body $uncertainChatBody
  }
  $uncertainRequestFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT request.status || '|' || reservation.state || '|' || reservation.charged_tokens || '|' || reservation.usage_source || '|' || (reservation.terminal_event_id IS NULL)::text || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'uncertain') FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE request.gateway_key_id = '$($createdKey.data.key.id)' AND request.idempotency_key = '$uncertainIdempotencyKey'"
  if ($LASTEXITCODE -ne 0 -or $uncertainRequestFacts -ne "uncertain|reserved|0|unknown|true|1") {
    throw "The unknown upstream side effect did not retain an uncertain request with a held reservation: $uncertainRequestFacts"
  }

  $backgroundRestartStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
  $backgroundRestartBody = @{
    model             = "core-chat"
    input             = "hold background across gateway restart"
    max_output_tokens = 64
    background        = $true
    store             = $true
  } | ConvertTo-Json -Depth 8
  $backgroundRestart = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $backgroundRestartBody
  $backgroundRestartDeadline = (Get-Date).AddSeconds(10)
  do {
    $providerActive = (Invoke-RestMethod -Uri "$providerAdminURL/stats").active
    if ($providerActive -ge 1) {
      break
    }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $backgroundRestartDeadline)
  if ($providerActive -lt 1) {
    throw "Background Response did not reach the Provider before the gateway restart."
  }
  $backgroundRestartResponseID = ([guid]([string]$backgroundRestart.id).Substring(5)).ToString()

  Stop-Process -Id $process.Id -Force
  $null = $process.WaitForExit(5000)

  $queuedRequestID = [guid]::NewGuid().ToString()
  $queuedReservationID = [guid]::NewGuid().ToString()
  $queuedReserveEventID = [guid]::NewGuid().ToString()
  $settlementRequestID = [guid]::NewGuid().ToString()
  $settlementExecutionID = [guid]::NewGuid().ToString()
  $settlementAttemptID = [guid]::NewGuid().ToString()
  $settlementReservationID = [guid]::NewGuid().ToString()
  $settlementReserveEventID = [guid]::NewGuid().ToString()
  $unknownRequestID = [guid]::NewGuid().ToString()
  $unknownExecutionID = [guid]::NewGuid().ToString()
  $unknownAttemptID = [guid]::NewGuid().ToString()
  $unknownReservationID = [guid]::NewGuid().ToString()
  $unknownReserveEventID = [guid]::NewGuid().ToString()
  $recoveryFixtureSQL = @"
BEGIN;
INSERT INTO requests (id, request_digest, user_id, gateway_key_id, model_id, entitlement_id, config_revision_id, resource_domain, price_version_id, cost_currency, input_rate_nanos_per_million, output_rate_nanos_per_million, status, stream, updated_at)
VALUES ('$queuedRequestID', decode(repeat('11', 32), 'hex'), '$($member.id)', '$($createdKey.data.key.id)', '$($model.data.id)', '$($entitlement.data.id)', '$($revision.data.id)', 'free', '$($zeroPrice.data.id)', 'USD', 3250000000, 10000000000, 'queued', false, now() - interval '2 minutes');
INSERT INTO ledger_events (id, user_id, entitlement_id, request_id, reservation_id, kind, token_delta, reserved_tokens, usage_source, source_event_id)
VALUES ('$queuedReserveEventID', '$($member.id)', '$($entitlement.data.id)', '$queuedRequestID', '$queuedReservationID', 'reservation', -20, 20, 'estimated', '$queuedReservationID');
INSERT INTO ledger_reservations (id, entitlement_id, request_id, state, reserved_tokens, reserve_event_id)
VALUES ('$queuedReservationID', '$($entitlement.data.id)', '$queuedRequestID', 'reserved', 20, '$queuedReserveEventID');

INSERT INTO requests (id, request_digest, user_id, gateway_key_id, model_id, entitlement_id, config_revision_id, resource_domain, price_version_id, cost_currency, input_rate_nanos_per_million, output_rate_nanos_per_million, status, stream, execution_id, execution_generation, execution_claimed_at, execution_heartbeat_at)
VALUES ('$settlementRequestID', decode(repeat('22', 32), 'hex'), '$($member.id)', '$($createdKey.data.key.id)', '$($model.data.id)', '$($entitlement.data.id)', '$($revision.data.id)', 'free', '$($zeroPrice.data.id)', 'USD', 3250000000, 10000000000, 'dispatching', false, '$settlementExecutionID', 1, now() - interval '2 minutes', now() - interval '2 minutes');
INSERT INTO request_attempts (id, request_id, execution_id, execution_generation, credential_id, sequence, status, sent_at, completed_at, input_tokens, output_tokens, usage_source)
VALUES ('$settlementAttemptID', '$settlementRequestID', '$settlementExecutionID', 1, '$($credential.data.id)', 1, 'completed', now() - interval '2 minutes', now() - interval '2 minutes', 5, 3, 'authoritative');
INSERT INTO ledger_events (id, user_id, entitlement_id, request_id, reservation_id, kind, token_delta, reserved_tokens, usage_source, source_event_id)
VALUES ('$settlementReserveEventID', '$($member.id)', '$($entitlement.data.id)', '$settlementRequestID', '$settlementReservationID', 'reservation', -20, 20, 'estimated', '$settlementReservationID');
INSERT INTO ledger_reservations (id, entitlement_id, request_id, state, reserved_tokens, reserve_event_id)
VALUES ('$settlementReservationID', '$($entitlement.data.id)', '$settlementRequestID', 'reserved', 20, '$settlementReserveEventID');

INSERT INTO requests (id, request_digest, user_id, gateway_key_id, model_id, entitlement_id, config_revision_id, resource_domain, price_version_id, cost_currency, input_rate_nanos_per_million, output_rate_nanos_per_million, status, stream, execution_id, execution_generation, execution_claimed_at, execution_heartbeat_at)
VALUES ('$unknownRequestID', decode(repeat('33', 32), 'hex'), '$($member.id)', '$($createdKey.data.key.id)', '$($model.data.id)', '$($entitlement.data.id)', '$($revision.data.id)', 'free', '$($zeroPrice.data.id)', 'USD', 3250000000, 10000000000, 'dispatching', false, '$unknownExecutionID', 1, now() - interval '2 minutes', now() - interval '2 minutes');
INSERT INTO request_attempts (id, request_id, execution_id, execution_generation, credential_id, sequence, status, sent_at)
VALUES ('$unknownAttemptID', '$unknownRequestID', '$unknownExecutionID', 1, '$($credential.data.id)', 1, 'sending', now() - interval '2 minutes');
INSERT INTO ledger_events (id, user_id, entitlement_id, request_id, reservation_id, kind, token_delta, reserved_tokens, usage_source, source_event_id)
VALUES ('$unknownReserveEventID', '$($member.id)', '$($entitlement.data.id)', '$unknownRequestID', '$unknownReservationID', 'reservation', -20, 20, 'estimated', '$unknownReservationID');
INSERT INTO ledger_reservations (id, entitlement_id, request_id, state, reserved_tokens, reserve_event_id)
VALUES ('$unknownReservationID', '$($entitlement.data.id)', '$unknownRequestID', 'reserved', 20, '$unknownReserveEventID');
COMMIT;
"@
  $recoveryFixtureResult = $recoveryFixtureSQL | & $docker exec -i $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core
  if ($LASTEXITCODE -ne 0) {
    throw "Could not create isolated crash-recovery facts: $recoveryFixtureResult"
  }

  $process = Start-Process @startArguments
  Wait-LLMGatewayReady -Process $process -BaseURL $baseURL
  $recoveryFacts = $null
  $recoveryDeadline = (Get-Date).AddSeconds(15)
  do {
    $recoveryFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
      "SELECT (SELECT status::text FROM requests WHERE id = '$queuedRequestID') || '|' || (SELECT state::text FROM ledger_reservations WHERE request_id = '$queuedRequestID') || '|' || (SELECT status::text FROM requests WHERE id = '$settlementRequestID') || '|' || (SELECT state::text FROM ledger_reservations WHERE request_id = '$settlementRequestID') || '|' || (SELECT charged_tokens FROM ledger_reservations WHERE request_id = '$settlementRequestID') || '|' || (SELECT total_cost_nanos FROM requests WHERE id = '$settlementRequestID') || '|' || (SELECT status::text FROM requests WHERE id = '$unknownRequestID') || '|' || (SELECT state::text FROM ledger_reservations WHERE request_id = '$unknownRequestID') || '|' || (SELECT status::text FROM request_attempts WHERE id = '$unknownAttemptID') || '|' || (SELECT (total_cost_nanos IS NULL)::text FROM requests WHERE id = '$unknownRequestID')"
    if ($recoveryFacts -eq "failed|released|completed|settled|8|46250|uncertain|reserved|uncertain|true") {
      break
    }
    Start-Sleep -Milliseconds 100
  } while ((Get-Date) -lt $recoveryDeadline)
  if ($LASTEXITCODE -ne 0 -or $recoveryFacts -ne "failed|released|completed|settled|8|46250|uncertain|reserved|uncertain|true") {
    throw "Gateway restart did not recover queued, known-usage, and unknown-side-effect facts: $recoveryFacts"
  }

  $backgroundRestarted = $null
  $backgroundRestartFacts = $null
  do {
    $backgroundRestarted = Invoke-RestMethod -Uri "$baseURL/v1/responses/$($backgroundRestart.id)" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
    $backgroundRestartFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
      "SELECT response.status || '|' || request.status || '|' || reservation.state || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id) FROM response_records response JOIN requests request ON request.id = response.id JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE response.id = '$backgroundRestartResponseID'"
    $backgroundRestartStatsAfter = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    if ($backgroundRestarted.status -eq "uncertain" -and
        $backgroundRestartFacts -eq "uncertain|uncertain|reserved|1" -and
        $backgroundRestartStatsAfter.held -eq ($backgroundRestartStats.held + 1) -and
        $backgroundRestartStatsAfter.canceled -gt $backgroundRestartStats.canceled) {
      break
    }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $recoveryDeadline)
  if ($backgroundRestarted.status -ne "uncertain" -or
      $backgroundRestartFacts -ne "uncertain|uncertain|reserved|1" -or
      $backgroundRestartStatsAfter.held -ne ($backgroundRestartStats.held + 1) -or
      $backgroundRestartStatsAfter.canceled -le $backgroundRestartStats.canceled) {
    throw "Restarted background Response was replayed or did not converge to one uncertain send: response=$($backgroundRestarted.status) facts=$backgroundRestartFacts held=$($backgroundRestartStatsAfter.held) canceled=$($backgroundRestartStatsAfter.canceled)"
  }

  $leaseExpiryDeadline = (Get-Date).AddSeconds(10)
  do {
    $activeLeaseMembers = 0
    $leaseKeys = @(& $docker exec -e "REDISCLI_AUTH=$valkeyPassword" $valkey.Container valkey-cli --raw --scan --pattern "llmgateway:{coordination}:lease:*")
    $nowMilliseconds = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    foreach ($leaseKey in $leaseKeys) {
      if ($leaseKey) {
        $activeLeaseMembers += [int](& $docker exec -e "REDISCLI_AUTH=$valkeyPassword" $valkey.Container valkey-cli --raw ZCOUNT $leaseKey $nowMilliseconds "+inf")
      }
    }
    if ($activeLeaseMembers -eq 0) {
      break
    }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $leaseExpiryDeadline)
  if ($activeLeaseMembers -ne 0) {
    throw "Crashed request leases did not expire before the credential cooldown scenario."
  }

  $cooldownStatsBefore = Invoke-RestMethod -Uri "$providerAdminURL/stats"
  $cooldownBody = @{
    model                 = "core-chat"
    messages              = @(@{ role = "user"; content = "persist credential cooldown" })
    max_completion_tokens = 64
  } | ConvertTo-Json -Depth 8
  Assert-HTTPFailureStatus -ExpectedStatus 429 -FailureMessage "Provider 429 did not reach the public rate-limit contract." -Action {
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
      -ContentType "application/json" -Body $cooldownBody
  }
  $cooldownStatsAfter = Invoke-RestMethod -Uri "$providerAdminURL/stats"
  $cooldownFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT status::text || '|' || (cooldown_until > now())::text || '|' || consecutive_failures || '|' || coalesce(last_error_kind, '') FROM provider_credentials WHERE id = '$($credential.data.id)'"
  if ($LASTEXITCODE -ne 0 -or $cooldownFacts -ne "cooling|true|1|rate_limit" -or
      $cooldownStatsAfter.rate_limited -ne ($cooldownStatsBefore.rate_limited + 1)) {
    throw "Provider 429 did not persist one credential cooldown without budget-breaking replay: facts=$cooldownFacts rateLimited=$($cooldownStatsAfter.rate_limited)"
  }

  Stop-Process -Id $process.Id -Force
  $null = $process.WaitForExit(5000)
  $process = Start-Process @startArguments
  Wait-LLMGatewayReady -Process $process -BaseURL $baseURL
  $coolingPublicModels = Invoke-RestMethod -Uri "$baseURL/v1/models" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if ($coolingPublicModels.data.Count -ne 1 -or $coolingPublicModels.data[0].id -ne "core-chat") {
    throw "The published model catalog changed because its only credential was temporarily cooling."
  }
  $providerStatsBeforeBlocked = Invoke-RestMethod -Uri "$providerAdminURL/stats"
  $blockedByCooldownBody = @{
    model                 = "core-chat"
    messages              = @(@{ role = "user"; content = "must not cross the persisted free pool cooldown" })
    max_completion_tokens = 64
  } | ConvertTo-Json -Depth 8
  Assert-HTTPFailureStatus -ExpectedStatus 503 -RequireRetryAfter -FailureMessage "A restarted gateway forgot the persisted free-pool cooldown." -Action {
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
      -ContentType "application/json" -Body $blockedByCooldownBody
  }
  $providerStatsAfterBlocked = Invoke-RestMethod -Uri "$providerAdminURL/stats"
  if ($providerStatsAfterBlocked.completed -ne $providerStatsBeforeBlocked.completed -or
      $providerStatsAfterBlocked.rate_limited -ne $providerStatsBeforeBlocked.rate_limited) {
    throw "A request reached the Provider while the only free credential was cooling."
  }

  $restoreCredentialBody = @{
    enabled           = $true
    expectedUpdatedAt = $credential.data.updatedAt
  } | ConvertTo-Json
  $restoredCredential = Invoke-RestMethod -Method Put -Uri "$baseURL/api/control/credentials/$($credential.data.id)/status" -WebSession $adminSession `
    -Headers @{ "X-CSRF-Token" = $adminCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $restoreCredentialBody
  if ($restoredCredential.data.status -ne "active") {
    throw "The administrator could not explicitly restore a cooling credential."
  }
  $restoredResponse = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/chat/completions" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $chatBody
  $restoredFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT status::text || '|' || consecutive_failures || '|' || (last_success_at IS NOT NULL)::text || '|' || (last_error_kind IS NULL)::text FROM provider_credentials WHERE id = '$($credential.data.id)'"
  if ($restoredResponse.choices[0].message.content -ne "fixture response" -or $restoredFacts -ne "active|0|true|true") {
    throw "A successful call did not close the administrator recovery and credential health loop: $restoredFacts"
  }

  $memberUsage = Invoke-RestMethod -Uri "$baseURL/api/control/usage?page=1&pageSize=100" -WebSession $memberSession
  $matchingUsage = @($memberUsage.data.items | Where-Object {
      $_.requestId -eq $gatewayRequestID -and $_.userName -eq "Member" -and
      $_.keyPrefix -eq $createdKey.data.key.prefix -and $_.modelAlias -eq "core-chat" -and
      $_.resourceDomain -eq "free" -and $_.inputTokens -eq 4 -and $_.outputTokens -eq 2 -and
      $_.usageSource -eq "authoritative"
    })
  if ($matchingUsage.Count -ne 1 -or @($memberUsage.data.items | Where-Object { $_.userName -ne "Member" }).Count -ne 0) {
    throw "Member usage did not expose only the authenticated owner's authoritative request facts."
  }
  if (@($memberUsage.data.items | Where-Object { $_.PSObject.Properties.Name -contains "totalCostNanos" }).Count -ne 0) {
    throw "Member usage exposed procurement cost facts."
  }
  $administratorUsage = Invoke-RestMethod -Uri "$baseURL/api/control/usage?search=$gatewayRequestID&page=1&pageSize=20" -WebSession $adminSession
  if ($administratorUsage.data.total -ne 1 -or $administratorUsage.data.items[0].requestId -ne $gatewayRequestID) {
    throw "Administrator usage search did not find the persisted logical request."
  }
  Assert-HTTPFailureStatus -ExpectedStatus 401 -FailureMessage "Unauthenticated usage query was accepted." -Action {
    Invoke-RestMethod -Uri "$baseURL/api/control/usage?page=1&pageSize=20"
  }

  Stop-Process -Id $process.Id -Force
  $null = $process.WaitForExit(5000)
  $env:LLMGATEWAY_REQUEST_MAX_ACTIVE = "1"
  $env:LLMGATEWAY_REQUEST_MAX_ACTIVE_PER_USER = "1"
  $process = Start-Process @startArguments
  Wait-LLMGatewayReady -Process $process -BaseURL $baseURL

  $crossInstanceStatsBefore = Invoke-RestMethod -Uri "$providerAdminURL/stats"
  $crossInstanceCanceledBefore = $crossInstanceStatsBefore.canceled
  $crossInstanceHeldBefore = $crossInstanceStatsBefore.held
  $crossInstanceBackground = Invoke-RestMethod -Method Post -Uri "$baseURL/v1/responses" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $backgroundCancelBody
  $crossInstanceDeadline = (Get-Date).AddSeconds(10)
  do {
    $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    if ($providerStats.held -gt $crossInstanceHeldBefore -and $providerStats.active -ge 1) { break }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $crossInstanceDeadline)
  if ($providerStats.held -le $crossInstanceHeldBefore -or $providerStats.active -lt 1) {
    throw "The first gateway did not start the background Response before the second instance joined."
  }

  $secondPort = Get-LLMGatewayFreeLoopbackPort
  $secondBaseURL = "http://127.0.0.1:$secondPort"
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$secondPort"
  $secondStartArguments = @{
    FilePath               = $binaryPath
    PassThru               = $true
    RedirectStandardOutput = $secondStdoutPath
    RedirectStandardError  = $secondStderrPath
  }
  if ($runningOnWindows) {
    $secondStartArguments.WindowStyle = "Hidden"
  }
  $secondProcess = Start-Process @secondStartArguments
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$port"
  Wait-LLMGatewayReady -Process $secondProcess -BaseURL $secondBaseURL

  $crossInstanceCanceled = Invoke-RestMethod -Method Post -Uri "$secondBaseURL/v1/responses/$($crossInstanceBackground.id)/cancel" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if ($crossInstanceCanceled.status -ne "canceled") {
    throw "The second gateway did not persist background Response cancellation."
  }
  $crossInstanceCanceledReplay = Invoke-RestMethod -Method Post -Uri "$secondBaseURL/v1/responses/$($crossInstanceBackground.id)/cancel" `
    -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  if ($crossInstanceCanceledReplay.status -ne "canceled") {
    throw "Repeated cross-instance background cancellation was not idempotent."
  }
  $crossInstanceResponseID = ([guid]([string]$crossInstanceBackground.id).Substring(5)).ToString()
  do {
    $crossInstanceFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
      "SELECT response.status || '|' || request.status || '|' || reservation.state || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'uncertain') FROM response_records response JOIN requests request ON request.id = response.id JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE response.id = '$crossInstanceResponseID'"
    $crossInstanceProviderStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    if ($crossInstanceFacts -eq "canceled|uncertain|reserved|1" -and $crossInstanceProviderStats.canceled -gt $crossInstanceCanceledBefore) { break }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $crossInstanceDeadline)
  if ($crossInstanceFacts -ne "canceled|uncertain|reserved|1" -or $crossInstanceProviderStats.canceled -le $crossInstanceCanceledBefore) {
    throw "The executing gateway did not observe cancellation persisted by its peer: facts=$crossInstanceFacts canceled=$($crossInstanceProviderStats.canceled)"
  }
  do {
    $crossInstanceProviderStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    if ($crossInstanceProviderStats.active -eq 0) { break }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $crossInstanceDeadline)
  if ($crossInstanceProviderStats.active -ne 0) {
    throw "The Provider did not finish cross-instance cancellation cleanup."
  }

  $sharedHoldBody = @{
    model                 = "core-chat"
    messages              = @(@{ role = "user"; content = "hold capacity across instances" })
    max_completion_tokens = 64
  } | ConvertTo-Json -Depth 8
  $sharedHoldKey = [guid]::NewGuid().ToString()
  $sharedWaitKey = [guid]::NewGuid().ToString()
  $sharedHeldBefore = (Invoke-RestMethod -Uri "$providerAdminURL/stats").held
  $sharedHold = Start-LLMGatewayChatCall -BaseURL $baseURL -GatewayKey $gatewayKeySecret -IdempotencyKey $sharedHoldKey -Body $sharedHoldBody
  $sharedDeadline = (Get-Date).AddSeconds(10)
  do {
    $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    if ($providerStats.held -gt $sharedHeldBefore -and $providerStats.active -ge 1) { break }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $sharedDeadline)
  if ($providerStats.held -le $sharedHeldBefore -or $providerStats.active -lt 1) {
    Close-LLMGatewayChatCall -Call $sharedHold -Cancel
    throw "The first gateway did not hold the shared admission permit."
  }
  $sharedWait = Start-LLMGatewayChatCall -BaseURL $secondBaseURL -GatewayKey $gatewayKeySecret -IdempotencyKey $sharedWaitKey -Body $chatBody
  Start-Sleep -Milliseconds 250
  $sharedWaitingFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT count(*) FROM requests WHERE gateway_key_id = '$($createdKey.data.key.id)' AND idempotency_key = '$sharedWaitKey'"
  if ($sharedWait.Task.IsCompleted -or $sharedWaitingFacts -ne "0") {
    Close-LLMGatewayChatCall -Call $sharedHold -Cancel
    Close-LLMGatewayChatCall -Call $sharedWait -Cancel
    throw "The second gateway crossed admission before the shared permit was released: requestCount=$sharedWaitingFacts completed=$($sharedWait.Task.IsCompleted)"
  }
  Close-LLMGatewayChatCall -Call $sharedHold -Cancel
  $sharedWaitDeadline = (Get-Date).AddSeconds(10)
  while (-not $sharedWait.Task.IsCompleted -and (Get-Date) -lt $sharedWaitDeadline) {
    Start-Sleep -Milliseconds 50
  }
  if (-not $sharedWait.Task.IsCompleted) {
    Close-LLMGatewayChatCall -Call $sharedWait -Cancel
    throw "The waiting gateway did not continue after the first caller canceled."
  }
  $sharedWaitResponse = $sharedWait.Task.GetAwaiter().GetResult()
  $sharedWaitPayload = $sharedWaitResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult() | ConvertFrom-Json
  if ([int]$sharedWaitResponse.StatusCode -ne 200 -or $sharedWaitPayload.choices[0].message.content -ne "fixture response") {
    throw "The waiting gateway returned an invalid response after admission release."
  }
  $sharedWaitResponse.Dispose()
  $sharedWait.Request.Dispose()
  $sharedWait.Client.Dispose()
  $sharedWait.Source.Dispose()

  do {
    $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    if ($providerStats.active -eq 0) { break }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $sharedWaitDeadline)
  if ($providerStats.active -ne 0) {
    throw "The Provider did not finish canceled admission-holder cleanup."
  }

  $crashHoldKey = [guid]::NewGuid().ToString()
  $crashWaitKey = [guid]::NewGuid().ToString()
  $crashHeldBefore = $providerStats.held
  $crashHold = Start-LLMGatewayChatCall -BaseURL $baseURL -GatewayKey $gatewayKeySecret -IdempotencyKey $crashHoldKey -Body $sharedHoldBody
  $crashDeadline = (Get-Date).AddSeconds(10)
  do {
    $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    if ($providerStats.held -gt $crashHeldBefore -and $providerStats.active -ge 1) { break }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $crashDeadline)
  if ($providerStats.held -le $crashHeldBefore -or $providerStats.active -lt 1) {
    Close-LLMGatewayChatCall -Call $crashHold -Cancel
    throw "The first gateway did not hold capacity for the crash recovery scenario."
  }
  $crashWait = Start-LLMGatewayChatCall -BaseURL $secondBaseURL -GatewayKey $gatewayKeySecret -IdempotencyKey $crashWaitKey -Body $chatBody
  Start-Sleep -Milliseconds 250
  $crashWaitingFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT count(*) FROM requests WHERE gateway_key_id = '$($createdKey.data.key.id)' AND idempotency_key = '$crashWaitKey'"
  if ($crashWait.Task.IsCompleted -or $crashWaitingFacts -ne "0") {
    Close-LLMGatewayChatCall -Call $crashHold -Cancel
    Close-LLMGatewayChatCall -Call $crashWait -Cancel
    throw "The second gateway did not wait behind the crashed holder: requestCount=$crashWaitingFacts"
  }
  Stop-Process -Id $process.Id -Force
  $null = $process.WaitForExit(5000)
  $crashWaitDeadline = (Get-Date).AddSeconds(10)
  while (-not $crashWait.Task.IsCompleted -and (Get-Date) -lt $crashWaitDeadline) {
    Start-Sleep -Milliseconds 50
  }
  if (-not $crashWait.Task.IsCompleted) {
    Close-LLMGatewayChatCall -Call $crashWait -Cancel
    throw "The waiting gateway did not recover after the holder process was killed and its lease expired."
  }
  $crashWaitResponse = $crashWait.Task.GetAwaiter().GetResult()
  if ([int]$crashWaitResponse.StatusCode -ne 200) {
    throw "The waiting gateway did not complete successfully after holder recovery."
  }
  $crashWaitResponse.Dispose()
  $crashWait.Request.Dispose()
  $crashWait.Client.Dispose()
  $crashWait.Source.Dispose()
  Close-LLMGatewayChatCall -Call $crashHold -Cancel

  $process = Start-Process @startArguments
  Wait-LLMGatewayReady -Process $process -BaseURL $baseURL
  $coordinationFailureKey = [guid]::NewGuid().ToString()
  $coordinationHeldBefore = (Invoke-RestMethod -Uri "$providerAdminURL/stats").held
  $coordinationFailure = Start-LLMGatewayChatCall -BaseURL $secondBaseURL -GatewayKey $gatewayKeySecret -IdempotencyKey $coordinationFailureKey -Body $sharedHoldBody
  $coordinationDeadline = (Get-Date).AddSeconds(10)
  do {
    $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    if ($providerStats.held -gt $coordinationHeldBefore -and $providerStats.active -ge 1) { break }
    Start-Sleep -Milliseconds 50
  } while ((Get-Date) -lt $coordinationDeadline)
  if ($providerStats.held -le $coordinationHeldBefore -or $providerStats.active -lt 1) {
    Close-LLMGatewayChatCall -Call $coordinationFailure -Cancel
    throw "The request did not reach the Provider before the Valkey interruption."
  }
  $canceledBeforeCoordinationFailure = (Invoke-RestMethod -Uri "$providerAdminURL/stats").canceled
  & $docker pause $valkey.Container | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "Could not pause isolated Valkey for the coordination failure scenario."
  }
  $valkeyPaused = $true
  $coordinationDeadline = (Get-Date).AddSeconds(10)
  do {
    $providerStats = Invoke-RestMethod -Uri "$providerAdminURL/stats"
    $coordinationFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
      "SELECT request.status || '|' || reservation.state || '|' || (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id AND attempt.status = 'uncertain') FROM requests request JOIN ledger_reservations reservation ON reservation.request_id = request.id WHERE request.gateway_key_id = '$($createdKey.data.key.id)' AND request.idempotency_key = '$coordinationFailureKey'"
    if ($providerStats.canceled -gt $canceledBeforeCoordinationFailure -and $coordinationFacts -eq "uncertain|reserved|1") { break }
    Start-Sleep -Milliseconds 100
  } while ((Get-Date) -lt $coordinationDeadline)
  if ($providerStats.canceled -le $canceledBeforeCoordinationFailure -or $coordinationFacts -ne "uncertain|reserved|1") {
    Close-LLMGatewayChatCall -Call $coordinationFailure -Cancel
    throw "Losing the shared admission lease did not cancel the Provider and preserve an uncertain hold: facts=$coordinationFacts canceled=$($providerStats.canceled)"
  }
  Close-LLMGatewayChatCall -Call $coordinationFailure

  $unavailableKey = [guid]::NewGuid().ToString()
  Assert-HTTPFailureStatus -ExpectedStatus 503 -FailureMessage "A request bypassed unavailable admission coordination." -Action {
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/v1/chat/completions" `
      -Headers @{ Authorization = "Bearer $gatewayKeySecret"; "Idempotency-Key" = $unavailableKey } `
      -ContentType "application/json" -Body $chatBody
  }
  $unavailableFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_core -Atc `
    "SELECT count(*) FROM requests WHERE gateway_key_id = '$($createdKey.data.key.id)' AND idempotency_key = '$unavailableKey'"
  if ($unavailableFacts -ne "0") {
    throw "Admission coordination failure persisted a request before returning 503: $unavailableFacts"
  }
  & $docker unpause $valkey.Container | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "Could not unpause isolated Valkey after the coordination failure scenario."
  }
  $valkeyPaused = $false
  Wait-LLMGatewayValkeyReady -Container $valkey.Container -Password $valkeyPassword

  $revokedKey = Invoke-RestMethod -Method Post -Uri "$baseURL/api/control/keys/$($createdKey.data.key.id)/revoke" -WebSession $memberSession -Headers @{ "X-CSRF-Token" = $memberCSRF }
  if ($revokedKey.data.status -ne "revoked") {
    throw "Gateway key revocation did not become visible."
  }
  Assert-HTTPFailureStatus -ExpectedStatus 401 -FailureMessage "A revoked gateway key still accessed the public model catalog." -Action {
    Invoke-RestMethod -Uri "$baseURL/v1/models" -Headers @{ Authorization = "Bearer $gatewayKeySecret" }
  }
  $runtimeLogs = @(Get-ChildItem -LiteralPath $buildDirectory -File -Filter "*.log" | Select-Object -ExpandProperty FullName)
  if ($runtimeLogs.Count -gt 0 -and
      (Select-String -LiteralPath $runtimeLogs -SimpleMatch -Quiet -Pattern @("core-upstream-secret", $gatewayKeySecret, $otherGatewayKeySecret))) {
    throw "A Provider or Gateway Key secret appeared in a core runtime log."
  }
  $gatewayKeySecret = $null
  $otherGatewayKeySecret = $null
  Invoke-RestMethod -Method Delete -Uri "$baseURL/api/control/session" -WebSession $memberSession -Headers @{ "X-CSRF-Token" = $memberCSRF } | Out-Null
  Assert-HTTPFailureStatus -ExpectedStatus 401 -FailureMessage "Logout did not invalidate the member session." -Action {
    Invoke-RestMethod -Uri "$baseURL/api/control/session" -WebSession $memberSession
  }

} catch {
  $testFailure = $_
  if (Test-Path $stdoutPath) {
    try {
      Get-Content $stdoutPath | Write-Host
    } catch {
      Write-Host "Could not read the core gateway standard-output log."
    }
  }
  if (Test-Path $stderrPath) {
    try {
      Get-Content $stderrPath | Write-Host
    } catch {
      Write-Host "Could not read the core gateway standard-error log."
    }
  }
  if (Test-Path $secondStdoutPath) {
    try {
      Get-Content $secondStdoutPath | Write-Host
    } catch {
      Write-Host "Could not read the second core gateway standard-output log."
    }
  }
  if (Test-Path $secondStderrPath) {
    try {
      Get-Content $secondStderrPath | Write-Host
    } catch {
      Write-Host "Could not read the second core gateway standard-error log."
    }
  }
} finally {
  $cleanupFailures = @()
  try {
    if ($process -and -not $process.HasExited) {
      Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
      $null = $process.WaitForExit(5000)
    }
  } catch {
    $cleanupFailures += "gateway cleanup: $($_.Exception.Message)"
  }
  try {
    if ($secondProcess -and -not $secondProcess.HasExited) {
      Stop-Process -Id $secondProcess.Id -Force -ErrorAction SilentlyContinue
      $null = $secondProcess.WaitForExit(5000)
    }
  } catch {
    $cleanupFailures += "second gateway cleanup: $($_.Exception.Message)"
  }
  try {
    if ($providerProcess -and -not $providerProcess.HasExited) {
      Stop-Process -Id $providerProcess.Id -Force -ErrorAction SilentlyContinue
      $null = $providerProcess.WaitForExit(5000)
    }
  } catch {
    $cleanupFailures += "Provider fixture cleanup: $($_.Exception.Message)"
  }
  try {
    Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot
  } catch {
    $cleanupFailures += "environment restore: $($_.Exception.Message)"
  }
  if ($null -ne $valkey) {
    try {
      if ($valkeyPaused) {
        & $docker unpause $valkey.Container | Out-Null
        $valkeyPaused = $false
      }
      Stop-LLMGatewayTestContainer -Container $valkey.Container -RunID $runID
    } catch {
      $cleanupFailures += "Valkey cleanup: $($_.Exception.Message)"
    }
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
    $failureMessage = $testFailure.Exception.Message
    $failureStack = $testFailure.ScriptStackTrace
    if ($cleanupFailures.Count -gt 0) {
      throw "Core gateway flow failed: $failureMessage at $failureStack Cleanup also failed: $($cleanupFailures -join '; ')"
    }
    throw "Core gateway flow failed: $failureMessage at $failureStack"
  }
  if ($cleanupFailures.Count -gt 0) {
    throw "Core gateway cleanup failed: $($cleanupFailures -join '; ')"
  }
}

Write-Host "Core publication, identity, Key authorization, public API, inline Gateway Key testing, multi-instance admission, execution fencing, restart recovery, and uncertain-hold request flow passed against isolated real gateways and a Provider fixture."
