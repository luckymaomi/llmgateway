$ErrorActionPreference = "Stop"

. "$PSScriptRoot\isolated-services.ps1"
. "$PSScriptRoot\acceptance\runtime.ps1"

function Invoke-ControlJSON {
  param(
    [Parameter(Mandatory = $true)][string] $Method,
    [Parameter(Mandatory = $true)][string] $Path,
    [Parameter(Mandatory = $true)] $Body,
    [switch] $Idempotent
  )

  $headers = @{ "X-CSRF-Token" = $script:AdminCSRF }
  if ($Idempotent) { $headers["Idempotency-Key"] = [guid]::NewGuid().ToString() }
  $encoded = $Body | ConvertTo-Json -Depth 10 -Compress
  return Invoke-RestMethod -Method $Method -Uri "$script:BaseURL$Path" -WebSession $script:AdminSession `
    -Headers $headers -ContentType "application/json" -Body $encoded -TimeoutSec 180
}

function New-Provider {
  param(
    [Parameter(Mandatory = $true)][string] $Slug,
    [Parameter(Mandatory = $true)][string] $Name,
    [Parameter(Mandatory = $true)][string] $Kind,
    [Parameter(Mandatory = $true)][string] $ProviderBaseURL
  )

  return (Invoke-ControlJSON -Method Post -Path "/api/control/providers" -Idempotent -Body @{
      slug = $Slug; name = $Name; kind = $Kind; baseUrl = $ProviderBaseURL
    }).data
}

function New-Model {
  param(
    [Parameter(Mandatory = $true)][string] $ProviderID,
    [Parameter(Mandatory = $true)][string] $Alias,
    [Parameter(Mandatory = $true)][string] $UpstreamModelID,
    [ValidateSet("", "toggle", "effort", "hybrid")][string] $ReasoningMode = "",
    [string] $Currency = "USD",
    [string] $InputPricePerMillionTokens = "0",
    [string] $OutputPricePerMillionTokens = "0"
  )

  $capabilities = [System.Collections.Generic.List[string]]::new()
  $capabilities.Add("streaming")
  $capabilities.Add("tools")
  if ($ReasoningMode) { $capabilities.Add("reasoning") }
  $body = @{
    providerId = $ProviderID
    alias = $Alias
    upstreamModelId = $UpstreamModelID
    resourceDomain = "free"
    capabilities = @($capabilities)
    contextTokens = 131072
  }
  if ($ReasoningMode) { $body.reasoningMode = $ReasoningMode }
  $model = (Invoke-ControlJSON -Method Post -Path "/api/control/models" -Body $body).data
  $null = Invoke-ControlJSON -Method Post -Path "/api/control/model-prices" -Idempotent -Body @{
    modelId = $model.id
    currency = $Currency
    inputPricePerMillionTokens = $InputPricePerMillionTokens
    outputPricePerMillionTokens = $OutputPricePerMillionTokens
    effectiveAt = (Get-Date).ToUniversalTime().AddMinutes(-1).ToString("o")
  }
  return $model
}

function New-Credential {
  param(
    [Parameter(Mandatory = $true)][string] $ProviderID,
    [Parameter(Mandatory = $true)][string] $Name,
    [Parameter(Mandatory = $true)][string] $Secret,
    [Parameter(Mandatory = $true)][string] $ModelID,
    [Parameter(Mandatory = $true)][int] $Priority,
    [Parameter(Mandatory = $true)][int] $Weight,
    [string[]] $AdditionalModelIDs = @()
  )

  $bindings = [System.Collections.Generic.List[object]]::new()
  $bindings.Add(@{ modelId = $ModelID; priority = $Priority; weight = $Weight })
  foreach ($additionalModelID in $AdditionalModelIDs) {
    $bindings.Add(@{ modelId = $additionalModelID; priority = $Priority; weight = $Weight })
  }
  return (Invoke-ControlJSON -Method Post -Path "/api/control/credentials" -Idempotent -Body @{
      providerId = $ProviderID
      label = $Name
      secret = $Secret
      resourceDomain = "free"
      modelBindings = @($bindings)
      rpmLimit = 60
      tpmLimit = 1000000
      concurrencyLimit = 4
    }).data
}

function Enable-Provider {
  param([Parameter(Mandatory = $true)] $Provider)

  $result = Invoke-ControlJSON -Method Put -Path "/api/control/providers/$($Provider.id)/status" -Idempotent -Body @{
    enabled = $true; expectedUpdatedAt = $Provider.updatedAt
  }
  if ($result.data.status -ne "enabled") { throw "A real Provider did not become enabled." }
}

function New-Entitlement {
  param(
    [Parameter(Mandatory = $true)][string] $OwnerID,
    [Parameter(Mandatory = $true)][string] $ModelID
  )

  $result = Invoke-ControlJSON -Method Post -Path "/api/control/entitlements" -Idempotent -Body @{
    ownerId = $OwnerID
    planKind = "token"
    resourceDomain = "free"
    modelId = $ModelID
    grantedTokens = 1000000
    concurrencyLimit = 4
    rpmLimit = 120
    tpmLimit = 1000000
    startsAt = (Get-Date).ToUniversalTime().AddMinutes(-1).ToString("o")
    expiresAt = (Get-Date).ToUniversalTime().AddDays(1).ToString("o")
    reason = "Real Provider acceptance"
  }
  if ($result.data.modelId -ne $ModelID) { throw "A real Provider entitlement was not persisted." }
}

function Invoke-SDKClient {
  param(
    [Parameter(Mandatory = $true)][ValidateSet("go", "python")][string] $SDK,
    [Parameter(Mandatory = $true)][string] $SuccessModel,
    [Parameter(Mandatory = $true)][string] $StreamModel,
    [Parameter(Mandatory = $true)][ValidateSet("toggle", "effort", "hybrid")][string] $ReasoningMode,
    [Parameter(Mandatory = $true)][string] $ErrorModel,
    [Parameter(Mandatory = $true)][string] $PythonPath
  )

  $env:LLMGATEWAY_SDK_BASE_URL = "$script:BaseURL/v1"
  $env:LLMGATEWAY_SDK_API_KEY = $script:GatewayKey
  $env:LLMGATEWAY_SDK_SUCCESS_MODEL = $SuccessModel
  $env:LLMGATEWAY_SDK_STREAM_MODEL = $StreamModel
  $env:LLMGATEWAY_SDK_REASONING_MODE = $ReasoningMode
  $env:LLMGATEWAY_SDK_ERROR_MODEL = $ErrorModel
  $previousPreference = $ErrorActionPreference
  try {
    $ErrorActionPreference = "Continue"
    if ($SDK -eq "go") {
      $output = & go run . 2>$null
      $exitCode = $LASTEXITCODE
    } else {
      $output = & $PythonPath client.py 2>$null
      $exitCode = $LASTEXITCODE
    }
  } finally {
    $ErrorActionPreference = $previousPreference
    $env:LLMGATEWAY_SDK_API_KEY = $null
    $env:LLMGATEWAY_SDK_EXPLICIT_REISSUE = $null
    $env:LLMGATEWAY_SDK_STREAM_MODEL = $null
    $env:LLMGATEWAY_SDK_REASONING_MODE = $null
  }
  if (@($output).Count -ne 1) {
    throw "$SDK SDK acceptance failed without a valid redacted summary."
  }
  $summary = [string]($output | Select-Object -First 1) | ConvertFrom-Json
  if ($exitCode -ne 0 -or -not $summary.succeeded) {
    $failed = @($summary.scenarios | Where-Object { -not $_.succeeded } | ForEach-Object {
        "$($_.name):$($_.httpStatus):$($_.errorCode):$($_.errorType)"
      }) -join ","
    throw "$SDK SDK acceptance failed: $failed"
  }
  return $summary
}

function Invoke-ProviderCanary {
  param(
    [Parameter(Mandatory = $true)][string] $CanaryBinary,
    [Parameter(Mandatory = $true)][string] $Secret,
    [Parameter(Mandatory = $true)][string] $Kind,
    [Parameter(Mandatory = $true)][string] $ProviderBaseURL,
    [Parameter(Mandatory = $true)][string] $Model,
    [Parameter(Mandatory = $true)][string] $Scenarios
  )

  $previousPreference = $ErrorActionPreference
  try {
    $ErrorActionPreference = "Continue"
    $output = $Secret | & $CanaryBinary -kind $Kind -base-url $ProviderBaseURL -model $Model `
      -scenarios $Scenarios -request-timeout 120s `
      -allowed-resolved-networks "198.18.0.0/15,fdfe:dcba:9876::/48" 2>$null
  } finally {
    $ErrorActionPreference = $previousPreference
  }
  if (@($output).Count -ne 1) { throw "Provider canary did not return one redacted summary." }
  return [string]($output | Select-Object -First 1) | ConvertFrom-Json
}

function Get-GatewayRetryDelaySeconds {
  param([Parameter(Mandatory = $true)] $Response)

  $value = [string]$Response.Headers["Retry-After"]
  if (-not $value) { return $null }
  $seconds = 0.0
  if ([double]::TryParse($value, [ref]$seconds)) {
    if ($seconds -lt 0 -or $seconds -gt 180) { return $null }
    return [Math]::Max($seconds + 0.25, 0.25)
  }
  $deadline = [DateTimeOffset]::MinValue
  if ([DateTimeOffset]::TryParse($value, [ref]$deadline)) {
    $delay = ($deadline.ToUniversalTime() - [DateTimeOffset]::UtcNow).TotalSeconds + 1.0
    if ($delay -ge 0 -and $delay -le 180) { return [Math]::Max($delay, 0.25) }
  }
  return $null
}

function Invoke-GatewayJSONWithExplicitReissue {
  param(
    [Parameter(Mandatory = $true)][string] $Path,
    [Parameter(Mandatory = $true)][string] $Body,
    [int] $MaxAttempts = 4
  )

  for ($attempt = 1; $attempt -le $MaxAttempts; $attempt++) {
    try {
      $data = Invoke-RestMethod -Method Post -Uri "$script:BaseURL$Path" `
        -Headers @{ Authorization = "Bearer $script:GatewayKey" } -ContentType "application/json" -Body $Body -TimeoutSec 180
      return [pscustomobject]@{ Succeeded = $true; Data = $data; HTTPStatus = 200; ErrorCode = "" }
    } catch {
      $response = $_.Exception.Response
      if ($null -eq $response) { throw }
      $status = [int]$response.StatusCode
      $code = ""
      try {
        $problem = $_.ErrorDetails.Message | ConvertFrom-Json
        $code = [string]$problem.error.code
      } catch {
        $code = ""
      }
      $retryable = $status -eq 429 -or
        ($status -eq 409 -and $code -eq "upstream_outcome_uncertain") -or
        ($status -eq 503 -and @("upstream_circuit_open", "free_pool_unavailable", "503") -contains $code)
      $delaySeconds = Get-GatewayRetryDelaySeconds -Response $response
      if (-not $retryable -or $null -eq $delaySeconds) { throw }
      if ($attempt -eq $MaxAttempts) {
        return [pscustomobject]@{ Succeeded = $false; Data = $null; HTTPStatus = $status; ErrorCode = $code }
      }
      Start-Sleep -Milliseconds ([int][Math]::Ceiling($delaySeconds * 1000))
    }
  }
  throw "Explicit Gateway reissue ended without a result."
}

$root = Split-Path -Parent $PSScriptRoot
$runID = New-LLMGatewayTestRunID -Purpose "provider"
$buildDirectory = Join-Path $root ".build\provider-real-$runID"
$isWindows = $env:OS -eq "Windows_NT"
$binaryName = if ($isWindows) { "gateway.exe" } else { "gateway" }
$binaryPath = Join-Path $buildDirectory $binaryName
$canaryBinaryName = if ($isWindows) { "providercanary.exe" } else { "providercanary" }
$canaryBinary = Join-Path $buildDirectory $canaryBinaryName
$stdoutPath = Join-Path $buildDirectory "gateway.stdout.log"
$stderrPath = Join-Path $buildDirectory "gateway.stderr.log"
$pythonEnvironment = Join-Path $buildDirectory "python"
$pythonPath = if ($isWindows) { Join-Path $pythonEnvironment "Scripts\python.exe" } else { Join-Path $pythonEnvironment "bin/python" }
$environmentSnapshot = Save-LLMGatewayEnvironment
$postgres = $null
$valkey = $null
$gatewayProcess = $null
$acceptancePassed = $false
$failure = $null
$cleanupFailures = [System.Collections.Generic.List[string]]::new()

Push-Location $root
try {
  Clear-LLMGatewayEnvironment
  New-Item -ItemType Directory -Force $buildDirectory | Out-Null
  $zhipuLabelPrefix = [string]::Concat([char]0x667A, [char]0x8C31)
  $keyLines = @(Get-Content -Encoding UTF8 -LiteralPath (Join-Path $root "key.txt") | ForEach-Object { $_.Trim() } | Where-Object { $_ })
  $keyEntries = @($keyLines | ForEach-Object {
      $segments = @($_.Split([char]0xFF1A) | ForEach-Object { $_.Trim() })
      if ($segments[0] -notmatch '^(agnes[1-3]|gemini1)$' -and $segments[0] -notmatch "^$zhipuLabelPrefix[1-3]$") {
        return
      }
      if ($segments.Count -lt 2 -or @($segments | Where-Object { -not $_ }).Count -ne 0) {
        throw "Each named real Provider credential must contain a label and a final secret segment."
      }
      [pscustomobject]@{ Label = $segments[0]; Secret = $segments[-1] }
    })
  $siliconKeys = @($keyLines | Where-Object { $_ -match '^sk-[A-Za-z0-9_-]{20,}$' })
  $keyLines = $null
  $keys = @($keyEntries | ForEach-Object { $_.Secret })
  $namedCredentialCount = @($keys).Count
  $siliconCredentialCount = @($siliconKeys).Count
  $shortCredentialCount = @((@($keys) + @($siliconKeys)) | Where-Object { $_.Length -lt 20 }).Count
  if ($namedCredentialCount -ne 7 -or $siliconCredentialCount -ne 1 -or $shortCredentialCount -ne 0) {
    throw "Real Provider acceptance credential counts are invalid (named=$namedCredentialCount, SiliconFlow=$siliconCredentialCount, short=$shortCredentialCount)."
  }

  & go build -trimpath -o $canaryBinary .\cmd\providercanary
  if ($LASTEXITCODE -ne 0) { throw "Could not build the Provider ownership canary." }
  $agnesKeys = [System.Collections.Generic.List[string]]::new()
  $zhipuKeys = [System.Collections.Generic.List[string]]::new()
  $geminiKeys = [System.Collections.Generic.List[string]]::new()
  foreach ($entry in $keyEntries) {
    if ($entry.Label -match '^agnes[1-3]$') {
      $agnesKeys.Add($entry.Secret)
    } elseif ($entry.Label -match "^$zhipuLabelPrefix[1-3]$") {
      $zhipuKeys.Add($entry.Secret)
    } elseif ($entry.Label -eq "gemini1") {
      $geminiKeys.Add($entry.Secret)
    } else {
      throw "A real Provider credential label is outside the expected six-label contract."
    }
  }
  if ($agnesKeys.Count -ne 3 -or $zhipuKeys.Count -ne 3 -or $geminiKeys.Count -ne 1) {
    throw "Credential labels did not classify three Agnes, three Zhipu, and one Gemini credential."
  }
  foreach ($secret in $agnesKeys) {
    $probe = Invoke-ProviderCanary -CanaryBinary $canaryBinary -Secret $secret -Kind "agnes" `
      -ProviderBaseURL "https://apihub.agnes-ai.com/v1" -Model "agnes-2.0-flash" -Scenarios "models"
    if (-not $probe.succeeded) { throw "An Agnes credential failed its official models probe." }
  }
  foreach ($secret in $zhipuKeys) {
    $probe = Invoke-ProviderCanary -CanaryBinary $canaryBinary -Secret $secret -Kind "zhipu" `
      -ProviderBaseURL "https://open.bigmodel.cn/api/paas/v4" -Model "glm-5.2" -Scenarios "models"
    if (-not $probe.succeeded) { throw "A Zhipu credential failed its official models probe." }
  }
  $geminiProbe = Invoke-ProviderCanary -CanaryBinary $canaryBinary -Secret $geminiKeys[0] -Kind "gemini" `
    -ProviderBaseURL "https://generativelanguage.googleapis.com/v1beta/openai" -Model "gemini-3.5-flash" -Scenarios "models"
  if (-not $geminiProbe.succeeded) { throw "The Gemini credential failed its official models probe." }
  $siliconProbe = Invoke-ProviderCanary -CanaryBinary $canaryBinary -Secret $siliconKeys[0] -Kind "openai-compatible" `
    -ProviderBaseURL "https://api.siliconflow.cn/v1" -Model "Qwen/Qwen3.5-9B" -Scenarios "models"
  if (-not $siliconProbe.succeeded) { throw "The SiliconFlow credential failed its official models probe." }

  $zhipuQuotaKeys = [System.Collections.Generic.List[string]]::new()
  $zhipuSuccessKeys = [System.Collections.Generic.List[string]]::new()
  foreach ($secret in $zhipuKeys) {
    $chatProbe = Invoke-ProviderCanary -CanaryBinary $canaryBinary -Secret $secret -Kind "zhipu" `
      -ProviderBaseURL "https://open.bigmodel.cn/api/paas/v4" -Model "glm-5.2" -Scenarios "chat"
    $scenario = @($chatProbe.scenarios | Select-Object -First 1)
    if ($chatProbe.succeeded) {
      $zhipuSuccessKeys.Add($secret)
    } elseif ($scenario.Count -eq 1 -and $scenario[0].errorKind -eq "quota") {
      $zhipuQuotaKeys.Add($secret)
    } else {
      throw "A Zhipu test credential returned neither success nor the expected quota fact."
    }
  }
  if ($zhipuQuotaKeys.Count -ne 2 -or $zhipuSuccessKeys.Count -ne 1) {
    throw "Zhipu canary did not confirm two quota credentials and one healthy credential."
  }

  $postgres = Start-LLMGatewayTestPostgres -RunID $runID -DatabaseName "llmgateway_provider" -Password "provider-postgres-fixture"
  $valkeyPassword = "provider-valkey-fixture"
  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $valkeyPassword
  $gatewayPort = Get-AcceptanceLoopbackPort
  $script:BaseURL = "http://127.0.0.1:$gatewayPort"

  $env:LLMGATEWAY_PROFILE = "test"
  $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$gatewayPort"
  $env:LLMGATEWAY_HTTP_IDLE_TIMEOUT = "180s"
  $env:LLMGATEWAY_DATABASE_URL = $postgres.DatabaseURL
  $env:LLMGATEWAY_DATABASE_MIGRATE_ON_START = "true"
  $env:LLMGATEWAY_VALKEY_ADDRESS = $valkey.Address
  $env:LLMGATEWAY_VALKEY_PASSWORD = $valkeyPassword
  $env:LLMGATEWAY_VALKEY_DATABASE = "0"
  $env:LLMGATEWAY_MASTER_KEYS = "1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  $env:LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION = "1"
  $env:LLMGATEWAY_SESSION_PEPPER = "llmgateway-provider-session-pepper-0000"
  $env:LLMGATEWAY_API_KEY_PEPPER = "llmgateway-provider-api-key-pepper-0000"
  $env:LLMGATEWAY_COORDINATION_KEY_HASH_SECRET = "llmgateway-provider-coordination-pepper"
  $env:LLMGATEWAY_COOKIE_SECURE = "false"
  $env:LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS = "198.18.0.0/15,fdfe:dcba:9876::/48"
  $env:LLMGATEWAY_PROVIDER_PROBE_TIMEOUT = "120s"
  $env:LLMGATEWAY_REQUEST_MAX_QUEUE_WAIT = "150s"
  $env:LLMGATEWAY_REQUEST_RETRY_MAX_ELAPSED = "120s"
  $env:LLMGATEWAY_LOG_LEVEL = "info"

  & pnpm.cmd --dir web run build
  if ($LASTEXITCODE -ne 0) { throw "Could not build the production frontend for real Provider acceptance." }
  & go build -tags webembed -trimpath -o $binaryPath .\cmd\gateway
  if ($LASTEXITCODE -ne 0) { throw "Could not build the real Provider gateway." }
  $gatewayProcess = Start-AcceptanceProcess -BinaryPath $binaryPath -StandardOutputPath $stdoutPath -StandardErrorPath $stderrPath
  Wait-AcceptanceReadiness -Process $gatewayProcess -ReadinessURL "$script:BaseURL/health/ready" -TimeoutSeconds 60

  $script:AdminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  $setup = Invoke-RestMethod -Method Post -Uri "$script:BaseURL/api/control/setup" -WebSession $script:AdminSession `
    -ContentType "application/json" -Body (@{
      email = "provider-owner@example.test"
      displayName = "Provider Owner"
      password = "correct horse battery staple"
    } | ConvertTo-Json) -TimeoutSec 30
  if ($setup.data.role -ne "administrator" -or -not $setup.data.csrfToken) {
    throw "Real Provider acceptance could not establish the administrator."
  }
  $script:AdminCSRF = $setup.data.csrfToken

  $agnes = New-Provider -Slug "real-agnes" -Name "Real Agnes" -Kind "agnes" -ProviderBaseURL "https://apihub.agnes-ai.com/v1"
  $compatible = New-Provider -Slug "real-compatible" -Name "Real Agnes Compatible" -Kind "openai-compatible" -ProviderBaseURL "https://apihub.agnes-ai.com/v1"
  $zhipu = New-Provider -Slug "real-zhipu" -Name "Real Zhipu" -Kind "zhipu" -ProviderBaseURL "https://open.bigmodel.cn/api/paas/v4"
  $gemini = New-Provider -Slug "real-gemini" -Name "Real Google Gemini" -Kind "gemini" -ProviderBaseURL "https://generativelanguage.googleapis.com/v1beta/openai"
  $silicon = New-Provider -Slug "real-siliconflow" -Name "Real SiliconFlow" -Kind "openai-compatible" -ProviderBaseURL "https://api.siliconflow.cn/v1"

  $agnesModel = New-Model -ProviderID $agnes.id -Alias "real-agnes-chat" -UpstreamModelID "agnes-2.0-flash" -ReasoningMode "toggle"
  $compatibleModel = New-Model -ProviderID $compatible.id -Alias "real-compatible-chat" -UpstreamModelID "agnes-2.0-flash"
  $zhipuModel = New-Model -ProviderID $zhipu.id -Alias "real-zhipu-chat" -UpstreamModelID "glm-5.2" -ReasoningMode "hybrid"
  $geminiModel = New-Model -ProviderID $gemini.id -Alias "real-gemini-chat" -UpstreamModelID "gemini-3.5-flash" `
    -ReasoningMode "effort" -InputPricePerMillionTokens "1.5" -OutputPricePerMillionTokens "9"
  $siliconModel = New-Model -ProviderID $silicon.id -Alias "real-siliconflow-chat" -UpstreamModelID "Qwen/Qwen3.5-9B" `
    -ReasoningMode "toggle" -Currency "CNY" -InputPricePerMillionTokens "1.5" -OutputPricePerMillionTokens "12"

  for ($index = 0; $index -lt 3; $index++) {
    New-Credential -ProviderID $agnes.id -Name "Agnes dedicated $($index + 1)" -Secret $agnesKeys[$index] `
      -ModelID $agnesModel.id -Priority (($index + 1) * 10) -Weight 100 | Out-Null
    New-Credential -ProviderID $compatible.id -Name "Agnes compatible $($index + 1)" -Secret $agnesKeys[$index] `
      -ModelID $compatibleModel.id -Priority (($index + 1) * 10) -Weight 100 | Out-Null
  }
  New-Credential -ProviderID $zhipu.id -Name "Zhipu quota 1" -Secret $zhipuQuotaKeys[0] -ModelID $zhipuModel.id -Priority 10 -Weight 100 | Out-Null
  New-Credential -ProviderID $zhipu.id -Name "Zhipu success" -Secret $zhipuSuccessKeys[0] -ModelID $zhipuModel.id -Priority 30 -Weight 100 | Out-Null
  New-Credential -ProviderID $zhipu.id -Name "Zhipu quota 3" -Secret $zhipuQuotaKeys[1] -ModelID $zhipuModel.id -Priority 20 -Weight 100 | Out-Null
  New-Credential -ProviderID $gemini.id -Name "Gemini dedicated 1" -Secret $geminiKeys[0] -ModelID $geminiModel.id -Priority 10 -Weight 100 | Out-Null
  New-Credential -ProviderID $silicon.id -Name "SiliconFlow dedicated 1" -Secret $siliconKeys[0] -ModelID $siliconModel.id -Priority 10 -Weight 100 | Out-Null

  Enable-Provider -Provider $agnes
  Enable-Provider -Provider $compatible
  Enable-Provider -Provider $zhipu
  Enable-Provider -Provider $gemini
  Enable-Provider -Provider $silicon

  $active = Invoke-RestMethod -Uri "$script:BaseURL/api/control/configuration/active" -WebSession $script:AdminSession -TimeoutSec 30
  $revision = Invoke-RestMethod -Method Post -Uri "$script:BaseURL/api/control/configuration/revisions" -WebSession $script:AdminSession `
    -Headers @{ "X-CSRF-Token" = $script:AdminCSRF; "Idempotency-Key" = [guid]::NewGuid().ToString() } -TimeoutSec 30
  if ($revision.data.providerCount -ne 5 -or $revision.data.modelCount -ne 5 -or $revision.data.credentialCount -ne 11 -or $revision.data.routeCount -ne 11) {
    throw "Real Provider configuration capture did not contain the complete four-kind catalog."
  }
  $publication = Invoke-ControlJSON -Method Post -Path "/api/control/configuration/revisions/$($revision.data.id)/publish" -Idempotent -Body @{
    expectedActiveVersion = [int64]$active.data.version
  }
  if ($publication.data.phase -ne "completed") { throw "Real Provider configuration publication did not complete." }

  foreach ($model in @($agnesModel, $compatibleModel, $zhipuModel, $geminiModel, $siliconModel)) {
    New-Entitlement -OwnerID $setup.data.userId -ModelID $model.id
  }
  $keyResult = Invoke-ControlJSON -Method Post -Path "/api/control/keys" -Idempotent -Body @{
    ownerId = $setup.data.userId
    name = "Real Provider SDK"
    authorizedModelIds = @($agnesModel.id, $compatibleModel.id, $zhipuModel.id, $geminiModel.id, $siliconModel.id)
    expiresAt = $null
  }
  $script:GatewayKey = $keyResult.data.secret
  if (-not $script:GatewayKey -or -not $script:GatewayKey.StartsWith("llmg_")) {
    throw "Real Provider Gateway Key was not issued."
  }

  Push-Location (Join-Path $root "scripts\acceptance\openai-go")
  try {
    $goSummary = Invoke-SDKClient -SDK go -SuccessModel $siliconModel.alias -StreamModel $siliconModel.alias `
      -ReasoningMode "toggle" -ErrorModel $zhipuModel.alias -PythonPath $pythonPath
  } finally {
    Pop-Location
  }

  & python -m venv $pythonEnvironment
  if ($LASTEXITCODE -ne 0) { throw "Could not create the isolated Python SDK environment." }
  & $pythonPath -m pip install --disable-pip-version-check --requirement (Join-Path $root "scripts\acceptance\openai-python\requirements.txt") *> $null
  if ($LASTEXITCODE -ne 0) { throw "Could not install the pinned Python SDK." }
  Push-Location (Join-Path $root "scripts\acceptance\openai-python")
  try {
    $env:LLMGATEWAY_SDK_EXPLICIT_REISSUE = "true"
    $pythonSummary = Invoke-SDKClient -SDK python -SuccessModel $siliconModel.alias -StreamModel $agnesModel.alias `
      -ReasoningMode "toggle" -ErrorModel $zhipuModel.alias -PythonPath $pythonPath
  } finally {
    Pop-Location
  }

  $dedicatedToolBody = @{
    model = $agnesModel.alias
    messages = @(@{ role = "user"; content = "Call lookup for Beijing. Do not answer directly." })
    tools = @(@{ type = "function"; function = @{
          name = "lookup"; description = "Look up a city"
          parameters = @{ type = "object"; properties = @{ city = @{ type = "string" } }; required = @("city") }
        } })
    max_tokens = 64
  } | ConvertTo-Json -Depth 10 -Compress
  $dedicatedToolResult = Invoke-GatewayJSONWithExplicitReissue -Path "/v1/chat/completions" -Body $dedicatedToolBody
  if (-not $dedicatedToolResult.Succeeded) {
    throw "The dedicated Agnes tool request remained unavailable after bounded explicit reissue."
  }
  $dedicatedTool = $dedicatedToolResult.Data
  if (@($dedicatedTool.choices).Count -eq 0 -or @($dedicatedTool.choices[0].message.tool_calls).Count -eq 0) {
    throw "The dedicated Agnes adapter did not return an automatic tool call through the Gateway."
  }

  $dedicatedReasoningBody = @{
    model = $agnesModel.alias
    messages = @(@{ role = "user"; content = "Reply with exactly OK after thinking." })
    thinking = @{ type = "enabled" }
    max_tokens = 64
  } | ConvertTo-Json -Depth 6 -Compress
  $dedicatedReasoningResult = Invoke-GatewayJSONWithExplicitReissue -Path "/v1/chat/completions" -Body $dedicatedReasoningBody
  if (-not $dedicatedReasoningResult.Succeeded) {
    throw "The dedicated Agnes thinking request remained unavailable after bounded explicit reissue."
  }
  $dedicatedReasoning = $dedicatedReasoningResult.Data
  if (@($dedicatedReasoning.choices).Count -eq 0 -or $dedicatedReasoning.usage.total_tokens -lt 1) {
    throw "The dedicated Agnes thinking request did not complete with usage through the Gateway."
  }

  $compatibleChatBody = @{
    model = $compatibleModel.alias
    messages = @(@{ role = "user"; content = "Reply with exactly OK." })
    max_tokens = 256
  } | ConvertTo-Json -Depth 5 -Compress
  $compatibleChatResult = Invoke-GatewayJSONWithExplicitReissue -Path "/v1/chat/completions" -Body $compatibleChatBody
  if (-not $compatibleChatResult.Succeeded) {
    throw "The OpenAI-compatible chat remained unavailable after bounded explicit reissue."
  }
  $compatibleChat = $compatibleChatResult.Data
  if (@($compatibleChat.choices).Count -eq 0 -or $compatibleChat.usage.total_tokens -lt 1) {
    throw "The OpenAI-compatible adapter did not complete a real chat with usage."
  }

  $geminiTools = @(@{ type = "function"; function = @{
        name = "lookup"; description = "Look up a city"
        parameters = @{ type = "object"; properties = @{ city = @{ type = @("string", "null") } }; required = @("city", "unknown") }
      } })
  $geminiToolBody = @{
    model = $geminiModel.alias
    messages = @(@{ role = "user"; content = "Use the lookup tool for Beijing." })
    tools = $geminiTools
    tool_choice = "required"
    reasoning_effort = "low"
    max_tokens = 256
  } | ConvertTo-Json -Depth 12 -Compress
  $geminiAvailability = $null
  $geminiToolResult = Invoke-GatewayJSONWithExplicitReissue -Path "/v1/chat/completions" -Body $geminiToolBody
  if (-not $geminiToolResult.Succeeded) {
    $geminiAvailability = "$($geminiToolResult.HTTPStatus):$($geminiToolResult.ErrorCode)"
  } else {
    $geminiCall = @($geminiToolResult.Data.choices[0].message.tool_calls | Select-Object -First 1)
    if ($geminiCall.Count -ne 1 -or -not $geminiCall[0].extra_content.google.thought_signature) {
      throw "The Gemini adapter did not preserve the tool-call thought signature."
    }
    $geminiReplayBody = @{
      model = $geminiModel.alias
      messages = @(
        @{ role = "user"; content = "Use the lookup tool for Beijing." },
        @{ role = "assistant"; content = $null; tool_calls = @($geminiCall[0]) },
        @{ role = "tool"; tool_call_id = $geminiCall[0].id; content = "Beijing is sunny." }
      )
      tools = $geminiTools
      tool_choice = "auto"
      reasoning_effort = "low"
      max_tokens = 256
    } | ConvertTo-Json -Depth 14 -Compress
    $geminiReplayResult = Invoke-GatewayJSONWithExplicitReissue -Path "/v1/chat/completions" -Body $geminiReplayBody
    if (-not $geminiReplayResult.Succeeded) {
      $geminiAvailability = "$($geminiReplayResult.HTTPStatus):$($geminiReplayResult.ErrorCode)"
    } elseif (@($geminiReplayResult.Data.choices).Count -eq 0 -or $geminiReplayResult.Data.usage.total_tokens -lt 1) {
      throw "The Gemini adapter did not complete a signed tool-call replay."
    }
  }

  $chatBody = @{
    model = $zhipuModel.alias
    messages = @(@{ role = "user"; content = "Reply with exactly OK." })
    max_tokens = 32
  } | ConvertTo-Json -Depth 5 -Compress
  $zhipuSuccess = Invoke-RestMethod -Method Post -Uri "$script:BaseURL/v1/chat/completions" `
    -Headers @{ Authorization = "Bearer $script:GatewayKey" } -ContentType "application/json" -Body $chatBody -TimeoutSec 150
  if (-not $zhipuSuccess.id -or @($zhipuSuccess.choices).Count -eq 0 -or $zhipuSuccess.usage.total_tokens -lt 1) {
    throw "The remaining healthy Zhipu credential did not take over with authoritative usage."
  }

  $docker = Get-LLMGatewayDockerCommand
  $zhipuFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_provider -Atc `
    "SELECT string_agg(name || ':' || status::text || ':' || coalesce(last_error_kind, 'ok'), ',' ORDER BY name) FROM provider_credentials WHERE provider_id = '$($zhipu.id)'"
  if ($LASTEXITCODE -ne 0 -or $zhipuFacts -ne "Zhipu quota 1:cooling:quota,Zhipu quota 3:cooling:quota,Zhipu success:active:ok") {
    throw "Real Zhipu quota exclusion and healthy credential takeover did not persist."
  }
  $attemptFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_provider -Atc `
    "SELECT string_agg(credential.name || ':' || coalesce(attempt.error_kind, 'ok'), ',' ORDER BY attempt.created_at, attempt.id) FROM request_attempts attempt JOIN provider_credentials credential ON credential.id = attempt.credential_id JOIN requests request ON request.id = attempt.request_id WHERE request.model_id = '$($zhipuModel.id)'"
  if ($LASTEXITCODE -ne 0 -or $attemptFacts -ne "Zhipu quota 1:quota,Zhipu quota 3:quota,Zhipu success:ok") {
    throw "Real Zhipu attempt order did not prove priority, exclusion, and takeover."
  }

  if (-not $geminiAvailability) {
    $geminiCostFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_provider -Atc `
      "SELECT count(*) || '|' || sum(input_tokens) || '|' || sum(output_tokens) || '|' || sum(total_cost_nanos) || '|' || bool_and(cost_currency = 'USD' AND input_rate_nanos_per_million = 1500000000 AND output_rate_nanos_per_million = 9000000000 AND input_cost_nanos = ceil(input_tokens::numeric * 1500000000 / 1000000)::bigint AND output_cost_nanos = ceil(output_tokens::numeric * 9000000000 / 1000000)::bigint AND total_cost_nanos = input_cost_nanos + output_cost_nanos)::text FROM requests WHERE model_id = '$($geminiModel.id)' AND status = 'completed' AND usage_source = 'authoritative' AND total_cost_nanos IS NOT NULL"
    if ($LASTEXITCODE -ne 0) { throw "Could not read the real Gemini cost ledger." }
    $geminiCostSegments = @($geminiCostFacts.Split('|'))
    if ($geminiCostSegments.Count -ne 5 -or [int]$geminiCostSegments[0] -lt 2 -or $geminiCostSegments[4] -ne "true") {
      throw "Real Gemini authoritative usage did not reconcile to the frozen official paid-tier cost snapshot: $geminiCostFacts"
    }
    $geminiSummary = Invoke-RestMethod -Uri "$script:BaseURL/api/control/costs?search=real-gemini-chat&page=1&pageSize=20" -WebSession $script:AdminSession -TimeoutSec 30
    $geminiSummaryItem = @($geminiSummary.data.items | Where-Object { $_.modelId -eq $geminiModel.id }) | Select-Object -First 1
    if (-not $geminiSummaryItem -or $geminiSummaryItem.totalCostNanos -ne $geminiCostSegments[3]) {
      throw "Administrator cost aggregation did not reconcile to the real Gemini request ledger."
    }
    $costEvidenceDirectory = Join-Path $root ".build\acceptance-evidence"
    New-Item -ItemType Directory -Force $costEvidenceDirectory | Out-Null
    $costReport = [ordered]@{
      provider = "Google Gemini"
      model = "gemini-3.5-flash"
      officialPricingURL = "https://ai.google.dev/gemini-api/docs/pricing"
      pricingSnapshotDate = "2026-07-22"
      pricingTier = "standard-paid"
      currency = "USD"
      inputPricePerMillionTokens = "1.5"
      outputPricePerMillionTokens = "9"
      authoritativeRequests = [int64]$geminiCostSegments[0]
      inputTokens = [int64]$geminiCostSegments[1]
      outputTokens = [int64]$geminiCostSegments[2]
      totalCostNanos = [string]$geminiCostSegments[3]
      aggregateReconciled = $true
      customerContractMargin = "not_provided"
    }
    [IO.File]::WriteAllText((Join-Path $costEvidenceDirectory "provider-cost-report.json"), ($costReport | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
  }

  $siliconCostFacts = & $docker exec $postgres.Container psql -v ON_ERROR_STOP=1 -U llmgateway -d llmgateway_provider -Atc `
    "SELECT count(*) || '|' || sum(input_tokens) || '|' || sum(output_tokens) || '|' || sum(total_cost_nanos) || '|' || bool_and(cost_currency = 'CNY' AND input_rate_nanos_per_million = 1500000000 AND output_rate_nanos_per_million = 12000000000 AND input_cost_nanos = ceil(input_tokens::numeric * 1500000000 / 1000000)::bigint AND output_cost_nanos = ceil(output_tokens::numeric * 12000000000 / 1000000)::bigint AND total_cost_nanos = input_cost_nanos + output_cost_nanos)::text FROM requests WHERE model_id = '$($siliconModel.id)' AND status = 'completed' AND usage_source = 'authoritative' AND total_cost_nanos IS NOT NULL"
  if ($LASTEXITCODE -ne 0) { throw "Could not read the real SiliconFlow cost ledger." }
  $siliconCostSegments = @($siliconCostFacts.Split('|'))
  if ($siliconCostSegments.Count -ne 5 -or [int]$siliconCostSegments[0] -lt 2 -or $siliconCostSegments[4] -ne "true") {
    throw "Real SiliconFlow authoritative usage did not reconcile to the frozen official cost snapshot: $siliconCostFacts"
  }
  $siliconSummary = Invoke-RestMethod -Uri "$script:BaseURL/api/control/costs?search=real-siliconflow-chat&page=1&pageSize=20" -WebSession $script:AdminSession -TimeoutSec 30
  $siliconSummaryItem = @($siliconSummary.data.items | Where-Object { $_.modelId -eq $siliconModel.id }) | Select-Object -First 1
  if (-not $siliconSummaryItem -or $siliconSummaryItem.totalCostNanos -ne $siliconCostSegments[3]) {
    throw "Administrator cost aggregation did not reconcile to the real SiliconFlow request ledger."
  }
  $costEvidenceDirectory = Join-Path $root ".build\acceptance-evidence"
  New-Item -ItemType Directory -Force $costEvidenceDirectory | Out-Null
  $costReport = [ordered]@{
    provider = "SiliconFlow"
    model = "Qwen/Qwen3.5-9B"
    officialPricingURL = "https://siliconflow.cn/pricing"
    pricingSnapshotDate = "2026-07-22"
    currency = "CNY"
    inputPricePerMillionTokens = "1.5"
    outputPricePerMillionTokens = "12"
    authoritativeRequests = [int64]$siliconCostSegments[0]
    inputTokens = [int64]$siliconCostSegments[1]
    outputTokens = [int64]$siliconCostSegments[2]
    totalCostNanos = [string]$siliconCostSegments[3]
    aggregateReconciled = $true
    customerContractMargin = "not_provided"
  }
  [IO.File]::WriteAllText((Join-Path $costEvidenceDirectory "provider-cost-report.json"), ($costReport | ConvertTo-Json), [Text.UTF8Encoding]::new($false))

  foreach ($secret in $keys) {
    if (Select-String -LiteralPath @($stdoutPath, $stderrPath) -SimpleMatch -Quiet -Pattern $secret) {
      throw "A real Provider credential appeared in a Gateway runtime log."
    }
  }
  if (@($goSummary.scenarios).Count -ne 8 -or @($pythonSummary.scenarios).Count -ne 7) {
    throw "The standard SDK summaries did not cover the complete scenario set."
  }
  $acceptancePassed = $true
} catch {
  $failure = $_
} finally {
  $script:GatewayKey = $null
  $keys = $null
  $keyEntries = $null
  $agnesKeys = $null
  $zhipuKeys = $null
  $geminiKeys = $null
  $siliconKeys = $null
  $zhipuQuotaKeys = $null
  $zhipuSuccessKeys = $null
  try { Stop-AcceptanceProcess -Process $gatewayProcess -ExpectedBinaryPath $binaryPath } catch { $cleanupFailures.Add($_.Exception.Message) }
  if ($null -ne $valkey) {
    try { Stop-LLMGatewayTestContainer -Container $valkey.Container -RunID $runID } catch { $cleanupFailures.Add($_.Exception.Message) }
  }
  if ($null -ne $postgres) {
    try { Stop-LLMGatewayTestContainer -Container $postgres.Container -RunID $runID } catch { $cleanupFailures.Add($_.Exception.Message) }
  }
  Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot
  Pop-Location
  if ($acceptancePassed -and $cleanupFailures.Count -eq 0) {
    try { Remove-AcceptanceBuildDirectory -RepositoryRoot $root -BuildDirectory $buildDirectory -ExpectedPrefix "provider-real-" } catch { $cleanupFailures.Add($_.Exception.Message) }
  }
}

if ($null -ne $failure) {
  if ($cleanupFailures.Count -gt 0) { throw "$($failure.Exception.Message) Cleanup: $($cleanupFailures -join '; ')" }
  throw $failure
}
if ($cleanupFailures.Count -gt 0) { throw "Real Provider acceptance cleanup failed: $($cleanupFailures -join '; ')" }
if (-not $acceptancePassed) { throw "Real Provider acceptance ended without a result." }

Write-Host "Real Agnes, Zhipu, OpenAI-compatible, Go SDK, Python SDK, quota exclusion, and healthy takeover acceptance passed."
if ($geminiAvailability) {
  Write-Host "Gemini live generation remained temporarily unavailable after bounded explicit reissue ($geminiAvailability); its retryable Gateway error contract passed."
} else {
  Write-Host "Gemini thought-signature and signed tool replay acceptance passed."
}
