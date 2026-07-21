param(
  [Parameter(Mandatory = $true)][string] $InputPath,
  [Parameter(Mandatory = $true)][string] $TargetDatabase,
  [string] $Container = "llmgateway-postgres",
  [string] $DatabaseUser = "",
  [string] $ExpectedComposeProject = "",
  [switch] $ConfirmRestore,
  [switch] $AllowIsolatedTestContainer
)

$ErrorActionPreference = "Stop"
. "$PSScriptRoot\docker.ps1"

if (-not $ConfirmRestore) { throw "Restore requires -ConfirmRestore." }
if ($TargetDatabase -notmatch '^[A-Za-z][A-Za-z0-9_]{2,62}$' -or $TargetDatabase -eq 'postgres' -or $TargetDatabase.StartsWith('template')) {
  throw "Target database name is unsafe."
}
$resolvedInput = [System.IO.Path]::GetFullPath($InputPath)
if (-not (Test-Path -LiteralPath $resolvedInput) -or (Get-Item -LiteralPath $resolvedInput).Length -le 0) {
  throw "Backup archive is missing or empty: $resolvedInput"
}

$docker = Get-LLMGatewayDockerCommand
$labels = (& $docker inspect --format '{{json .Config.Labels}}' $Container | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0) { throw "Could not inspect PostgreSQL container $Container." }
if ($ExpectedComposeProject -and $ExpectedComposeProject -notmatch '^[a-z0-9][a-z0-9_-]{1,62}$') {
  throw "Expected Compose project name is invalid."
}
$allowedProjects = if ($ExpectedComposeProject) { @($ExpectedComposeProject) } else { @('llmgateway', 'llmgateway-production') }
$owned = $labels.'com.docker.compose.project' -in $allowedProjects -and $labels.'com.docker.compose.service' -eq 'postgres'
$isolated = $AllowIsolatedTestContainer -and $labels.'llmgateway.test.owner' -eq 'llmgateway-isolated-tests'
if (-not $owned -and -not $isolated) { throw "Refusing to restore into a PostgreSQL container not owned by LLMGateway." }
if (-not $DatabaseUser) {
  foreach ($entry in @(& $docker inspect --format '{{json .Config.Env}}' $Container | ConvertFrom-Json)) {
    if ([string]$entry -like 'POSTGRES_USER=*') { $DatabaseUser = ([string]$entry -split '=', 2)[1] }
  }
}
if (-not $DatabaseUser) { throw "PostgreSQL user could not be resolved." }

$existing = & $docker exec $Container psql --username $DatabaseUser --dbname postgres --tuples-only --no-align --command "SELECT 1 FROM pg_database WHERE datname = '$TargetDatabase'"
if ($LASTEXITCODE -ne 0) { throw "Could not check the restore target database." }
if ([string]$existing -eq '1') { throw "Restore target already exists: $TargetDatabase" }

$containerArchive = "/tmp/llmgateway-restore-$([guid]::NewGuid().ToString('N')).dump"
$created = $false
try {
  & $docker cp $resolvedInput "$Container`:$containerArchive"
  if ($LASTEXITCODE -ne 0) { throw "The backup archive could not be copied into PostgreSQL." }
  & $docker exec $Container pg_restore --list $containerArchive | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "The backup archive is invalid." }
  & $docker exec $Container createdb --username $DatabaseUser $TargetDatabase
  if ($LASTEXITCODE -ne 0) { throw "The restore target database could not be created." }
  $created = $true
  & $docker exec $Container pg_restore --exit-on-error --no-owner --no-privileges --username $DatabaseUser --dbname $TargetDatabase $containerArchive
  if ($LASTEXITCODE -ne 0) { throw "pg_restore failed." }
} catch {
  if ($created) { & $docker exec $Container dropdb --if-exists --username $DatabaseUser $TargetDatabase 2>$null | Out-Null }
  throw
} finally {
  & $docker exec $Container rm -f $containerArchive 2>$null | Out-Null
}

Write-Host "PostgreSQL backup restored into new database: $TargetDatabase"
