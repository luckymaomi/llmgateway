param(
  [switch] $SkipIntegration,
  [switch] $SkipBrowser,
  [switch] $SkipBuildMatrix
)

$ErrorActionPreference = "Stop"

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

Push-Location (Join-Path $PSScriptRoot "..")
try {
  Invoke-Step "Environment" { powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-environment.ps1 }

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
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    $after = Get-ChildItem .\internal\store\db -File | Sort-Object FullName | ForEach-Object { "$($_.Name):$((Get-FileHash -Algorithm SHA256 $_.FullName).Hash)" }
    if (Compare-Object $before $after) { throw "sqlc generated output drifted." }
  }

  if (Test-Path .\web\package.json) {
    Invoke-Step "Frontend install integrity" { pnpm.cmd --dir web install --frozen-lockfile }
    Invoke-Step "Frontend checks" { pnpm.cmd --dir web run verify }
    if (-not $SkipBrowser) {
      Invoke-Step "Browser acceptance" { pnpm.cmd --dir web run test:e2e }
    }
  }

  Invoke-Step "Compose configuration" { docker compose config --quiet }
  if (-not $SkipIntegration) {
    Invoke-Step "Migration round-trip" { powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-migrations.ps1 }
    Invoke-Step "Core gateway flow" { powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-core.ps1 }
  }

  if (-not $SkipBuildMatrix) {
    Invoke-Step "Go build matrix" {
      $targets = @(
        @{ OS = "windows"; Arch = "amd64"; Suffix = ".exe" },
        @{ OS = "linux"; Arch = "amd64"; Suffix = "" },
        @{ OS = "linux"; Arch = "arm64"; Suffix = "" },
        @{ OS = "darwin"; Arch = "amd64"; Suffix = "" },
        @{ OS = "darwin"; Arch = "arm64"; Suffix = "" }
      )
      New-Item -ItemType Directory -Force .\.build | Out-Null
      foreach ($target in $targets) {
        $env:GOOS = $target.OS
        $env:GOARCH = $target.Arch
        $output = ".\.build\llmgateway-$($target.OS)-$($target.Arch)$($target.Suffix)"
        go build -trimpath -o $output .\cmd\gateway
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
      }
      Remove-Item Env:GOOS -ErrorAction SilentlyContinue
      Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    }
  }

  Write-Host "`nLLMGateway verification passed."
} finally {
  Remove-Item Env:GOOS -ErrorAction SilentlyContinue
  Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
  Pop-Location
}
