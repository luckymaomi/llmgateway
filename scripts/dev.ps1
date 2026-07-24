[CmdletBinding()]
param(
  [switch] $OpenBrowser,
  [switch] $InfrastructureOnly,
  [ValidateRange(1, 65535)]
  [int] $GatewayPort = 8080,
  [ValidateRange(1, 65535)]
  [int] $WebPort = 5173
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

function Assert-LLMGatewayLoopbackPortAvailable {
  param(
    [Parameter(Mandatory = $true)][int] $Port,
    [Parameter(Mandatory = $true)][string] $Label
  )

  $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $Port)
  try {
    $listener.Start()
  } catch {
    throw "$Label port 127.0.0.1:$Port is unavailable. Run python .\stop_dev.py to stop an old LLMGateway run. If the port belongs to another program, pass a different port to start_dev.py."
  } finally {
    $listener.Stop()
  }
}

function Assert-LLMGatewayComposeContainerOwnership {
  param(
    [Parameter(Mandatory = $true)][string] $Container,
    [Parameter(Mandatory = $true)][string] $Service
  )

  $docker = Get-LLMGatewayDockerCommand
  $encoded = & $docker inspect --format '{{json .Config.Labels}}' $Container
  if ($LASTEXITCODE -ne 0 -or -not $encoded) {
    throw "Could not inspect the ownership of $Container."
  }
  $labels = ConvertFrom-Json -InputObject ($encoded -join "")
  if ($labels.'com.docker.compose.project' -ne "llmgateway" -or
      $labels.'com.docker.compose.service' -ne $Service) {
    throw "Refusing to use $Container because it is not owned by the LLMGateway Compose project."
  }
}

function Get-LLMGatewayComposeConfiguration {
  $docker = Get-LLMGatewayDockerCommand
  $encoded = & $docker compose config --format json
  if ($LASTEXITCODE -ne 0 -or -not $encoded) {
    throw "Could not resolve the Docker Compose configuration."
  }
  return ConvertFrom-Json -InputObject ($encoded -join "")
}

function Assert-LLMGatewayComposeLoopbackBinding {
  param(
    [Parameter(Mandatory = $true)] $Configuration,
    [Parameter(Mandatory = $true)][string] $Service,
    [Parameter(Mandatory = $true)][int] $Target
  )

  $serviceProperty = $Configuration.services.PSObject.Properties[$Service]
  if ($null -eq $serviceProperty) {
    throw "Compose does not define the required $Service service."
  }
  $ports = @($serviceProperty.Value.ports | Where-Object {
      [int] $_.target -eq $Target -and $_.protocol -eq "tcp"
    })
  if ($ports.Count -ne 1 -or $ports[0].host_ip -ne "127.0.0.1") {
    throw "Refusing to start ${Service}: TCP $Target must publish exactly once on 127.0.0.1."
  }
}

function Get-LLMGatewayLoopbackContainerEndpoint {
  param(
    [Parameter(Mandatory = $true)][string] $Container,
    [Parameter(Mandatory = $true)][string] $Target
  )

  $docker = Get-LLMGatewayDockerCommand
  $bindings = @(& $docker port $Container $Target)
  if ($LASTEXITCODE -ne 0 -or $bindings.Count -ne 1) {
    throw "Expected one published loopback endpoint for $Container $Target."
  }
  $match = [regex]::Match($bindings[0].Trim(), '^127\.0\.0\.1:(\d+)$')
  if (-not $match.Success) {
    throw "Refusing to use the non-loopback endpoint published by ${Container}: $($bindings[0])"
  }
  return "127.0.0.1:$($match.Groups[1].Value)"
}

function Get-LLMGatewayContainerEnvironment {
  param([Parameter(Mandatory = $true)][string] $Container)

  $docker = Get-LLMGatewayDockerCommand
  $encoded = & $docker inspect --format '{{json .Config.Env}}' $Container
  if ($LASTEXITCODE -ne 0 -or -not $encoded) {
    throw "Could not inspect the runtime configuration of $Container."
  }
  $values = @{}
  foreach ($entry in (ConvertFrom-Json -InputObject ($encoded -join ""))) {
    $separator = $entry.IndexOf('=')
    if ($separator -gt 0) {
      $values[$entry.Substring(0, $separator)] = $entry.Substring($separator + 1)
    }
  }
  return $values
}

function Get-LLMGatewayRequiredEnvironmentValue {
  param(
    [Parameter(Mandatory = $true)][hashtable] $Values,
    [Parameter(Mandatory = $true)][string] $Name,
    [Parameter(Mandatory = $true)][string] $Container
  )

  if (-not $Values.ContainsKey($Name) -or [string]::IsNullOrWhiteSpace($Values[$Name])) {
    throw "$Container does not expose the required $Name development fact."
  }
  return [string] $Values[$Name]
}

function Save-LLMGatewayProcessEnvironment {
  $snapshot = @{}
  foreach ($item in Get-ChildItem Env: | Where-Object {
      $_.Name -like "LLMGATEWAY_*" -or $_.Name -like "VITE_*"
    }) {
    $snapshot[$item.Name] = $item.Value
  }
  return $snapshot
}

function Clear-LLMGatewayProcessEnvironment {
  foreach ($item in @(Get-ChildItem Env: | Where-Object {
        $_.Name -like "LLMGATEWAY_*" -or $_.Name -like "VITE_*"
      })) {
    [Environment]::SetEnvironmentVariable($item.Name, $null, "Process")
  }
}

function Restore-LLMGatewayProcessEnvironment {
  param([Parameter(Mandatory = $true)][hashtable] $Snapshot)

  Clear-LLMGatewayProcessEnvironment
  foreach ($name in $Snapshot.Keys) {
    [Environment]::SetEnvironmentVariable($name, $Snapshot[$name], "Process")
  }
}

function Save-LLMGatewayNamedEnvironment {
  param([Parameter(Mandatory = $true)][string[]] $Names)

  $environment = [Environment]::GetEnvironmentVariables("Process")
  $snapshot = @{}
  foreach ($name in $Names) {
    $snapshot[$name] = [pscustomobject]@{
      Exists = $environment.Contains($name)
      Value  = [Environment]::GetEnvironmentVariable($name, "Process")
    }
  }
  return $snapshot
}

function Restore-LLMGatewayNamedEnvironment {
  param([Parameter(Mandatory = $true)][hashtable] $Snapshot)

  foreach ($name in $Snapshot.Keys) {
    $item = $Snapshot[$name]
    [Environment]::SetEnvironmentVariable(
      $name,
      $(if ($item.Exists) { $item.Value } else { $null }),
      "Process"
    )
  }
}

function Wait-LLMGatewayHTTPReady {
  param(
    [Parameter(Mandatory = $true)][string] $URL,
    [Parameter(Mandatory = $true)][System.Diagnostics.Process] $Process,
    [Parameter(Mandatory = $true)][string] $Label,
    [int] $TimeoutSeconds = 60
  )

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    if ($Process.HasExited) {
      throw "$Label exited before readiness with code $($Process.ExitCode)."
    }
    try {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $URL -TimeoutSec 2
      if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300) {
        return
      }
    } catch {
      # The listener becomes observable through its real HTTP endpoint.
    }
    Start-Sleep -Milliseconds 200
  } while ((Get-Date) -lt $deadline)

  throw "Timed out waiting for $Label at $URL."
}

function Stop-LLMGatewayOwnedProcess {
  param(
    [System.Diagnostics.Process] $Process,
    [Parameter(Mandatory = $true)][string] $Label
  )

  if ($null -eq $Process -or $Process.HasExited) {
    return
  }
  Write-Host "Stopping $Label (PID $($Process.Id))..."
  Stop-Process -Id $Process.Id -ErrorAction SilentlyContinue
  try {
    if (-not $Process.WaitForExit(5000)) {
      Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue
      [void] $Process.WaitForExit(5000)
    }
  } catch {
    if (-not $Process.HasExited) {
      throw "Could not stop the owned $Label process $($Process.Id)."
    }
  }
}

function Remove-LLMGatewayOwnedRunDirectory {
  param(
    [string] $RunDirectory,
    [Parameter(Mandatory = $true)][string] $BuildRoot
  )

  if (-not $RunDirectory -or -not (Test-Path -LiteralPath $RunDirectory)) {
    return
  }
  $resolvedBuildRoot = [System.IO.Path]::GetFullPath($BuildRoot).TrimEnd('\') + '\'
  $resolvedRunDirectory = [System.IO.Path]::GetFullPath($RunDirectory)
  if (-not $resolvedRunDirectory.StartsWith(
      $resolvedBuildRoot,
      [System.StringComparison]::OrdinalIgnoreCase
    )) {
    throw "Refusing to remove a development run directory outside .build."
  }
  Remove-Item -LiteralPath $resolvedRunDirectory -Recurse -Force
}

if ($GatewayPort -eq $WebPort) {
  throw "GatewayPort and WebPort must be different."
}
if ($InfrastructureOnly -and $OpenBrowser) {
  throw "OpenBrowser cannot be used with InfrastructureOnly."
}

$root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$buildRoot = Join-Path $root ".build"
$gatewayProcess = $null
$webProcess = $null
$runDirectory = $null

Push-Location $root
try {
  if (-not $InfrastructureOnly) {
    Assert-LLMGatewayLoopbackPortAvailable -Port $GatewayPort -Label "Gateway"
    Assert-LLMGatewayLoopbackPortAvailable -Port $WebPort -Label "Web"

    $goCommand = Get-Command go -CommandType Application -ErrorAction SilentlyContinue
    if (-not $goCommand) {
      throw "Go was not found. Install the toolchain declared by go.mod and reopen the terminal."
    }
    $goHostOS = (@(& $goCommand.Source env GOHOSTOS) -join "").Trim()
    $goHostArch = (@(& $goCommand.Source env GOHOSTARCH) -join "").Trim()
    if ($LASTEXITCODE -ne 0 -or $goHostOS -ne "windows" -or
        $goHostArch -notin @("amd64", "arm64")) {
      throw "dev.ps1 requires a native Windows Go toolchain for amd64 or arm64."
    }
    $nodeCommand = Get-Command node -CommandType Application -ErrorAction SilentlyContinue
    if (-not $nodeCommand) {
      throw "Node.js was not found. Install Node.js 22.12 or newer and reopen the terminal."
    }
    $pnpmCommand = Get-Command pnpm.cmd -CommandType Application -ErrorAction SilentlyContinue
    if (-not $pnpmCommand) {
      throw "pnpm.cmd was not found. Enable Corepack or install pnpm and reopen the terminal."
    }

    $viteEntry = Join-Path $root "web\node_modules\vite\bin\vite.js"
    if (-not (Test-Path -LiteralPath $viteEntry)) {
      Write-Host "Installing pinned Web dependencies..."
      & $pnpmCommand.Source --dir web install --frozen-lockfile
      if ($LASTEXITCODE -ne 0) {
        throw "Web dependency installation failed."
      }
    }
    if (-not (Test-Path -LiteralPath $viteEntry)) {
      throw "The pinned Vite entry was not installed."
    }
  }

  Write-Host "Validating Docker Compose configuration..."
  $composeConfiguration = Get-LLMGatewayComposeConfiguration
  Assert-LLMGatewayComposeLoopbackBinding `
    -Configuration $composeConfiguration -Service "postgres" -Target 5432
  Assert-LLMGatewayComposeLoopbackBinding `
    -Configuration $composeConfiguration -Service "valkey" -Target 6379

  Write-Host "Starting LLMGateway-owned PostgreSQL and Valkey..."
  Invoke-LLMGatewayDocker compose up --detach
  Wait-LLMGatewayContainerHealthy -Container "llmgateway-postgres"
  Wait-LLMGatewayContainerHealthy -Container "llmgateway-valkey"
  Assert-LLMGatewayComposeContainerOwnership -Container "llmgateway-postgres" -Service "postgres"
  Assert-LLMGatewayComposeContainerOwnership -Container "llmgateway-valkey" -Service "valkey"
  Test-LLMGatewayPostgres
  Test-LLMGatewayValkey

  $postgresAddress = Get-LLMGatewayLoopbackContainerEndpoint `
    -Container "llmgateway-postgres" -Target "5432/tcp"
  $valkeyAddress = Get-LLMGatewayLoopbackContainerEndpoint `
    -Container "llmgateway-valkey" -Target "6379/tcp"
  $postgresEnvironment = Get-LLMGatewayContainerEnvironment -Container "llmgateway-postgres"
  $valkeyEnvironment = Get-LLMGatewayContainerEnvironment -Container "llmgateway-valkey"
  $postgresDatabase = Get-LLMGatewayRequiredEnvironmentValue `
    -Values $postgresEnvironment -Name "POSTGRES_DB" -Container "llmgateway-postgres"
  $postgresUser = Get-LLMGatewayRequiredEnvironmentValue `
    -Values $postgresEnvironment -Name "POSTGRES_USER" -Container "llmgateway-postgres"
  $postgresPassword = Get-LLMGatewayRequiredEnvironmentValue `
    -Values $postgresEnvironment -Name "POSTGRES_PASSWORD" -Container "llmgateway-postgres"
  $valkeyPassword = Get-LLMGatewayRequiredEnvironmentValue `
    -Values $valkeyEnvironment -Name "VALKEY_PASSWORD" -Container "llmgateway-valkey"

  Write-Host "Development infrastructure is ready."
  Write-Host "PostgreSQL: $postgresAddress (persistent Compose volume)"
  Write-Host "Valkey:     $valkeyAddress (persistent Compose volume)"
  if ($InfrastructureOnly) {
    Write-Host "Run scripts\stop-dev.ps1 when the persistent infrastructure is no longer needed."
    return
  }

  New-Item -ItemType Directory -Path $buildRoot -Force | Out-Null
  $runID = "dev-$PID-$([guid]::NewGuid().ToString('N').Substring(0, 8))"
  $runDirectory = Join-Path $buildRoot $runID
  New-Item -ItemType Directory -Path $runDirectory | Out-Null
  $gatewayBinary = Join-Path $runDirectory "gateway.exe"

  Write-Host "Building the real Go gateway..."
  $goEnvironmentSnapshot = Save-LLMGatewayNamedEnvironment -Names @("GOOS", "GOARCH")
  try {
    $env:GOOS = $goHostOS
    $env:GOARCH = $goHostArch
    & $goCommand.Source build -o $gatewayBinary .\cmd\gateway
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $gatewayBinary)) {
      throw "Gateway build failed."
    }
  } finally {
    Restore-LLMGatewayNamedEnvironment -Snapshot $goEnvironmentSnapshot
  }

  $databaseURL = "postgres://$([uri]::EscapeDataString($postgresUser)):$([uri]::EscapeDataString($postgresPassword))@$postgresAddress/$([uri]::EscapeDataString($postgresDatabase))?sslmode=disable"
  $environmentSnapshot = Save-LLMGatewayProcessEnvironment
  try {
    Clear-LLMGatewayProcessEnvironment
    $env:LLMGATEWAY_PROFILE = "development"
    $env:LLMGATEWAY_HTTP_ADDRESS = "127.0.0.1:$GatewayPort"
    $env:LLMGATEWAY_DATABASE_URL = $databaseURL
    $env:LLMGATEWAY_DATABASE_MIGRATE_ON_START = "true"
    $env:LLMGATEWAY_VALKEY_ADDRESS = $valkeyAddress
    $env:LLMGATEWAY_VALKEY_PASSWORD = $valkeyPassword
    $env:LLMGATEWAY_VALKEY_DATABASE = "0"
    $env:LLMGATEWAY_COOKIE_SECURE = "false"
    # Meta/Clash Fake-IP DNS is dual-stack on Windows. These are local proxy
    # ranges, not Provider or user IP allowlists; public upstream IPs remain
    # free to change and are re-resolved for every outbound request.
    $env:LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS = "198.18.0.0/15,fdfe:dcba:9876::/64"
    $gatewayProcess = Start-Process -FilePath $gatewayBinary -WorkingDirectory $root `
      -NoNewWindow -PassThru
  } finally {
    Restore-LLMGatewayProcessEnvironment -Snapshot $environmentSnapshot
  }

  $gatewayURL = "http://127.0.0.1:$GatewayPort"
  Wait-LLMGatewayHTTPReady -URL "$gatewayURL/health/ready" -Process $gatewayProcess `
    -Label "gateway"

  $environmentSnapshot = Save-LLMGatewayProcessEnvironment
  try {
    Clear-LLMGatewayProcessEnvironment
    $env:VITE_API_PROXY_TARGET = $gatewayURL
    $webArguments = @(
      "`"$viteEntry`"",
      "--host", "127.0.0.1",
      "--port", "$WebPort",
      "--strictPort"
    )
    $webProcess = Start-Process -FilePath $nodeCommand.Source -ArgumentList $webArguments `
      -WorkingDirectory (Join-Path $root "web") -NoNewWindow -PassThru
  } finally {
    Restore-LLMGatewayProcessEnvironment -Snapshot $environmentSnapshot
  }

  $webURL = "http://127.0.0.1:$WebPort"
  Wait-LLMGatewayHTTPReady -URL $webURL -Process $webProcess -Label "Web development server"

  Write-Host ""
  Write-Host "LLMGateway is ready."
  Write-Host "Web UI:     $webURL"
  Write-Host "Gateway:    $gatewayURL"
  Write-Host "Readiness:  $gatewayURL/health/ready"
  Write-Host "Press Ctrl+C to stop this run. PostgreSQL and Valkey stay available with their data."
  if ($OpenBrowser) {
    Start-Process $webURL | Out-Null
  }

  while ($true) {
    if ($gatewayProcess.HasExited) {
      throw "Gateway exited unexpectedly with code $($gatewayProcess.ExitCode)."
    }
    if ($webProcess.HasExited) {
      throw "Web development server exited unexpectedly with code $($webProcess.ExitCode)."
    }
    Start-Sleep -Milliseconds 500
  }
} finally {
  $cleanupFailures = [System.Collections.Generic.List[string]]::new()
  try {
    Stop-LLMGatewayOwnedProcess -Process $webProcess -Label "Web development server"
  } catch {
    $cleanupFailures.Add("Web cleanup failed: $($_.Exception.Message)")
  }
  try {
    Stop-LLMGatewayOwnedProcess -Process $gatewayProcess -Label "gateway"
  } catch {
    $cleanupFailures.Add("Gateway cleanup failed: $($_.Exception.Message)")
  }
  try {
    Remove-LLMGatewayOwnedRunDirectory -RunDirectory $runDirectory -BuildRoot $buildRoot
  } catch {
    $cleanupFailures.Add("Build cleanup failed: $($_.Exception.Message)")
  }
  try {
    Pop-Location
  } catch {
    $cleanupFailures.Add("Location cleanup failed: $($_.Exception.Message)")
  }
  if ($cleanupFailures.Count -gt 0) {
    throw ($cleanupFailures -join [Environment]::NewLine)
  }
}
