param(
    [string]$ApiKey,
    [string]$BaseUrl,
    [string]$ModelName,
    [string]$SandboxUrl
)

$ErrorActionPreference = "Stop"

function Import-DotEnv {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    if (-not (Test-Path $Path)) {
        return
    }

    Get-Content -LiteralPath $Path | ForEach-Object {
        $line = $_.Trim()
        if (-not $line -or $line.StartsWith("#")) {
            return
        }

        $idx = $line.IndexOf("=")
        if ($idx -lt 1) {
            return
        }

        $name = $line.Substring(0, $idx).Trim()
        $value = $line.Substring($idx + 1).Trim().Trim('"').Trim("'")
        [System.Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}

$scriptRoot = $PSScriptRoot
if (-not $scriptRoot) {
    $scriptRoot = Split-Path -Parent $PSCommandPath
}
if (-not $scriptRoot) {
    $scriptRoot = (Get-Location).Path
}
$root = Split-Path -Parent (Split-Path -Parent $scriptRoot)
$dotenvFile = Join-Path $root "backend.env"
$legacyEnvFile = Join-Path $root "backend.env.ps1"
$gocache = Join-Path $root ".gocache"

Get-ChildItem -LiteralPath $root -Force -Directory -Filter ".gocache_verify*" -ErrorAction SilentlyContinue | ForEach-Object {
    try {
        Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction Stop
    } catch {
    }
}

if (-not (Test-Path $gocache)) {
    New-Item -ItemType Directory -Path $gocache | Out-Null
}

Import-DotEnv -Path $dotenvFile

if (Test-Path $legacyEnvFile) {
    . $legacyEnvFile
}

if (-not $ApiKey) {
    $ApiKey = $env:OPENAI_API_KEY
}
if (-not $BaseUrl) {
    $BaseUrl = $env:OPENAI_BASE_URL
}
if (-not $ModelName) {
    $ModelName = $env:OPENAI_MODEL_NAME
}
if (-not $SandboxUrl) {
    $SandboxUrl = $env:SANDBOX_URL
}

if (-not $ApiKey) {
    throw "OPENAI_API_KEY not set. Configure backend.env or backend.env.ps1, or run .\scripts\windows\start-backend.ps1 -ApiKey 'your-key'"
}
if (-not $BaseUrl) {
    $BaseUrl = "https://dashscope.aliyuncs.com/compatible-mode/v1"
}
if (-not $ModelName) {
    $ModelName = "qwen3-coder-plus"
}
if (-not $SandboxUrl) {
    $SandboxUrl = "http://localhost:8082"
}

$env:OPENAI_API_KEY = $ApiKey
$env:OPENAI_BASE_URL = $BaseUrl
$env:OPENAI_MODEL_NAME = $ModelName
$env:SANDBOX_URL = $SandboxUrl
$env:HTTP_PROXY = ""
$env:HTTPS_PROXY = ""
$env:ALL_PROXY = ""
$env:http_proxy = ""
$env:https_proxy = ""
$env:all_proxy = ""
$env:NO_PROXY = "localhost,127.0.0.1,::1"
$env:no_proxy = "localhost,127.0.0.1,::1"
$env:GOCACHE = $gocache

Set-Location (Join-Path $root "backend")
go run cmd/api/main.go
