param(
  [ValidateRange(10, 43200)][int] $DurationSeconds = 60,
  [ValidateRange(200, 300)][int] $Users = 300,
  [ValidateRange(1, 300)][int] $ActiveUsers = 60
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\isolated-services.ps1"

function Wait-CapacityGateway {
  param([System.Diagnostics.Process] $Process, [string] $BaseURL)
  $deadline = (Get-Date).AddSeconds(30)
  do {
    if ($Process.HasExited) { throw "Capacity Gateway exited before readiness." }
    try {
      $health = Invoke-RestMethod -Uri "$BaseURL/health/ready" -TimeoutSec 2
      if ($health.status -eq "ready") { return }
    } catch { Start-Sleep -Milliseconds 100 }
  } while ((Get-Date) -lt $deadline)
  throw "Capacity Gateway did not become ready."
}

function Start-CapacityProcess {
  param([string] $FilePath, [string[]] $Arguments, [string] $Stdout, [string] $Stderr)
  $start = @{
    FilePath = $FilePath; PassThru = $true
    RedirectStandardOutput = $Stdout; RedirectStandardError = $Stderr
  }
  if ($Arguments.Count -gt 0) { $start.ArgumentList = $Arguments }
  if ($env:OS -eq "Windows_NT") { $start.WindowStyle = "Hidden" }
  return Start-Process @start
}

function Invoke-CapacityControl {
  param(
    [string] $Method, [string] $Uri, $Session, [string] $CSRF = "",
    [string] $IdempotencyKey = "", $Body = $null
  )
  $headers = @{}
  if ($CSRF) { $headers["X-CSRF-Token"] = $CSRF }
  if ($IdempotencyKey) { $headers["Idempotency-Key"] = $IdempotencyKey }
  $parameters = @{ Method = $Method; Uri = $Uri; WebSession = $Session; Headers = $headers }
  if ($null -ne $Body) {
    $parameters.ContentType = "application/json"
    $parameters.Body = ($Body | ConvertTo-Json -Depth 8)
  }
  return Invoke-RestMethod @parameters
}

Push-Location (Join-Path $PSScriptRoot "..")
$runID = New-LLMGatewayTestRunID -Purpose "capacity"
$postgres = $null
$valkey = $null
$providerProcess = $null
$recoveryProcess = $null
$gatewayProcesses = @()
$environmentSnapshot = Save-LLMGatewayEnvironment
$testFailure = $null
$cleanupFailures = @()
$runningOnWindows = $env:OS -eq "Windows_NT"
$gatewayName = if ($runningOnWindows) { "gateway.exe" } else { "gateway" }
$providerName = if ($runningOnWindows) { "fixture-provider.exe" } else { "fixture-provider" }
$capacityName = if ($runningOnWindows) { "capacity.exe" } else { "capacity" }
$recoveryName = if ($runningOnWindows) { "recovery.exe" } else { "recovery" }
$buildDirectory = Join-Path (Get-Location) ".build\capacity-$runID"
$evidenceDirectory = Join-Path (Get-Location) ".build\acceptance-evidence"
$gatewayPath = Join-Path $buildDirectory $gatewayName
$providerPath = Join-Path $buildDirectory $providerName
$capacityPath = Join-Path $buildDirectory $capacityName
$recoveryPath = Join-Path $buildDirectory $recoveryName
$providerCertificatePath = Join-Path $buildDirectory "provider-ca.pem"
$reportPath = Join-Path $buildDirectory "capacity-report.json"
$recoveryReportPath = Join-Path $buildDirectory "recovery-report.json"
try {
  if ($ActiveUsers -gt $Users) { throw "ActiveUsers cannot exceed Users." }
  Clear-LLMGatewayEnvironment
  New-Item -ItemType Directory -Force $buildDirectory | Out-Null
  $postgres = Start-LLMGatewayTestPostgres -RunID $runID -DatabaseName "llmgateway_capacity" -Password "capacity-postgres-fixture"
  $valkeyPassword = "capacity-valkey-fixture"
  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $valkeyPassword
  $providerPort = Get-LLMGatewayFreeLoopbackPort
  $providerAdminPort = Get-LLMGatewayFreeLoopbackPort
  $gatewayPorts = @(
    Get-LLMGatewayFreeLoopbackPort
    Get-LLMGatewayFreeLoopbackPort
  )
  $providerBaseURL = "https://127.0.0.1:$providerPort/v1"
  $providerAdminURL = "http://127.0.0.1:$providerAdminPort"
  $gatewayURLs = @("http://127.0.0.1:$($gatewayPorts[0])", "http://127.0.0.1:$($gatewayPorts[1])")
  $firstDatabaseURL = "$($postgres.DatabaseURL)&application_name=capacity-gateway-first"
  $secondDatabaseURL = "$($postgres.DatabaseURL)&application_name=capacity-gateway-second"
  $observerDatabaseURL = "$($postgres.DatabaseURL)&application_name=capacity-observer"

  & go build -trimpath -o $gatewayPath .\cmd\gateway
  if ($LASTEXITCODE -ne 0) { throw "Could not build the capacity Gateway." }
  & go build -trimpath -o $providerPath .\scripts\fixtures\provider
  if ($LASTEXITCODE -ne 0) { throw "Could not build the capacity Provider fixture." }
  & go build -trimpath -o $capacityPath .\scripts\acceptance\capacity
  if ($LASTEXITCODE -ne 0) { throw "Could not build the capacity acceptance client." }
  & go build -trimpath -o $recoveryPath .\scripts\acceptance\recovery
  if ($LASTEXITCODE -ne 0) { throw "Could not build the recovery acceptance client." }

  $providerProcess = Start-CapacityProcess -FilePath $providerPath -Arguments @(
    "-address", "127.0.0.1:$providerPort", "-admin-address", "127.0.0.1:$providerAdminPort",
    "-certificate-out", $providerCertificatePath, "-certificate-ip", "127.0.0.1"
  ) -Stdout (Join-Path $buildDirectory "provider.stdout.log") -Stderr (Join-Path $buildDirectory "provider.stderr.log")
  $providerDeadline = (Get-Date).AddSeconds(30)
  do {
    if ($providerProcess.HasExited) { throw "Capacity Provider fixture exited before readiness." }
    try { $providerReady = (Invoke-RestMethod -Uri "$providerAdminURL/stats" -TimeoutSec 2).active -eq 0 } catch { Start-Sleep -Milliseconds 100 }
  } while (-not $providerReady -and (Get-Date) -lt $providerDeadline)
  if (-not $providerReady) { throw "Capacity Provider fixture did not become ready." }

  $apiKeyPepper = "llmgateway-capacity-api-key-pepper-000000"
  $env:LLMGATEWAY_PROFILE = "test"
  $env:LLMGATEWAY_DATABASE_URL = $firstDatabaseURL
  $env:LLMGATEWAY_DATABASE_MAX_CONNECTIONS = "24"
  $env:LLMGATEWAY_DATABASE_MIN_CONNECTIONS = "4"
  $env:LLMGATEWAY_DATABASE_MIGRATE_ON_START = "true"
  $env:LLMGATEWAY_VALKEY_ADDRESS = $valkey.Address
  $env:LLMGATEWAY_VALKEY_PASSWORD = $valkeyPassword
  $env:LLMGATEWAY_MASTER_KEYS = "1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  $env:LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION = "1"
  $env:LLMGATEWAY_SESSION_PEPPER = "llmgateway-capacity-session-pepper-00000"
  $env:LLMGATEWAY_API_KEY_PEPPER = $apiKeyPepper
  $env:LLMGATEWAY_COORDINATION_KEY_HASH_SECRET = "llmgateway-capacity-coordination-hash-000"
  $env:LLMGATEWAY_COOKIE_SECURE = "false"
  $env:LLMGATEWAY_PROVIDER_CA_BUNDLE_FILE = $providerCertificatePath
  $env:LLMGATEWAY_REQUEST_MAX_QUEUED = "512"
  $env:LLMGATEWAY_REQUEST_MAX_ACTIVE = "256"
  $env:LLMGATEWAY_REQUEST_MAX_ACTIVE_PER_USER = "16"
  $env:LLMGATEWAY_REQUEST_MAX_QUEUE_WAIT = "30s"
  $env:LLMGATEWAY_REQUEST_LEASE_TTL = "10s"
  $env:LLMGATEWAY_REQUEST_EXECUTION_HEARTBEAT_INTERVAL = "200ms"
  $env:LLMGATEWAY_REQUEST_EXECUTION_STALE_AFTER = "2s"
  $env:LLMGATEWAY_REQUEST_RECOVERY_INTERVAL = "200ms"
  $env:LLMGATEWAY_RESPONSES_MAX_WORKERS = "16"
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$($gatewayPorts[0])"
  $firstGateway = Start-CapacityProcess -FilePath $gatewayPath -Arguments @() `
    -Stdout (Join-Path $buildDirectory "gateway-first.stdout.log") -Stderr (Join-Path $buildDirectory "gateway-first.stderr.log")
  $gatewayProcesses += $firstGateway
  Wait-CapacityGateway -Process $firstGateway -BaseURL $gatewayURLs[0]

  $adminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $setup = Invoke-CapacityControl -Method Post -Uri "$($gatewayURLs[0])/api/control/setup" -Session $adminSession -Body @{
    email = "capacity-owner@example.test"
  }
  if (-not $setup.data.initialPassword) { throw "Capacity setup did not return one-time administrator credentials." }
  $csrf = $setup.data.csrfToken
  $setup.data.initialPassword = $null
  $provider = Invoke-CapacityControl -Method Post -Uri "$($gatewayURLs[0])/api/control/providers" -Session $adminSession -CSRF $csrf `
    -IdempotencyKey ([guid]::NewGuid().ToString()) -Body @{ slug = "capacity-openai"; name = "Capacity fixture"; kind = "openai-compatible"; baseUrl = $providerBaseURL }
  $model = Invoke-CapacityControl -Method Post -Uri "$($gatewayURLs[0])/api/control/models" -Session $adminSession -CSRF $csrf -Body @{
    providerId = $provider.data.id; alias = "capacity-chat"; upstreamModelId = "fixture-chat"; resourceDomain = "free"
    capabilities = @("streaming", "tools", "reasoning"); reasoningMode = "toggle"; contextTokens = 8192
  }
  $null = Invoke-CapacityControl -Method Post -Uri "$($gatewayURLs[0])/api/control/model-prices" -Session $adminSession -CSRF $csrf `
    -IdempotencyKey ([guid]::NewGuid().ToString()) -Body @{
      modelId = $model.data.id; currency = "USD"; inputPricePerMillionTokens = "0"; outputPricePerMillionTokens = "0"
      effectiveAt = (Get-Date).ToUniversalTime().AddMinutes(-1).ToString("o")
    }
  foreach ($credentialIndex in 1..6) {
    $credential = Invoke-CapacityControl -Method Post -Uri "$($gatewayURLs[0])/api/control/credentials" -Session $adminSession -CSRF $csrf `
      -IdempotencyKey ([guid]::NewGuid().ToString()) -Body @{
        providerId = $provider.data.id; label = "Capacity fixture credential $credentialIndex"; secret = "core-upstream-secret"; resourceDomain = "free"
        modelBindings = @(@{ modelId = $model.data.id; priority = 10; weight = 100 })
        rpmLimit = 100000; tpmLimit = 100000000; concurrencyLimit = 128
      }
    if (-not $credential.data.id) { throw "Capacity credential $credentialIndex was not created." }
  }
  $enabled = Invoke-CapacityControl -Method Put -Uri "$($gatewayURLs[0])/api/control/providers/$($provider.data.id)/status" -Session $adminSession -CSRF $csrf `
    -IdempotencyKey ([guid]::NewGuid().ToString()) -Body @{ enabled = $true; expectedUpdatedAt = $provider.data.updatedAt }
  if ($enabled.data.status -ne "enabled") { throw "Capacity Provider was not enabled." }
  $revision = Invoke-CapacityControl -Method Post -Uri "$($gatewayURLs[0])/api/control/configuration/revisions" -Session $adminSession -CSRF $csrf `
    -IdempotencyKey ([guid]::NewGuid().ToString())
  $published = Invoke-CapacityControl -Method Post -Uri "$($gatewayURLs[0])/api/control/configuration/revisions/$($revision.data.id)/publish" -Session $adminSession -CSRF $csrf `
    -IdempotencyKey ([guid]::NewGuid().ToString()) -Body @{ expectedActiveVersion = 0 }
  if ($published.data.phase -ne "completed") { throw "Capacity configuration was not published." }

  $env:LLMGATEWAY_DATABASE_MIGRATE_ON_START = "false"
  $env:LLMGATEWAY_DATABASE_URL = $secondDatabaseURL
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$($gatewayPorts[1])"
  $secondGateway = Start-CapacityProcess -FilePath $gatewayPath -Arguments @() `
    -Stdout (Join-Path $buildDirectory "gateway-second.stdout.log") -Stderr (Join-Path $buildDirectory "gateway-second.stderr.log")
  $gatewayProcesses += $secondGateway
  Wait-CapacityGateway -Process $secondGateway -BaseURL $gatewayURLs[1]

  & $capacityPath -base-urls ($gatewayURLs -join ",") -database-url $observerDatabaseURL `
    -valkey-address $valkey.Address -valkey-password $valkeyPassword -api-key-pepper $apiKeyPepper `
    -model-id $model.data.id -model "capacity-chat" -provider-admin-url $providerAdminURL `
    -users $Users -active-users $ActiveUsers -duration "$($DurationSeconds)s" -postgres-connection-limit 49 > $reportPath
  if ($LASTEXITCODE -ne 0) { throw "Capacity acceptance failed; inspect $reportPath." }
  $report = Get-Content -LiteralPath $reportPath -Raw -Encoding UTF8 | ConvertFrom-Json
  if ($report.failures.Count -ne 0) { throw "Capacity report contains failed production thresholds." }
  New-Item -ItemType Directory -Force $evidenceDirectory | Out-Null
  Copy-Item -LiteralPath $reportPath -Destination (Join-Path $evidenceDirectory "capacity-report.json") -Force

  $recoveryProcess = Start-CapacityProcess -FilePath $recoveryPath -Arguments @(
    "-base-url", $gatewayURLs[0], "-run-id", $report.runId, "-model", "capacity-chat", "-requests", "128"
  ) -Stdout $recoveryReportPath -Stderr (Join-Path $buildDirectory "recovery.stderr.log")
  $recoveryDeadline = (Get-Date).AddSeconds(30)
  do {
    if ($recoveryProcess.HasExited) { throw "Recovery client exited before all crash-bound streams reached the Provider." }
    $recoveryStats = Invoke-RestMethod -Uri "$providerAdminURL/stats" -TimeoutSec 2
    if ($recoveryStats.active -lt 128) { Start-Sleep -Milliseconds 100 }
  } while ($recoveryStats.active -lt 128 -and (Get-Date) -lt $recoveryDeadline)
  if ($recoveryStats.active -lt 128) { throw "Recovery drill did not hold 128 committed streams." }

  Stop-Process -Id $firstGateway.Id -Force -ErrorAction Stop
  $firstGateway.WaitForExit()
  $canceledDeadline = (Get-Date).AddSeconds(5)
  do {
    $recoveryStats = Invoke-RestMethod -Uri "$providerAdminURL/stats" -TimeoutSec 2
    if ($recoveryStats.active -gt 0) { Start-Sleep -Milliseconds 50 }
  } while ($recoveryStats.active -gt 0 -and (Get-Date) -lt $canceledDeadline)
  if ($recoveryStats.active -gt 0 -or $recoveryStats.canceled -lt 129) { throw "Killed Gateway streams did not disconnect from the Provider." }
  Invoke-RestMethod -Method Post -Uri "$providerAdminURL/release" -TimeoutSec 2 | Out-Null
  if (-not $recoveryProcess.WaitForExit(30000)) { throw "Recovery client timed out while observing interrupted committed streams." }
  $recoveryProcess.WaitForExit()
  $recoveryReport = Get-Content -LiteralPath $recoveryReportPath -Raw -Encoding UTF8 | ConvertFrom-Json
  if ($recoveryReport.requests -ne 128 -or $recoveryReport.statusOk -ne 128 -or $recoveryReport.completed -ne 0 -or $recoveryReport.interrupted -ne 128) {
    throw "Recovery client result drifted from the committed-stream crash contract."
  }

  $probeBody = @{
    model = "capacity-chat"; messages = @(@{ role = "user"; content = "capacity short" }); max_tokens = 16
  } | ConvertTo-Json -Depth 4
  $probeKey = "llmg_capacity_$($report.runId)_250"
  $probe = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$($gatewayURLs[1])/v1/chat/completions" `
    -Headers @{ Authorization = "Bearer $probeKey"; "Idempotency-Key" = [guid]::NewGuid().ToString() } `
    -ContentType "application/json" -Body $probeBody -TimeoutSec 20
  $probeKey = $null
  if ($probe.StatusCode -ne 200) { throw "Surviving Gateway did not recover after the shared lease expired." }

  $docker = Get-LLMGatewayDockerCommand
  $databaseDeadline = (Get-Date).AddSeconds(10)
  do {
    $recoveryFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_capacity -Atc `
      "SELECT count(*) FROM requests r JOIN ledger_reservations lr ON lr.request_id = r.id WHERE r.idempotency_key LIKE 'recovery-first-%' AND r.status = 'uncertain' AND lr.state = 'reserved'"
    if ($LASTEXITCODE -ne 0) { throw "Could not inspect crash recovery facts." }
    if ([int]$recoveryFacts -lt 128) { Start-Sleep -Milliseconds 100 }
  } while ([int]$recoveryFacts -lt 128 -and (Get-Date) -lt $databaseDeadline)
  if ([int]$recoveryFacts -ne 128) { throw "Crash recovery did not preserve exactly 128 uncertain holds." }
  Copy-Item -LiteralPath $recoveryReportPath -Destination (Join-Path $evidenceDirectory "recovery-report.json") -Force

  $logFiles = @(Get-ChildItem -LiteralPath $buildDirectory -File -Filter "*.log" | Select-Object -ExpandProperty FullName)
  if ($logFiles.Count -gt 0 -and (Select-String -LiteralPath $logFiles -SimpleMatch -Quiet -Pattern "llmg_capacity_", $apiKeyPepper, "core-upstream-secret")) {
    throw "A capacity fixture secret appeared in runtime logs."
  }
  if ($logFiles.Count -gt 0 -and (Select-String -LiteralPath $logFiles -SimpleMatch -Quiet -Pattern '"level":"ERROR"')) {
    throw "Capacity runtime logs contain an unexpected ERROR event."
  }
  Write-Host "Capacity and crash recovery acceptance passed for $Users controlled users, $ActiveUsers steady active users, and two Gateway instances."
} catch {
  $testFailure = $_
} finally {
  foreach ($process in @($gatewayProcesses) + @($providerProcess, $recoveryProcess)) {
    if ($null -ne $process -and -not $process.HasExited) {
      try { Stop-Process -Id $process.Id -Force -ErrorAction Stop; $process.WaitForExit() } catch { $cleanupFailures += "Process cleanup: $($_.Exception.Message)" }
    }
  }
  if ($null -ne $valkey) { try { Stop-LLMGatewayTestContainer -Container $valkey.Container -RunID $runID } catch { $cleanupFailures += "Valkey cleanup: $($_.Exception.Message)" } }
  if ($null -ne $postgres) { try { Stop-LLMGatewayTestContainer -Container $postgres.Container -RunID $runID } catch { $cleanupFailures += "PostgreSQL cleanup: $($_.Exception.Message)" } }
  Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot
  Pop-Location
}
if ($null -ne $testFailure) { throw $testFailure }
if ($cleanupFailures.Count -gt 0) { throw ($cleanupFailures -join [Environment]::NewLine) }
