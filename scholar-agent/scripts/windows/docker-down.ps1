$ErrorActionPreference = "Stop"

$scriptRoot = $PSScriptRoot
if (-not $scriptRoot) {
    $scriptRoot = Split-Path -Parent $PSCommandPath
}
if (-not $scriptRoot) {
    $scriptRoot = (Get-Location).Path
}
$root = Split-Path -Parent (Split-Path -Parent $scriptRoot)

Set-Location $root
docker compose down
