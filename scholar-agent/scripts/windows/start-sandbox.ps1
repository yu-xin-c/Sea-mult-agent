param(
    [string]$OpenSandboxUrl
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

if (-not $OpenSandboxUrl) {
    $OpenSandboxUrl = $env:OPEN_SANDBOX_URL
}
if (-not $OpenSandboxUrl) {
    $OpenSandboxUrl = "http://localhost:8081"
}
$enableFallback = $env:ENABLE_OPENSANDBOX_FALLBACK
if (-not $enableFallback) {
    $enableFallback = "false"
}

$env:GOCACHE = $gocache
$env:OPEN_SANDBOX_URL = $OpenSandboxUrl
$env:ENABLE_OPENSANDBOX_FALLBACK = $enableFallback
$env:HTTP_PROXY = ""
$env:HTTPS_PROXY = ""
$env:ALL_PROXY = ""
$env:http_proxy = ""
$env:https_proxy = ""
$env:all_proxy = ""
$env:NO_PROXY = "localhost,127.0.0.1,::1"
$env:no_proxy = "localhost,127.0.0.1,::1"

Set-Location (Join-Path $root "docker-sandbox")
go run main.go
