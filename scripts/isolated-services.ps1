$ErrorActionPreference = "Stop"

. "$PSScriptRoot\docker.ps1"

$script:LLMGatewayTestOwner = "llmgateway-isolated-tests"

function Invoke-LLMGatewayNativeProbe {
  param([Parameter(Mandatory = $true)][scriptblock] $Action)

  $previousPreference = $ErrorActionPreference
  try {
    $ErrorActionPreference = "Continue"
    $output = & $Action 2>$null
    $exitCode = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $previousPreference
  }
  return [pscustomobject]@{ ExitCode = $exitCode; Output = @($output) }
}

function New-LLMGatewayTestRunID {
  param([Parameter(Mandatory = $true)][string] $Purpose)

  if ($Purpose -notmatch '^[a-z][a-z0-9-]{0,19}$') {
    throw "Test purpose must be a short lowercase container-name segment."
  }
  $suffix = [guid]::NewGuid().ToString("N").Substring(0, 8)
  return "$Purpose-$PID-$suffix".ToLowerInvariant()
}

function Get-LLMGatewayFreeLoopbackPort {
  $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
  try {
    $listener.Start()
    return ([System.Net.IPEndPoint] $listener.LocalEndpoint).Port
  } finally {
    $listener.Stop()
  }
}

function Get-LLMGatewayPublishedPort {
  param(
    [Parameter(Mandatory = $true)][string] $Container,
    [Parameter(Mandatory = $true)][string] $Target
  )

  $docker = Get-LLMGatewayDockerCommand
  $binding = & $docker port $Container $Target
  if ($LASTEXITCODE -ne 0) {
    throw "Could not read the published port for isolated container $Container."
  }
  $match = [regex]::Match(($binding | Select-Object -First 1), ':(\d+)$')
  if (-not $match.Success) {
    throw "Docker returned an invalid port binding for isolated container $Container."
  }
  return [int] $match.Groups[1].Value
}

function Wait-LLMGatewayPostgresReady {
  param(
    [Parameter(Mandatory = $true)][string] $Container,
    [Parameter(Mandatory = $true)][string] $DatabaseName,
    [Parameter(Mandatory = $true)][string] $Password
  )

  $docker = Get-LLMGatewayDockerCommand
  $deadline = (Get-Date).AddSeconds(60)
  do {
    $probe = Invoke-LLMGatewayNativeProbe {
      & $docker exec --env "PGPASSWORD=$Password" $Container `
        psql -h 127.0.0.1 -U llmgateway -d $DatabaseName -Atc "SELECT 1"
    }
    if ($probe.ExitCode -eq 0 -and $probe.Output.Count -eq 1 -and $probe.Output[0] -eq "1") {
      return
    }
    $state = Invoke-LLMGatewayNativeProbe { & $docker inspect --format '{{.State.Running}}' $Container }
    if ($state.ExitCode -ne 0 -or $state.Output.Count -ne 1 -or $state.Output[0] -ne "true") {
      throw "The isolated PostgreSQL container stopped before readiness."
    }
    Start-Sleep -Milliseconds 200
  } while ((Get-Date) -lt $deadline)
  throw "Timed out waiting for isolated PostgreSQL."
}

function Wait-LLMGatewayValkeyReady {
  param(
    [Parameter(Mandatory = $true)][string] $Container,
    [Parameter(Mandatory = $true)][string] $Password
  )

  $docker = Get-LLMGatewayDockerCommand
  $deadline = (Get-Date).AddSeconds(60)
  do {
    $probe = Invoke-LLMGatewayNativeProbe { & $docker exec $Container valkey-cli --no-auth-warning -a $Password ping }
    if ($probe.ExitCode -eq 0 -and $probe.Output.Count -eq 1 -and $probe.Output[0] -eq "PONG") {
      return
    }
    $state = Invoke-LLMGatewayNativeProbe { & $docker inspect --format '{{.State.Running}}' $Container }
    if ($state.ExitCode -ne 0 -or $state.Output.Count -ne 1 -or $state.Output[0] -ne "true") {
      throw "The isolated Valkey container stopped before readiness."
    }
    Start-Sleep -Milliseconds 200
  } while ((Get-Date) -lt $deadline)
  throw "Timed out waiting for isolated Valkey."
}

function Start-LLMGatewayTestPostgres {
  param(
    [Parameter(Mandatory = $true)][string] $RunID,
    [Parameter(Mandatory = $true)][string] $DatabaseName,
    [Parameter(Mandatory = $true)][string] $Password
  )

  $docker = Get-LLMGatewayDockerCommand
  $containerName = "llmgateway-test-postgres-$RunID"
  $containerOutput = & $docker run --detach --rm --name $containerName `
    --label "llmgateway.test.owner=$script:LLMGatewayTestOwner" `
    --label "llmgateway.test.run=$RunID" `
    --publish "127.0.0.1::5432" `
    --env "POSTGRES_DB=$DatabaseName" `
    --env "POSTGRES_USER=llmgateway" `
    --env "POSTGRES_PASSWORD=$Password" `
    postgres:18.4-alpine
  $containerExitCode = $LASTEXITCODE
  $container = [string] ($containerOutput | Select-Object -First 1)
  $container = $container.Trim()
  if ($containerExitCode -ne 0 -or -not $container) {
    throw "Could not start isolated PostgreSQL."
  }

  try {
    Wait-LLMGatewayPostgresReady -Container $container -DatabaseName $DatabaseName -Password $Password
    $port = Get-LLMGatewayPublishedPort -Container $container -Target "5432/tcp"
    return [pscustomobject]@{
      Container   = $container
      DatabaseURL = "postgres://llmgateway:$Password@127.0.0.1:$port/$DatabaseName`?sslmode=disable"
    }
  } catch {
    $startFailure = $_
    try {
      Stop-LLMGatewayTestContainer -Container $container -RunID $RunID
    } catch {
      throw "PostgreSQL startup failed: $($startFailure.Exception.Message) Cleanup also failed: $($_.Exception.Message)"
    }
    throw $startFailure
  }
}

function Start-LLMGatewayTestValkey {
  param(
    [Parameter(Mandatory = $true)][string] $RunID,
    [Parameter(Mandatory = $true)][string] $Password
  )

  $docker = Get-LLMGatewayDockerCommand
  $containerName = "llmgateway-test-valkey-$RunID"
  $containerOutput = & $docker run --detach --rm --name $containerName `
    --label "llmgateway.test.owner=$script:LLMGatewayTestOwner" `
    --label "llmgateway.test.run=$RunID" `
    --publish "127.0.0.1::6379" `
    valkey/valkey:9.1.0-alpine `
    valkey-server --appendonly no --requirepass $Password
  $containerExitCode = $LASTEXITCODE
  $container = [string] ($containerOutput | Select-Object -First 1)
  $container = $container.Trim()
  if ($containerExitCode -ne 0 -or -not $container) {
    throw "Could not start isolated Valkey."
  }

  try {
    Wait-LLMGatewayValkeyReady -Container $container -Password $Password
    $port = Get-LLMGatewayPublishedPort -Container $container -Target "6379/tcp"
    return [pscustomobject]@{
      Container = $container
      Address   = "127.0.0.1:$port"
    }
  } catch {
    $startFailure = $_
    try {
      Stop-LLMGatewayTestContainer -Container $container -RunID $RunID
    } catch {
      throw "Valkey startup failed: $($startFailure.Exception.Message) Cleanup also failed: $($_.Exception.Message)"
    }
    throw $startFailure
  }
}

function Stop-LLMGatewayTestContainer {
  param(
    [string] $Container,
    [Parameter(Mandatory = $true)][string] $RunID
  )

  if (-not $Container) {
    return
  }
  if ($RunID -notmatch '^[a-z][a-z0-9-]{1,63}$') {
    throw "Refusing to use an invalid isolated test-run ID."
  }
  if ($Container -notmatch '^[a-f0-9]{12,64}$') {
    throw "Refusing to inspect an invalid isolated container ID."
  }
  $docker = Get-LLMGatewayDockerCommand
  $inspectionProbe = Invoke-LLMGatewayNativeProbe { & $docker inspect $Container }
  if ($inspectionProbe.ExitCode -ne 0) {
    $existenceProbe = Invoke-LLMGatewayNativeProbe { & $docker ps --all --quiet --no-trunc --filter "id=$Container" }
    if ($existenceProbe.ExitCode -ne 0) {
      throw "Could not determine whether isolated container $Container still exists."
    }
    if ($existenceProbe.Output.Count -eq 0) {
      return
    }
    throw "Could not inspect isolated container $Container before cleanup."
  }
  $inspection = @($inspectionProbe.Output | ConvertFrom-Json)
  if ($inspection.Count -ne 1) {
    throw "Docker returned an invalid inspection result for isolated container $Container."
  }
  $actualOwner = $inspection[0].Config.Labels.'llmgateway.test.owner'
  $actualRunID = $inspection[0].Config.Labels.'llmgateway.test.run'
  $actualName = [string] $inspection[0].Name
  $expectedNames = @("/llmgateway-test-postgres-$RunID", "/llmgateway-test-valkey-$RunID")
  if ($actualOwner -ne $script:LLMGatewayTestOwner -or $actualRunID -ne $RunID -or $actualName -notin $expectedNames) {
    throw "Refusing to remove container $Container because its isolated-test ownership does not match."
  }
  & $docker rm --force $Container *> $null
  if ($LASTEXITCODE -ne 0) {
    throw "Could not remove isolated container $Container."
  }
}

function Save-LLMGatewayEnvironment {
  $snapshot = @{}
  foreach ($item in Get-ChildItem Env: | Where-Object { $_.Name -like "LLMGATEWAY_*" }) {
    $snapshot[$item.Name] = $item.Value
  }
  return $snapshot
}

function Clear-LLMGatewayEnvironment {
  foreach ($item in @(Get-ChildItem Env: | Where-Object { $_.Name -like "LLMGATEWAY_*" })) {
    [Environment]::SetEnvironmentVariable($item.Name, $null, "Process")
  }
}

function Restore-LLMGatewayEnvironment {
  param([Parameter(Mandatory = $true)][hashtable] $Snapshot)

  Clear-LLMGatewayEnvironment
  foreach ($name in $Snapshot.Keys) {
    [Environment]::SetEnvironmentVariable($name, $Snapshot[$name], "Process")
  }
}
