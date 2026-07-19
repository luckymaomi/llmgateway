$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

Push-Location (Join-Path $PSScriptRoot "..")
$databaseName = "llmgateway_core_test_$PID"
$port = 21000 + ($PID % 1000)
$baseURL = "http://127.0.0.1:$port"
$process = $null
$logDirectory = Join-Path (Get-Location) ".build"
$stdoutPath = Join-Path $logDirectory "core-test-$PID.stdout.log"
$stderrPath = Join-Path $logDirectory "core-test-$PID.stderr.log"
try {
  New-Item -ItemType Directory -Force $logDirectory | Out-Null
  $docker = Get-LLMGatewayDockerCommand
  & $docker exec llmgateway-postgres psql -v ON_ERROR_STOP=1 -U llmgateway -d postgres -c "CREATE DATABASE $databaseName" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Could not create core test database." }

  $env:LLMGATEWAY_PROFILE = "test"
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$port"
  $env:LLMGATEWAY_DATABASE_URL = "postgres://llmgateway:llmgateway_dev@127.0.0.1:15432/$databaseName`?sslmode=disable"
  $env:LLMGATEWAY_DATABASE_MIGRATE_ON_START = "true"
  $env:LLMGATEWAY_VALKEY_DATABASE = "14"
  $env:LLMGATEWAY_MASTER_KEYS = "1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  $env:LLMGATEWAY_SESSION_PEPPER = "llmgateway-core-session-pepper-00000"
  $env:LLMGATEWAY_API_KEY_PEPPER = "llmgateway-core-api-key-pepper-00000"
  $env:LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS = "198.18.0.0/15"

  & go build -o .\.build\llmgateway-core-test.exe .\cmd\gateway
  if ($LASTEXITCODE -ne 0) { throw "Could not build gateway for core test." }
  $process = Start-Process -FilePath .\.build\llmgateway-core-test.exe -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath

  $ready = $false
  $deadline = (Get-Date).AddSeconds(30)
  do {
    if ($process.HasExited) { throw "Gateway exited before becoming ready." }
    try {
      $health = Invoke-RestMethod -Uri "$baseURL/health/ready" -TimeoutSec 2
      $ready = $health.status -eq "ready"
    } catch {
      Start-Sleep -Milliseconds 200
    }
  } while (-not $ready -and (Get-Date) -lt $deadline)
  if (-not $ready) { throw "Gateway did not become ready." }

  $adminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $bootstrapBody = @{ email = "owner@example.com"; display_name = "Owner"; password = "correct horse battery staple" } | ConvertTo-Json
  $bootstrap = Invoke-RestMethod -Method Post -Uri "$baseURL/api/setup/bootstrap" -WebSession $adminSession -ContentType "application/json" -Body $bootstrapBody
  if ($bootstrap.user.role -ne "administrator") { throw "Bootstrap did not create an administrator." }
  $adminCSRF = $adminSession.Cookies.GetCookies($baseURL)["llmgateway_csrf"].Value
  if (-not $adminCSRF) { throw "Bootstrap did not establish CSRF state." }

  $providerBody = @{
    slug = "deepseek"
    name = "DeepSeek"
    kind = "deepseek"
    base_url = "https://api.deepseek.com"
    enabled = $true
  } | ConvertTo-Json
  $provider = Invoke-RestMethod -Method Post -Uri "$baseURL/api/registry/providers" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $providerBody
  if ($provider.slug -ne "deepseek") { throw "Provider creation did not persist the registry fact." }

  $modelBody = @{
    provider_id = $provider.id
    public_name = "deepseek-chat"
    upstream_name = "deepseek-v4-flash"
    display_name = "DeepSeek Chat"
    resource_domain = "free"
    capabilities = @{ chat = $true; streaming = $true; tools = $true; reasoning = $true; structured_output = $true; context_tokens = 128000; output_tokens = 8192 }
    enabled = $true
  } | ConvertTo-Json -Depth 5
  $model = Invoke-RestMethod -Method Post -Uri "$baseURL/api/registry/models" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $modelBody
  if ($model.public_name -ne "deepseek-chat") { throw "Model creation did not persist the public catalog entry." }

  $credentialBody = @{
    provider_id = $provider.id
    name = "Core fixture"
    secret = "fixture-secret-for-encryption-test"
    resource_domain = "free"
    rpm_limit = 30
    tpm_limit = 100000
    concurrency_limit = 2
  } | ConvertTo-Json
  $credential = Invoke-RestMethod -Method Post -Uri "$baseURL/api/registry/credentials" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $credentialBody
  $bindingBody = @{ priority = 10; weight = 100 } | ConvertTo-Json
  Invoke-RestMethod -Method Put -Uri "$baseURL/api/registry/credentials/$($credential.id)/models/$($model.id)" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $bindingBody | Out-Null
  $credentials = Invoke-RestMethod -Uri "$baseURL/api/registry/credentials" -WebSession $adminSession
  $listedCredential = $credentials.items | Where-Object { $_.id -eq $credential.id } | Select-Object -First 1
  if (-not $listedCredential -or $listedCredential.status -ne "active") { throw "Credential metadata was not available after encrypted storage." }

  $configurationBody = @{
    requests = @{ max_body_bytes = 4194304; max_context_tokens = 262144; max_stream_seconds = 900; queue_timeout_millis = 30000 }
    routing = @{ max_attempts = 3; base_backoff_millis = 250; max_backoff_millis = 5000; affinity_ttl_seconds = 1800; circuit_open_seconds = 30 }
    audit = @{ content_retention_hours = 168; request_retention_days = 90 }
  } | ConvertTo-Json -Depth 5
  $revision = Invoke-RestMethod -Method Post -Uri "$baseURL/api/configuration/revisions" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $configurationBody
  $publishBody = @{ expected_version = 0 } | ConvertTo-Json
  $published = Invoke-RestMethod -Method Post -Uri "$baseURL/api/configuration/revisions/$($revision.id)/publish" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $publishBody
  if ($published.version -ne 1) { throw "Initial configuration publication did not establish version 1." }
  $activeConfiguration = Invoke-RestMethod -Uri "$baseURL/api/configuration/active" -WebSession $adminSession
  if ($activeConfiguration.revision.id -ne $revision.id) { throw "Published revision was not the active PostgreSQL fact." }

  $projectedVersion = ""
  $projectionDeadline = (Get-Date).AddSeconds(5)
  do {
    $projectedVersion = & $docker exec llmgateway-valkey valkey-cli --no-auth-warning -a llmgateway_dev -n 14 GET "llmgateway:{configuration}:active"
    if ($projectedVersion -ne "1") { Start-Sleep -Milliseconds 100 }
  } while ($projectedVersion -ne "1" -and (Get-Date) -lt $projectionDeadline)
  if ($projectedVersion -ne "1") { throw "Published configuration was not projected to Valkey." }

  $invitationBody = @{ role = "member"; valid_hours = 24 } | ConvertTo-Json
  $invitation = Invoke-RestMethod -Method Post -Uri "$baseURL/api/admin/invitations" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $invitationBody
  if (-not $invitation.code) { throw "Invitation creation did not return its one-time code." }

  $memberSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $registerBody = @{ invitation_code = $invitation.code; email = "member@example.com"; display_name = "Member"; password = "correct horse battery staple" } | ConvertTo-Json
  $registration = Invoke-RestMethod -Method Post -Uri "$baseURL/api/auth/register" -WebSession $memberSession -ContentType "application/json" -Body $registerBody
  if ($registration.status -ne "pending") { throw "Registration did not enter pending review." }

  $users = Invoke-RestMethod -Uri "$baseURL/api/admin/users?status=pending" -WebSession $adminSession
  $member = $users.items | Where-Object { $_.email -eq "member@example.com" } | Select-Object -First 1
  if (-not $member) { throw "Pending member was not visible to the administrator." }
  $approvalBody = @{ status = "active" } | ConvertTo-Json
  $approved = Invoke-RestMethod -Method Patch -Uri "$baseURL/api/admin/users/$($member.id)/status" -WebSession $adminSession -Headers @{ "X-CSRF-Token" = $adminCSRF } -ContentType "application/json" -Body $approvalBody
  if ($approved.status -ne "active") { throw "Member approval did not become active." }

  $loginBody = @{ email = "member@example.com"; password = "correct horse battery staple" } | ConvertTo-Json
  $login = Invoke-RestMethod -Method Post -Uri "$baseURL/api/auth/login" -WebSession $memberSession -ContentType "application/json" -Body $loginBody
  $memberCSRF = $memberSession.Cookies.GetCookies($baseURL)["llmgateway_csrf"].Value
  if ($login.user.role -ne "member" -or -not $memberCSRF) { throw "Approved member could not establish a session." }

  $keyBody = @{ name = "Core test"; expires_at = $null } | ConvertTo-Json
  $createdKey = Invoke-RestMethod -Method Post -Uri "$baseURL/api/keys" -WebSession $memberSession -Headers @{ "X-CSRF-Token" = $memberCSRF } -ContentType "application/json" -Body $keyBody
  if (-not $createdKey.secret.StartsWith("llmg_")) { throw "Gateway key was not revealed on creation." }
  $keys = Invoke-RestMethod -Uri "$baseURL/api/keys" -WebSession $memberSession
  $listedKey = $keys.items | Where-Object { $_.id -eq $createdKey.id } | Select-Object -First 1
  if (-not $listedKey -or $listedKey.prefix -ne $createdKey.prefix) { throw "Created gateway key was not listed by its masked identity." }

  Invoke-RestMethod -Method Delete -Uri "$baseURL/api/keys/$($createdKey.id)" -WebSession $memberSession -Headers @{ "X-CSRF-Token" = $memberCSRF } | Out-Null
  Invoke-RestMethod -Method Post -Uri "$baseURL/api/auth/logout" -WebSession $memberSession -Headers @{ "X-CSRF-Token" = $memberCSRF } | Out-Null

  Write-Host "Core identity and access flow passed against the real gateway."
} catch {
  if (Test-Path $stdoutPath) { Get-Content $stdoutPath | Write-Host }
  if (Test-Path $stderrPath) { Get-Content $stderrPath | Write-Host }
  throw
} finally {
  if ($process -and -not $process.HasExited) {
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
    $null = $process.WaitForExit(5000)
  }
  $docker = Get-LLMGatewayDockerCommand
  & $docker exec llmgateway-valkey valkey-cli --no-auth-warning -a llmgateway_dev -n 14 FLUSHDB | Out-Null
  & $docker exec llmgateway-postgres psql -v ON_ERROR_STOP=1 -U llmgateway -d postgres -c "DROP DATABASE IF EXISTS $databaseName WITH (FORCE)" | Out-Null
  Remove-Item Env:LLMGATEWAY_PROFILE -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_HTTP_ADDRESS -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_DATABASE_URL -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_DATABASE_MIGRATE_ON_START -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_VALKEY_DATABASE -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_MASTER_KEYS -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_SESSION_PEPPER -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_API_KEY_PEPPER -ErrorAction SilentlyContinue
  Remove-Item Env:LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS -ErrorAction SilentlyContinue
  Pop-Location
}
