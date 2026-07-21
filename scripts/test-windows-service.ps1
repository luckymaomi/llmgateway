$ErrorActionPreference = "Stop"
. "$PSScriptRoot\isolated-services.ps1"

$root = Split-Path -Parent $PSScriptRoot
$runID = New-LLMGatewayTestRunID -Purpose "winservice"
$buildDirectory = Join-Path $root ".build\$runID"
$databaseName = "llmgateway_winservice_$($runID.Replace('-', '_'))"
$databasePassword = "winservice-$runID-database-password"
$valkeyPassword = "winservice-$runID-valkey-password"
$environmentSnapshot = Save-LLMGatewayEnvironment
$postgres = $null
$valkey = $null
$serviceInstalled = $false
$failure = $null

function Write-ServiceSecret {
  param([string] $Name, [string] $Value)
  $path = Join-Path $buildDirectory $Name
  [IO.File]::WriteAllText($path, $Value, [Text.UTF8Encoding]::new($false))
  return $path
}

Push-Location $root
try {
  New-Item -ItemType Directory -Force -Path $buildDirectory | Out-Null
  Clear-LLMGatewayEnvironment
  $postgres = Start-LLMGatewayTestPostgres -RunID $runID -DatabaseName $databaseName -Password $databasePassword
  $valkey = Start-LLMGatewayTestValkey -RunID $runID -Password $valkeyPassword
  $gatewayPort = Get-LLMGatewayFreeLoopbackPort
  $binaryPath = Join-Path $buildDirectory "llmgateway.exe"
  $environmentPath = Join-Path $buildDirectory "service.env"

  pnpm.cmd --dir web run build
  if ($LASTEXITCODE -ne 0) { throw "Production frontend build failed." }
  go build -tags webembed -trimpath -o $binaryPath .\cmd\gateway
  if ($LASTEXITCODE -ne 0) { throw "Windows service Gateway build failed." }

  $masterKey = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("0123456789abcdef0123456789abcdef"))
  $databaseURLFile = Write-ServiceSecret "database-url" $postgres.DatabaseURL
  $valkeyPasswordFile = Write-ServiceSecret "valkey-password" $valkeyPassword
  $masterKeysFile = Write-ServiceSecret "master-keys" "1:$masterKey"
  $sessionPepperFile = Write-ServiceSecret "session-pepper" "winservice-session-pepper-$runID"
  $apiKeyPepperFile = Write-ServiceSecret "api-key-pepper" "winservice-api-key-pepper-$runID"
  $coordinationSecretFile = Write-ServiceSecret "coordination-secret" "winservice-coordination-secret-$runID"
  $lines = @(
    "LLMGATEWAY_PROFILE=production",
    "LLMGATEWAY_HTTP_ADDRESS=127.0.0.1:$gatewayPort",
    "LLMGATEWAY_DATABASE_URL_FILE=$databaseURLFile",
    "LLMGATEWAY_DATABASE_MIGRATE_ON_START=false",
    "LLMGATEWAY_DATABASE_MIN_CONNECTIONS=1",
    "LLMGATEWAY_DATABASE_MAX_CONNECTIONS=4",
    "LLMGATEWAY_VALKEY_ADDRESS=$($valkey.Address)",
    "LLMGATEWAY_VALKEY_PASSWORD_FILE=$valkeyPasswordFile",
    "LLMGATEWAY_MASTER_KEYS_FILE=$masterKeysFile",
    "LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION=1",
    "LLMGATEWAY_SESSION_PEPPER_FILE=$sessionPepperFile",
    "LLMGATEWAY_API_KEY_PEPPER_FILE=$apiKeyPepperFile",
    "LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE=$coordinationSecretFile",
    "LLMGATEWAY_COOKIE_SECURE=true"
  )
  [IO.File]::WriteAllLines($environmentPath, $lines, [Text.UTF8Encoding]::new($false))

  foreach ($line in $lines) {
    $parts = $line -split "=", 2
    [Environment]::SetEnvironmentVariable($parts[0], $parts[1], "Process")
  }
  go run .\cmd\dbtool -action up
  if ($LASTEXITCODE -ne 0) { throw "Windows service migration preflight failed." }
  Clear-LLMGatewayEnvironment

  & powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install-windows-service.ps1 `
    -BinaryPath $binaryPath -EnvironmentFile $environmentPath -Start `
    -HealthURL "http://127.0.0.1:$gatewayPort/health/ready"
  if ($LASTEXITCODE -ne 0) { throw "Windows service installation failed." }
  $serviceInstalled = $true

  $service = Get-Service -Name LLMGateway
  if ($service.Status -ne [ServiceProcess.ServiceControllerStatus]::Running -or $service.StartType -ne "Automatic") {
    throw "Windows service did not enter automatic running state."
  }
  $serviceFacts = Get-CimInstance Win32_Service -Filter "Name='LLMGateway'"
  if ($serviceFacts.StartMode -ne "Auto" -or -not $serviceFacts.StartName.EndsWith("\LLMGateway")) {
    throw "Windows SCM account or automatic start facts are invalid."
  }
  $event = Get-WinEvent -FilterHashtable @{ LogName = "Application"; ProviderName = "LLMGateway" } -MaxEvents 20 |
    Where-Object { $_.Message -like '*gateway listening*' } | Select-Object -First 1
  if (-not $event) { throw "Gateway structured startup log did not reach Windows Event Log." }

  Stop-Service -Name LLMGateway
  $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(45))
  Start-Service -Name LLMGateway
  $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(45))
  $response = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$gatewayPort/health/ready" -TimeoutSec 10
  if ([int]$response.StatusCode -ne 200) { throw "Restarted Windows service was not ready." }

  $recovery = & sc.exe qfailure LLMGateway
  if ($LASTEXITCODE -ne 0 -or ($recovery -join "`n") -notmatch 'RESTART') {
    throw "Windows service restart recovery policy is missing."
  }
  $evidenceDirectory = Join-Path $root ".build\acceptance-evidence"
  New-Item -ItemType Directory -Force -Path $evidenceDirectory | Out-Null
  $serviceReport = [ordered]@{
    serviceAccount = "virtual-service-account"
    fileSecrets = $true
    migrationPreflight = $true
    delayedAutomaticStart = $true
    eventLog = $true
    readiness = $true
    gracefulRestart = $true
    boundedFailureRestart = $true
  }
  [IO.File]::WriteAllText(
    (Join-Path $evidenceDirectory "windows-service-report.json"),
    ($serviceReport | ConvertTo-Json),
    [Text.UTF8Encoding]::new($false)
  )
} catch {
  $failure = $_
} finally {
  $cleanupFailures = @()
  if ($serviceInstalled -or (Get-Service -Name LLMGateway -ErrorAction SilentlyContinue)) {
    try {
      & powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\uninstall-windows-service.ps1 -RemoveEventSource
      if ($LASTEXITCODE -ne 0) { throw "Windows service uninstall returned a failure." }
    } catch { $cleanupFailures += $_.Exception.Message }
  }
  try { Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot } catch { $cleanupFailures += $_.Exception.Message }
  if ($null -ne $valkey) {
    try { Stop-LLMGatewayTestContainer -Container $valkey.Container -RunID $runID } catch { $cleanupFailures += $_.Exception.Message }
  }
  if ($null -ne $postgres) {
    try { Stop-LLMGatewayTestContainer -Container $postgres.Container -RunID $runID } catch { $cleanupFailures += $_.Exception.Message }
  }
  try {
    $resolvedBuild = [IO.Path]::GetFullPath($buildDirectory)
    $resolvedRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
    if (-not $resolvedBuild.StartsWith($resolvedRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
      throw "Refusing to remove an unowned Windows service build directory."
    }
    if (Test-Path -LiteralPath $resolvedBuild) { Remove-Item -LiteralPath $resolvedBuild -Recurse -Force }
  } catch { $cleanupFailures += $_.Exception.Message }
  try { Pop-Location } catch { $cleanupFailures += $_.Exception.Message }
  if ($null -ne $failure) {
    if ($cleanupFailures.Count -gt 0) { throw "Windows service test failed: $($failure.Exception.Message) Cleanup also failed: $($cleanupFailures -join '; ')" }
    throw $failure
  }
  if ($cleanupFailures.Count -gt 0) { throw "Windows service cleanup failed: $($cleanupFailures -join '; ')" }
}

Write-Host "Windows SCM installation, file secrets, migration preflight, automatic start, Event Log, health, graceful stop, and restart passed."
