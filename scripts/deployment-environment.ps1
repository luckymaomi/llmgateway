$ErrorActionPreference = "Stop"

function Read-LLMGatewayEnvironmentFile {
  param([Parameter(Mandatory = $true)][string] $Path)

  $resolved = [IO.Path]::GetFullPath($Path)
  if (-not (Test-Path -LiteralPath $resolved -PathType Leaf)) {
    throw "Deployment environment file does not exist: $resolved"
  }
  $values = @{}
  $lineNumber = 0
  foreach ($line in Get-Content -LiteralPath $resolved -Encoding UTF8) {
    $lineNumber++
    $trimmed = $line.Trim()
    if (-not $trimmed -or $trimmed.StartsWith("#")) { continue }
    $parts = $trimmed -split "=", 2
    if ($parts.Count -ne 2 -or $parts[0] -notmatch '^LLMGATEWAY_[A-Z0-9_]+$') {
      throw "Invalid deployment environment entry at line $lineNumber."
    }
    $name = $parts[0]
    if ($values.ContainsKey($name)) { throw "Duplicate deployment environment key: $name" }
    $values[$name] = $parts[1].Trim()
  }
  return $values
}

function Assert-LLMGatewayFileSecrets {
  param(
    [Parameter(Mandatory = $true)][hashtable] $Values,
    [switch] $IncludeStorageBootstrap
  )

  $inlineSecretKeys = @(
    "LLMGATEWAY_DATABASE_URL",
    "LLMGATEWAY_VALKEY_PASSWORD",
    "LLMGATEWAY_MASTER_KEYS",
    "LLMGATEWAY_SESSION_PEPPER",
    "LLMGATEWAY_API_KEY_PEPPER",
    "LLMGATEWAY_COORDINATION_KEY_HASH_SECRET"
  )
  foreach ($name in $inlineSecretKeys) {
    if ($Values.ContainsKey($name)) { throw "$name must use its explicit _FILE input in production." }
  }
  $required = @(
    "LLMGATEWAY_DATABASE_URL_FILE",
    "LLMGATEWAY_VALKEY_PASSWORD_FILE",
    "LLMGATEWAY_MASTER_KEYS_FILE",
    "LLMGATEWAY_SESSION_PEPPER_FILE",
    "LLMGATEWAY_API_KEY_PEPPER_FILE",
    "LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE"
  )
  if ($IncludeStorageBootstrap) {
    $required += "LLMGATEWAY_POSTGRES_PASSWORD_FILE", "LLMGATEWAY_VALKEY_ACL_FILE"
  }
  foreach ($name in $required) {
    if (-not $Values.ContainsKey($name) -or -not $Values[$name]) { throw "$name is required." }
    $path = [IO.Path]::GetFullPath($Values[$name])
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "$name does not name a readable file." }
    $info = Get-Item -LiteralPath $path
    if ($info.Length -le 0 -or $info.Length -gt 65536) { throw "$name must contain 1..65536 bytes." }
    $Values[$name] = $path
  }
}

function Set-LLMGatewayProcessEnvironment {
  param([Parameter(Mandatory = $true)][hashtable] $Values)

  foreach ($item in @(Get-ChildItem Env: | Where-Object { $_.Name -like "LLMGATEWAY_*" })) {
    [Environment]::SetEnvironmentVariable($item.Name, $null, "Process")
  }
  foreach ($name in $Values.Keys) {
    [Environment]::SetEnvironmentVariable($name, [string]$Values[$name], "Process")
  }
}
