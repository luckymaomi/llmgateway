param([switch] $RemoveEventSource)

$ErrorActionPreference = "Stop"
$serviceName = "LLMGateway"
$principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Uninstalling the LLMGateway Windows service requires an elevated PowerShell session."
}
$service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
if ($service) {
  if ($service.Status -ne [ServiceProcess.ServiceControllerStatus]::Stopped) {
    Stop-Service -Name $serviceName
    $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(45))
  }
  & sc.exe delete $serviceName
  if ($LASTEXITCODE -ne 0) { throw "Windows SCM service deletion failed." }
}
if ($RemoveEventSource -and [Diagnostics.EventLog]::SourceExists($serviceName)) {
  Remove-EventLog -Source $serviceName
}
Write-Host "LLMGateway Windows service removed; database, secret files, and logs were preserved."
