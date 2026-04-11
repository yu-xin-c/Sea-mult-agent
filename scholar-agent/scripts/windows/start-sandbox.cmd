@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "SCRIPT_DIR=%~dp0"
if "%SCRIPT_DIR:~-1%"=="\" set "SCRIPT_DIR=%SCRIPT_DIR:~0,-1%"
for %%I in ("%SCRIPT_DIR%\..\..") do set "ROOT=%%~fI"
set "DOTENV=%ROOT%\backend.env"
set "GOCACHE_DIR=%ROOT%\.gocache"

for /d %%D in ("%ROOT%\.gocache_verify*") do (
  if exist "%%~fD" rmdir /s /q "%%~fD" 2>nul
)

call :load_env "%DOTENV%"

if not defined OPEN_SANDBOX_URL set "OPEN_SANDBOX_URL=http://localhost:8081"
if not defined ENABLE_OPENSANDBOX_FALLBACK set "ENABLE_OPENSANDBOX_FALLBACK=false"

if not exist "%GOCACHE_DIR%" mkdir "%GOCACHE_DIR%"

set "GOCACHE=%GOCACHE_DIR%"
set "HTTP_PROXY="
set "HTTPS_PROXY="
set "ALL_PROXY="
set "http_proxy="
set "https_proxy="
set "all_proxy="
set "NO_PROXY=localhost,127.0.0.1,::1"
set "no_proxy=localhost,127.0.0.1,::1"

cd /d "%ROOT%\docker-sandbox"
go run main.go
exit /b %errorlevel%

:load_env
if not exist "%~1" exit /b 0
for /f "usebackq tokens=* delims=" %%L in ("%~1") do (
  set "line=%%L"
  if defined line if not "!line:~0,1!"=="#" call :parse_line "%%L"
)
exit /b 0

:parse_line
set "entry=%~1"
for /f "tokens=1* delims==" %%A in ("%entry%") do (
  set "key=%%A"
  set "value=%%B"
)
if defined key set "%key%=%value%"
set "key="
set "value="
exit /b 0
