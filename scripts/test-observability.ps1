[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

function Get-FreeLoopbackPort {
  $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
  $listener.Start()
  try { return ([Net.IPEndPoint]$listener.LocalEndpoint).Port } finally { $listener.Stop() }
}

$root = Split-Path -Parent $PSScriptRoot
$rulesPath = [IO.Path]::GetFullPath((Join-Path $root "deploy\observability\prometheus-rules.yaml"))
$dashboardPath = [IO.Path]::GetFullPath((Join-Path $root "deploy\observability\grafana-dashboard.json"))
$validationDirectory = [IO.Path]::GetFullPath((Join-Path $root ".build\observability-$([guid]::NewGuid().ToString('N'))"))
$docker = Get-LLMGatewayDockerCommand
$containerName = "llmgateway-observability-$([guid]::NewGuid().ToString('N'))"
$grafanaPort = Get-FreeLoopbackPort
$grafanaPassword = "observability-$([guid]::NewGuid().ToString('N'))"
$failure = $null

try {
  New-Item -ItemType Directory -Force -Path $validationDirectory | Out-Null
  $prometheusAsset = if ($env:OS -eq "Windows_NT") { "prometheus-3.13.1.windows-amd64.zip" } else { "prometheus-3.13.1.linux-amd64.tar.gz" }
  $prometheusArchive = Join-Path $validationDirectory $prometheusAsset
  $prometheusChecksums = Join-Path $validationDirectory "sha256sums.txt"
  Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/prometheus/prometheus/releases/download/v3.13.1/$prometheusAsset" -OutFile $prometheusArchive -TimeoutSec 300
  Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/prometheus/prometheus/releases/download/v3.13.1/sha256sums.txt" -OutFile $prometheusChecksums -TimeoutSec 30
  $checksumLine = Get-Content -Encoding ascii -LiteralPath $prometheusChecksums | Where-Object { $_ -match " $([regex]::Escape($prometheusAsset))$" } | Select-Object -First 1
  $expectedChecksum = [string]($checksumLine -split '\s+')[0]
  $actualChecksum = (Get-FileHash -Algorithm SHA256 -LiteralPath $prometheusArchive).Hash.ToLowerInvariant()
  if (-not $expectedChecksum -or $actualChecksum -ne $expectedChecksum.ToLowerInvariant()) {
    throw "The Prometheus release checksum did not match the official checksum list."
  }
  if ($prometheusAsset.EndsWith(".zip")) {
    Expand-Archive -LiteralPath $prometheusArchive -DestinationPath $validationDirectory
  } else {
    & tar -xzf $prometheusArchive -C $validationDirectory
    if ($LASTEXITCODE -ne 0) { throw "Could not extract the fixed Prometheus release." }
  }
  $promtoolName = if ($env:OS -eq "Windows_NT") { "promtool.exe" } else { "promtool" }
  $promtool = Get-ChildItem -LiteralPath $validationDirectory -Recurse -File -Filter $promtoolName | Select-Object -First 1 -ExpandProperty FullName
  if (-not $promtool) { throw "The fixed Prometheus release did not contain promtool." }
  if ($env:OS -ne "Windows_NT") { & chmod 0755 $promtool }
  & $promtool check rules $rulesPath
  if ($LASTEXITCODE -ne 0) { throw "Prometheus rule validation failed." }

  $dashboard = Get-Content -Raw -Encoding utf8 -LiteralPath $dashboardPath | ConvertFrom-Json
  if ($dashboard.uid -ne "llmgateway-operations" -or @($dashboard.panels).Count -ne 6) {
    throw "The Grafana dashboard source does not contain the expected stable UID and six panels."
  }

  $containerID = [string](& $docker run --detach --rm --name $containerName `
    --label "llmgateway.test.owner=llmgateway" `
    --publish "127.0.0.1:${grafanaPort}:3000" `
    --env "GF_SECURITY_ADMIN_PASSWORD=$grafanaPassword" `
    --env "GF_USERS_ALLOW_SIGN_UP=false" `
    grafana/grafana:13.1.1)
  if ($LASTEXITCODE -ne 0 -or -not $containerID.Trim()) { throw "Could not start isolated Grafana." }

  $credentials = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("admin:$grafanaPassword"))
  $headers = @{ Authorization = "Basic $credentials" }
  $deadline = (Get-Date).AddSeconds(60)
  do {
    try {
      $health = Invoke-RestMethod -UseBasicParsing -Uri "http://127.0.0.1:$grafanaPort/api/health" -TimeoutSec 2
      if ($health.database -eq "ok") { break }
    } catch {}
    Start-Sleep -Milliseconds 250
  } while ((Get-Date) -lt $deadline)
  if ($null -eq $health -or $health.database -ne "ok") { throw "Isolated Grafana did not become ready." }

  $importBody = @{ dashboard = $dashboard; overwrite = $true } | ConvertTo-Json -Depth 100 -Compress
  $imported = Invoke-RestMethod -UseBasicParsing -Method Post -Uri "http://127.0.0.1:$grafanaPort/api/dashboards/db" `
    -Headers $headers -ContentType "application/json" -Body $importBody -TimeoutSec 10
  if ($imported.status -ne "success" -or $imported.uid -ne "llmgateway-operations") {
    throw "Grafana did not import the operations dashboard."
  }
  $loaded = Invoke-RestMethod -UseBasicParsing -Uri "http://127.0.0.1:$grafanaPort/api/dashboards/uid/llmgateway-operations" `
    -Headers $headers -TimeoutSec 10
  if ($loaded.dashboard.title -ne "LLMGateway Operations" -or @($loaded.dashboard.panels).Count -ne 6) {
    throw "Grafana did not return the imported dashboard contract."
  }
} catch {
  $failure = $_
} finally {
  $previousErrorPreference = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    $inspection = @(& $docker inspect $containerName 2>$null | ConvertFrom-Json)
    $inspectionExitCode = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $previousErrorPreference
  }
  if ($inspectionExitCode -eq 0 -and $inspection.Count -eq 1 -and $inspection[0].Config.Labels.'llmgateway.test.owner' -eq "llmgateway") {
    & $docker rm --force $containerName | Out-Null
  }
  $validationRoot = [IO.Path]::GetFullPath((Join-Path $root ".build"))
  if ($validationDirectory.StartsWith($validationRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -and
      (Split-Path -Leaf $validationDirectory).StartsWith("observability-", [StringComparison]::Ordinal) -and
      (Test-Path -LiteralPath $validationDirectory)) {
    Remove-Item -LiteralPath $validationDirectory -Recurse -Force
  }
}

if ($null -ne $failure) { throw $failure }
Write-Host "Prometheus rules and the Grafana operations dashboard passed fixed-version validation."
