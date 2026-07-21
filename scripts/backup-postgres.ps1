param(
  [Parameter(Mandatory = $true)][string] $OutputPath,
  [string] $Container = "llmgateway-postgres",
  [string] $DatabaseName = "",
  [string] $DatabaseUser = "",
  [switch] $Force,
  [switch] $AllowIsolatedTestContainer
)

$ErrorActionPreference = "Stop"
. "$PSScriptRoot\docker.ps1"

function Get-ContainerFacts {
  param([string] $Name)
  $docker = Get-LLMGatewayDockerCommand
  $labels = (& $docker inspect --format '{{json .Config.Labels}}' $Name | ConvertFrom-Json)
  if ($LASTEXITCODE -ne 0) { throw "Could not inspect PostgreSQL container $Name." }
  $owned = $labels.'com.docker.compose.project' -eq 'llmgateway' -and $labels.'com.docker.compose.service' -eq 'postgres'
  $isolated = $AllowIsolatedTestContainer -and $labels.'llmgateway.test.owner' -eq 'llmgateway-isolated-tests'
  if (-not $owned -and -not $isolated) { throw "Refusing to back up a PostgreSQL container not owned by LLMGateway." }
  $values = @{}
  foreach ($entry in @(& $docker inspect --format '{{json .Config.Env}}' $Name | ConvertFrom-Json)) {
    $parts = [string]$entry -split '=', 2
    if ($parts.Count -eq 2) { $values[$parts[0]] = $parts[1] }
  }
  return $values
}

$docker = Get-LLMGatewayDockerCommand
$facts = Get-ContainerFacts -Name $Container
if (-not $DatabaseName) { $DatabaseName = [string]$facts.POSTGRES_DB }
if (-not $DatabaseUser) { $DatabaseUser = [string]$facts.POSTGRES_USER }
if (-not $DatabaseName -or -not $DatabaseUser) { throw "PostgreSQL database and user could not be resolved." }

$resolvedOutput = [System.IO.Path]::GetFullPath($OutputPath)
if ((Test-Path -LiteralPath $resolvedOutput) -and -not $Force) { throw "Backup already exists: $resolvedOutput" }
$parent = Split-Path -Parent $resolvedOutput
if (-not $parent) { throw "Backup output must include a parent directory." }
New-Item -ItemType Directory -Force -Path $parent | Out-Null

$containerArchive = "/tmp/llmgateway-backup-$([guid]::NewGuid().ToString('N')).dump"
try {
  & $docker exec $Container pg_dump --username $DatabaseUser --dbname $DatabaseName --format custom --compress 9 --file $containerArchive
  if ($LASTEXITCODE -ne 0) { throw "pg_dump failed." }
  & $docker exec $Container pg_restore --list $containerArchive | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "The PostgreSQL backup archive could not be listed." }
  & $docker cp "$Container`:$containerArchive" $resolvedOutput
  if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $resolvedOutput) -or (Get-Item -LiteralPath $resolvedOutput).Length -le 0) {
    throw "The PostgreSQL backup archive was not copied successfully."
  }
} finally {
  & $docker exec $Container rm -f $containerArchive 2>$null | Out-Null
}

Write-Host "PostgreSQL backup created: $resolvedOutput"
