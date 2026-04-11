param(
    [string]$BindHost = "0.0.0.0"
)

$ErrorActionPreference = "Stop"

$scriptRoot = $PSScriptRoot
if (-not $scriptRoot) {
    $scriptRoot = Split-Path -Parent $PSCommandPath
}
if (-not $scriptRoot) {
    $scriptRoot = (Get-Location).Path
}
$root = Split-Path -Parent (Split-Path -Parent $scriptRoot)

Set-Location (Join-Path $root "frontend")
npm run dev -- --host $BindHost
