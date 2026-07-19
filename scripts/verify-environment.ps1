param(
  [switch] $SkipGo
)

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

function Assert-Command {
  param(
    [Parameter(Mandatory = $true)]
    [string] $Name
  )

  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "Required command was not found: $Name"
  }
}

function Get-SemanticVersion {
  param(
    [Parameter(Mandatory = $true)]
    [string] $Value
  )

  $match = [regex]::Match($Value, '(\d+)\.(\d+)\.(\d+)')
  if (-not $match.Success) {
    throw "Could not parse version from: $Value"
  }

  return [Version]::new(
    [int] $match.Groups[1].Value,
    [int] $match.Groups[2].Value,
    [int] $match.Groups[3].Value
  )
}

Push-Location (Join-Path $PSScriptRoot "..")
try {
  Assert-Command git
  Assert-Command node
  Assert-Command pnpm

  $nodeVersion = Get-SemanticVersion (& node --version)
  if ($nodeVersion -lt [Version]"22.12.0") {
    throw "Node.js 22.12.0 or newer is required; found $nodeVersion."
  }

  if ($SkipGo) {
    Write-Host "Go check skipped explicitly."
  } else {
    Assert-Command go
    $goVersion = Get-SemanticVersion (& go version)
    if ($goVersion -lt [Version]"1.26.5") {
      throw "Go 1.26.5 or newer is required; found $goVersion."
    }
  }

  $docker = Get-LLMGatewayDockerCommand
  & $docker info --format "Docker server {{.ServerVersion}} ({{.OSType}}/{{.Architecture}})" | Write-Host
  if ($LASTEXITCODE -ne 0) {
    throw "Docker Desktop is not ready."
  }

  Invoke-LLMGatewayDocker compose version
  Invoke-LLMGatewayDocker compose config --quiet
  Wait-LLMGatewayContainerHealthy -Container "llmgateway-postgres"
  Wait-LLMGatewayContainerHealthy -Container "llmgateway-valkey"
  Test-LLMGatewayPostgres
  Test-LLMGatewayValkey

  Write-Host "Environment verification passed."
} finally {
  Pop-Location
}
