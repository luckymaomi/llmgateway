param(
  [switch] $SkipIntegration,
  [switch] $SkipBrowser,
  [switch] $SkipBuildMatrix
)

$ErrorActionPreference = "Stop"
$pnpmCommand = if ($env:OS -eq "Windows_NT") { "pnpm.cmd" } else { "pnpm" }
$powerShellCommand = if ($env:OS -eq "Windows_NT") { "powershell" } else { "pwsh" }

. "$PSScriptRoot\isolated-services.ps1"

function Invoke-Step {
  param(
    [Parameter(Mandatory = $true)][string] $Name,
    [Parameter(Mandatory = $true)][scriptblock] $Action
  )
  Write-Host "`n==> $Name"
  & $Action
  if ($LASTEXITCODE -ne 0) {
    throw "$Name failed with exit code $LASTEXITCODE."
  }
}

$environmentSnapshot = Save-LLMGatewayEnvironment
$goEnvironmentSnapshot = @{}
foreach ($name in @("GOOS", "GOARCH", "CGO_ENABLED")) {
  $item = Get-Item "Env:$name" -ErrorAction SilentlyContinue
  $goEnvironmentSnapshot[$name] = if ($null -eq $item) {
    @{ Exists = $false; Value = "" }
  } else {
    @{ Exists = $true; Value = $item.Value }
  }
}
$verificationFailure = $null
Push-Location (Join-Path $PSScriptRoot "..")
try {
  Clear-LLMGatewayEnvironment
  Invoke-Step "Environment" {
    if ($SkipIntegration -and $SkipBrowser) {
      & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-environment.ps1 -SkipServices -SkipDockerDaemon
    } else {
      & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-environment.ps1 -SkipServices
    }
  }

  Invoke-Step "Go formatting" {
    $unformatted = & gofmt -l .\cmd .\internal .\migrations
    if ($unformatted) {
      $unformatted | Write-Host
      throw "Go files are not formatted."
    }
  }
  Invoke-Step "Go vet" { go vet ./... }
  Invoke-Step "Go tests" { go test ./... }

  Invoke-Step "sqlc generation" {
    $before = Get-ChildItem .\internal\store\db -File | Sort-Object FullName | ForEach-Object { "$($_.Name):$((Get-FileHash -Algorithm SHA256 $_.FullName).Hash)" }
    go tool sqlc generate
    if ($LASTEXITCODE -ne 0) { throw "sqlc generation failed with exit code $LASTEXITCODE." }
    $after = Get-ChildItem .\internal\store\db -File | Sort-Object FullName | ForEach-Object { "$($_.Name):$((Get-FileHash -Algorithm SHA256 $_.FullName).Hash)" }
    if (Compare-Object $before $after) { throw "sqlc generated output drifted." }
  }

  if (Test-Path .\web\package.json) {
    Invoke-Step "Frontend install integrity" { & $pnpmCommand --dir web install --frozen-lockfile }
    Invoke-Step "Frontend checks" { & $pnpmCommand --dir web run verify }
    if (-not $SkipBrowser) {
      Invoke-Step "Real headed Provider browser acceptance" { & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-browser-real.ps1 }
    }
  }

  Invoke-Step "Compose configuration" { docker compose config --quiet }
  if (-not $SkipIntegration) {
    Invoke-Step "Migration round-trip" { & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-migrations.ps1 }
    Invoke-Step "Core gateway flow" { & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-core.ps1 }
    Invoke-Step "Credential rotation and PostgreSQL recovery" { & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-operations.ps1 }
    Invoke-Step "Prometheus rules and Grafana dashboard" { & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-observability.ps1 }
    if ($env:OS -eq "Windows_NT") {
      Invoke-Step "Windows SCM production service" { & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-windows-service.ps1 }
      if (-not $SkipBrowser) {
        Invoke-Step "Production TLS deployment and rolling recovery" { & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-deployment.ps1 }
        Invoke-Step "Encrypted empty-environment disaster recovery" { & $powerShellCommand -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-disaster-recovery.ps1 }
      }
    }
  }

  if (-not $SkipBuildMatrix) {
    Invoke-Step "Go build matrix" {
      $targets = @(
        @{ OS = "windows"; Arch = "amd64"; Suffix = ".exe" },
        @{ OS = "linux"; Arch = "amd64"; Suffix = "" }
      )
      New-Item -ItemType Directory -Force .\.build | Out-Null
      foreach ($target in $targets) {
        $env:GOOS = $target.OS
        $env:GOARCH = $target.Arch
        $env:CGO_ENABLED = "0"
        $output = ".\.build\llmgateway-$($target.OS)-$($target.Arch)$($target.Suffix)"
        go build -tags webembed -trimpath -o $output .\cmd\gateway
        if ($LASTEXITCODE -ne 0) { throw "Go build failed for $($target.OS)/$($target.Arch) with exit code $LASTEXITCODE." }
      }
      Remove-Item Env:GOOS -ErrorAction SilentlyContinue
      Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
      Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    }
  }

} catch {
  $verificationFailure = $_
} finally {
  $cleanupFailures = @()
  foreach ($name in @("GOOS", "GOARCH", "CGO_ENABLED")) {
    try {
      if ($goEnvironmentSnapshot[$name].Exists) {
        [Environment]::SetEnvironmentVariable($name, $goEnvironmentSnapshot[$name].Value, "Process")
      } else {
        [Environment]::SetEnvironmentVariable($name, $null, "Process")
      }
    } catch {
      $cleanupFailures += "$name restore: $($_.Exception.Message)"
    }
  }
  try {
    Restore-LLMGatewayEnvironment -Snapshot $environmentSnapshot
  } catch {
    $cleanupFailures += "LLMGATEWAY environment restore: $($_.Exception.Message)"
  }
  try {
    Pop-Location
  } catch {
    $cleanupFailures += "location restore: $($_.Exception.Message)"
  }
  if ($null -ne $verificationFailure) {
    if ($cleanupFailures.Count -gt 0) {
      throw "Verification failed: $($verificationFailure.Exception.Message) Cleanup also failed: $($cleanupFailures -join '; ')"
    }
    throw $verificationFailure
  }
  if ($cleanupFailures.Count -gt 0) {
    throw "Verification cleanup failed: $($cleanupFailures -join '; ')"
  }
}

Write-Host "`nLLMGateway verification passed."
