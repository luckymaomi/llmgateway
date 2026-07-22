[CmdletBinding()]
param(
  [switch] $ConfirmDataLoss,
  [ValidateRange(1, 65535)]
  [int] $GatewayPort = 8080,
  [ValidateRange(1, 65535)]
  [int] $WebPort = 5173
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

function Assert-LLMGatewayPortClosed {
  param(
    [Parameter(Mandatory = $true)][int] $Port,
    [Parameter(Mandatory = $true)][string] $Label
  )

  $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $Port)
  try {
    $listener.Start()
  } catch {
    throw "$Label port 127.0.0.1:$Port is still in use. Stop start_dev.py with Ctrl+C before resetting data."
  } finally {
    $listener.Stop()
  }
}

function Assert-NoLLMGatewayDevelopmentProcess {
  $root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
  $buildRoot = [System.IO.Path]::GetFullPath((Join-Path $root ".build")).TrimEnd('\') + '\'
  foreach ($process in @(Get-Process -Name "gateway" -ErrorAction SilentlyContinue)) {
    try {
      $path = [string] $process.Path
    } catch {
      $path = ""
    }
    if (-not $path.StartsWith($buildRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
      continue
    }
    $relativePath = $path.Substring($buildRoot.Length)
    if ($relativePath -match '^dev-[^\\]+\\gateway\.exe$') {
      throw "LLMGateway development process $($process.Id) is still running. Stop start_dev.py with Ctrl+C before resetting data."
    }
  }
}

function Get-LLMGatewayDockerLabels {
  param(
    [Parameter(Mandatory = $true)][ValidateSet("container", "volume")][string] $ResourceType,
    [Parameter(Mandatory = $true)][string] $Name
  )

  $docker = Get-LLMGatewayDockerCommand
  if ($ResourceType -eq "container") {
    $names = @(& $docker container ls --all --format '{{.Names}}')
  } else {
    $names = @(& $docker volume ls --format '{{.Name}}')
  }
  if ($LASTEXITCODE -ne 0) {
    throw "Could not list Docker $ResourceType resources before reset."
  }
  if ($names -notcontains $Name) {
    return $null
  }
  if ($ResourceType -eq "container") {
    $encoded = & $docker container inspect --format '{{json .Config.Labels}}' $Name
  } else {
    $encoded = & $docker volume inspect --format '{{json .Labels}}' $Name
  }
  if ($LASTEXITCODE -ne 0 -or -not $encoded) {
    throw "Could not inspect Docker ${ResourceType} ownership before reset: $Name"
  }
  $labels = ConvertFrom-Json -InputObject ($encoded -join "")
  if ($null -eq $labels) {
    throw "Docker $ResourceType does not expose ownership labels: $Name"
  }
  return $labels
}

function Assert-LLMGatewayOwnedContainerIfPresent {
  param(
    [Parameter(Mandatory = $true)][string] $Name,
    [Parameter(Mandatory = $true)][string] $Service
  )

  $labels = Get-LLMGatewayDockerLabels -ResourceType container -Name $Name
  if ($null -eq $labels) {
    return
  }
  if ($labels.'com.docker.compose.project' -ne "llmgateway" -or
      $labels.'com.docker.compose.service' -ne $Service) {
    throw "Refusing to reset $Name because it is not owned by the expected LLMGateway Compose service."
  }
}

function Assert-LLMGatewayOwnedVolumeIfPresent {
  param(
    [Parameter(Mandatory = $true)][string] $Name,
    [Parameter(Mandatory = $true)][string] $LogicalName
  )

  $labels = Get-LLMGatewayDockerLabels -ResourceType volume -Name $Name
  if ($null -eq $labels) {
    return
  }
  if ($labels.'com.docker.compose.project' -ne "llmgateway" -or
      $labels.'com.docker.compose.volume' -ne $LogicalName) {
    throw "Refusing to reset $Name because it is not owned by the expected LLMGateway Compose volume."
  }
}

function Assert-LLMGatewayResourceAbsent {
  param(
    [Parameter(Mandatory = $true)][ValidateSet("container", "volume")][string] $ResourceType,
    [Parameter(Mandatory = $true)][string] $Name
  )

  if ($null -ne (Get-LLMGatewayDockerLabels -ResourceType $ResourceType -Name $Name)) {
    throw "Reset did not remove the expected ${ResourceType}: $Name"
  }
}

if (-not $ConfirmDataLoss) {
  throw "Data reset requires the explicit -ConfirmDataLoss switch."
}
if ($GatewayPort -eq $WebPort) {
  throw "GatewayPort and WebPort must be different."
}

Assert-NoLLMGatewayDevelopmentProcess
Assert-LLMGatewayPortClosed -Port $GatewayPort -Label "Gateway"
Assert-LLMGatewayPortClosed -Port $WebPort -Label "Web"

Assert-LLMGatewayOwnedContainerIfPresent -Name "llmgateway-postgres" -Service "postgres"
Assert-LLMGatewayOwnedContainerIfPresent -Name "llmgateway-valkey" -Service "valkey"
Assert-LLMGatewayOwnedVolumeIfPresent -Name "llmgateway_postgres" -LogicalName "llmgateway_postgres"
Assert-LLMGatewayOwnedVolumeIfPresent -Name "llmgateway_valkey" -LogicalName "llmgateway_valkey"

Push-Location (Join-Path $PSScriptRoot "..")
try {
  Write-Host "Removing LLMGateway development containers and named data volumes..."
  Invoke-LLMGatewayDocker compose down --volumes
} finally {
  Pop-Location
}

Assert-LLMGatewayResourceAbsent -ResourceType container -Name "llmgateway-postgres"
Assert-LLMGatewayResourceAbsent -ResourceType container -Name "llmgateway-valkey"
Assert-LLMGatewayResourceAbsent -ResourceType volume -Name "llmgateway_postgres"
Assert-LLMGatewayResourceAbsent -ResourceType volume -Name "llmgateway_valkey"

Write-Host "LLMGateway local PostgreSQL and Valkey data were reset. Source files and other Docker projects were not changed."
