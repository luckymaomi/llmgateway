function Test-LLMGatewayCommandLineContains {
  param(
    [string] $CommandLine,
    [Parameter(Mandatory = $true)][string] $Expected
  )

  return -not [string]::IsNullOrWhiteSpace($CommandLine) -and
    $CommandLine.IndexOf($Expected, [System.StringComparison]::OrdinalIgnoreCase) -ge 0
}

function Get-LLMGatewayDevelopmentProcesses {
  param([Parameter(Mandatory = $true)][string] $Root)

  $resolvedRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd('\')
  $buildRoot = [System.IO.Path]::GetFullPath((Join-Path $resolvedRoot ".build")).TrimEnd('\') + '\'
  $viteEntry = [System.IO.Path]::GetFullPath(
    (Join-Path $resolvedRoot "web\node_modules\vite\bin\vite.js")
  )
  $devScript = [System.IO.Path]::GetFullPath((Join-Path $resolvedRoot "scripts\dev.ps1"))
  $processes = @(Get-CimInstance Win32_Process -ErrorAction Stop)
  $byID = @{}
  foreach ($process in $processes) {
    $byID[[int] $process.ProcessId] = $process
  }

  $owned = @{}
  foreach ($process in $processes) {
    $processID = [int] $process.ProcessId
    $name = [string] $process.Name
    $path = [string] $process.ExecutablePath
    $commandLine = [string] $process.CommandLine
    $role = $null

    if ($name -ieq "gateway.exe" -and
        $path.StartsWith($buildRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
      $relativePath = $path.Substring($buildRoot.Length)
      if ($relativePath -match '^dev-[^\\]+\\gateway\.exe$') {
        $role = "gateway"
      }
    } elseif ($name -ieq "node.exe" -and
        (Test-LLMGatewayCommandLineContains -CommandLine $commandLine -Expected $viteEntry)) {
      $role = "web"
    } elseif ($name -in @("powershell.exe", "pwsh.exe") -and
        (Test-LLMGatewayCommandLineContains -CommandLine $commandLine -Expected $devScript)) {
      $role = "dev-launcher"
    }

    if ($role) {
      $owned[$processID] = [pscustomobject]@{
        ProcessID       = $processID
        ParentProcessID = [int] $process.ParentProcessId
        Name            = $name
        Role            = $role
      }
    }
  }

  $changed = $true
  while ($changed) {
    $changed = $false
    foreach ($process in $processes) {
      $processID = [int] $process.ProcessId
      if ($owned.ContainsKey($processID) -or
          -not $owned.ContainsKey([int] $process.ParentProcessId)) {
        continue
      }
      $owned[$processID] = [pscustomobject]@{
        ProcessID       = $processID
        ParentProcessID = [int] $process.ParentProcessId
        Name            = [string] $process.Name
        Role            = "owned-child"
      }
      $changed = $true
    }
  }

  foreach ($launcher in @($owned.Values | Where-Object { $_.Role -eq "dev-launcher" })) {
    $parent = $byID[$launcher.ParentProcessID]
    if ($null -eq $parent -or $owned.ContainsKey([int] $parent.ProcessId)) {
      continue
    }
    if ([string] $parent.Name -notin @("python.exe", "pythonw.exe") -or
        -not (Test-LLMGatewayCommandLineContains `
          -CommandLine ([string] $parent.CommandLine) -Expected "start_dev.py")) {
      continue
    }
    $owned[[int] $parent.ProcessId] = [pscustomobject]@{
      ProcessID       = [int] $parent.ProcessId
      ParentProcessID = [int] $parent.ParentProcessId
      Name            = [string] $parent.Name
      Role            = "start-launcher"
    }
  }

  return @($owned.Values)
}

function Stop-LLMGatewayDevelopmentProcesses {
  param([Parameter(Mandatory = $true)][string] $Root)

  $owned = @(Get-LLMGatewayDevelopmentProcesses -Root $Root)
  if ($owned.Count -eq 0) {
    Write-Host "No LLMGateway development processes are running."
  } else {
    $workloads = @($owned | Where-Object {
        $_.Role -notin @("dev-launcher", "start-launcher")
      })
    $launchers = @($owned | Where-Object { $_.Role -eq "dev-launcher" })
    $startLaunchers = @($owned | Where-Object { $_.Role -eq "start-launcher" })

    foreach ($fact in @($workloads + $launchers + $startLaunchers)) {
      $process = Get-Process -Id $fact.ProcessID -ErrorAction SilentlyContinue
      if ($null -eq $process) {
        continue
      }
      Write-Host "Stopping LLMGateway $($fact.Role) (PID $($fact.ProcessID))..."
      Stop-Process -Id $fact.ProcessID -ErrorAction SilentlyContinue
    }

    $deadline = (Get-Date).AddSeconds(5)
    do {
      $remaining = @($owned | Where-Object {
          $null -ne (Get-Process -Id $_.ProcessID -ErrorAction SilentlyContinue)
        })
      if ($remaining.Count -eq 0) {
        break
      }
      Start-Sleep -Milliseconds 100
    } while ((Get-Date) -lt $deadline)

    foreach ($fact in $remaining) {
      Write-Host "Force stopping LLMGateway $($fact.Role) (PID $($fact.ProcessID))..."
      Stop-Process -Id $fact.ProcessID -Force -ErrorAction SilentlyContinue
    }

    Start-Sleep -Milliseconds 200
    $survivors = @(Get-LLMGatewayDevelopmentProcesses -Root $Root)
    if ($survivors.Count -gt 0) {
      $identifiers = ($survivors | ForEach-Object { "$($_.ProcessID):$($_.Role)" }) -join ", "
      throw "Could not stop all LLMGateway development processes: $identifiers"
    }
  }

  Remove-LLMGatewayDevelopmentRunDirectories -Root $Root
}

function Remove-LLMGatewayDevelopmentRunDirectories {
  param([Parameter(Mandatory = $true)][string] $Root)

  $buildRoot = [System.IO.Path]::GetFullPath((Join-Path $Root ".build")).TrimEnd('\')
  if (-not (Test-Path -LiteralPath $buildRoot -PathType Container)) {
    return
  }
  $buildPrefix = $buildRoot + '\'
  foreach ($directory in @(Get-ChildItem -LiteralPath $buildRoot -Directory -Filter "dev-*")) {
    $target = [System.IO.Path]::GetFullPath($directory.FullName)
    if (-not $target.StartsWith($buildPrefix, [System.StringComparison]::OrdinalIgnoreCase) -or
        $directory.Name -notmatch '^dev-[A-Za-z0-9-]+$') {
      throw "Refusing to remove an invalid development run directory: $target"
    }
    Write-Host "Removing stale development run directory $($directory.Name)..."
    Remove-Item -LiteralPath $target -Recurse -Force
  }
}
