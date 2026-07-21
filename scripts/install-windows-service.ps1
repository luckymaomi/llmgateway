param(
  [Parameter(Mandatory = $true)][string] $BinaryPath,
  [Parameter(Mandatory = $true)][string] $EnvironmentFile,
  [string] $HealthURL = "",
  [switch] $Start
)

$ErrorActionPreference = "Stop"
. "$PSScriptRoot\deployment-environment.ps1"

$serviceName = "LLMGateway"
$principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Installing the LLMGateway Windows service requires an elevated PowerShell session."
}
if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
  throw "The LLMGateway Windows service already exists. Uninstall it explicitly before reinstalling."
}
$binary = [IO.Path]::GetFullPath($BinaryPath)
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw "Gateway binary does not exist: $binary" }
$values = Read-LLMGatewayEnvironmentFile -Path $EnvironmentFile
Assert-LLMGatewayFileSecrets -Values $values
if ($values.LLMGATEWAY_PROFILE -ne "production") { throw "Windows service profile must be production." }

$environmentSnapshot = @{}
foreach ($item in Get-ChildItem Env:) { $environmentSnapshot[$item.Name] = $item.Value }
$created = $false
$eventSourceCreated = $false
try {
  Set-LLMGatewayProcessEnvironment -Values $values
  & $binary --check-config
  if ($LASTEXITCODE -ne 0) { throw "Gateway configuration validation failed." }

  & sc.exe create $serviceName "binPath=" $binary "start=" "delayed-auto" "obj=" "NT SERVICE\$serviceName" "depend=" "Tcpip"
  if ($LASTEXITCODE -ne 0) { throw "Windows SCM service creation failed." }
  $created = $true
  & sc.exe description $serviceName "Production multi-Provider LLMGateway service"
  if ($LASTEXITCODE -ne 0) { throw "Windows service description update failed." }
  & sc.exe sidtype $serviceName unrestricted
  if ($LASTEXITCODE -ne 0) { throw "Windows service SID configuration failed." }
  & sc.exe failure $serviceName "reset=" "86400" "actions=" "restart/5000/restart/15000/none/0"
  if ($LASTEXITCODE -ne 0) { throw "Windows service recovery policy failed." }
  & sc.exe failureflag $serviceName 1
  if ($LASTEXITCODE -ne 0) { throw "Windows non-crash recovery policy failed." }

  if (-not [Diagnostics.EventLog]::SourceExists($serviceName)) {
    New-EventLog -LogName Application -Source $serviceName
    $eventSourceCreated = $true
  }
  $registryPath = "HKLM:\SYSTEM\CurrentControlSet\Services\$serviceName"
  $serviceEnvironment = @($values.Keys | Sort-Object | ForEach-Object { "$_=$($values[$_])" })
  New-ItemProperty -Path $registryPath -Name Environment -PropertyType MultiString -Value $serviceEnvironment -Force | Out-Null

  $serviceIdentity = "NT SERVICE\$serviceName"
  foreach ($name in $values.Keys | Where-Object { $_ -like "*_FILE" }) {
    & icacls.exe $values[$name] /inheritance:r /grant:r "${serviceIdentity}:(R)" "BUILTIN\Administrators:(F)" "NT AUTHORITY\SYSTEM:(F)" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not restrict $name for the Windows service identity." }
  }
  & icacls.exe $binary /grant:r "${serviceIdentity}:(RX)" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Could not grant the service identity access to the Gateway binary." }

  if ($Start) {
    Start-Service -Name $serviceName
    $service = Get-Service -Name $serviceName
    $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(45))
    if ($HealthURL) {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $HealthURL -TimeoutSec 10
      if ([int]$response.StatusCode -ne 200) { throw "Windows service health check did not return HTTP 200." }
    }
  }
} catch {
  $installFailure = $_
  if ($created) {
    try { Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue } catch {}
    & sc.exe delete $serviceName | Out-Null
  }
  if ($eventSourceCreated) { try { Remove-EventLog -Source $serviceName } catch {} }
  throw $installFailure
} finally {
  foreach ($item in @(Get-ChildItem Env:)) {
    if (-not $environmentSnapshot.ContainsKey($item.Name)) {
      [Environment]::SetEnvironmentVariable($item.Name, $null, "Process")
    }
  }
  foreach ($name in $environmentSnapshot.Keys) {
    [Environment]::SetEnvironmentVariable($name, $environmentSnapshot[$name], "Process")
  }
}

Write-Host "LLMGateway Windows service installed with delayed automatic start and bounded restart recovery."
