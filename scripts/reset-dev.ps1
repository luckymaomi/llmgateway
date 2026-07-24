[CmdletBinding()]
param(
  [switch] $ConfirmDataLoss
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"
. "$PSScriptRoot\dev-processes.ps1"

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

$root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
Stop-LLMGatewayDevelopmentProcesses -Root $root

Assert-LLMGatewayOwnedContainerIfPresent -Name "llmgateway-postgres" -Service "postgres"
Assert-LLMGatewayOwnedContainerIfPresent -Name "llmgateway-valkey" -Service "valkey"
Assert-LLMGatewayOwnedVolumeIfPresent -Name "llmgateway_postgres" -LogicalName "llmgateway_postgres"
Assert-LLMGatewayOwnedVolumeIfPresent -Name "llmgateway_valkey" -LogicalName "llmgateway_valkey"

Push-Location $root
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
