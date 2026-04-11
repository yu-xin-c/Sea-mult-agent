@echo off
setlocal EnableExtensions

set "SCRIPT_DIR=%~dp0"
if "%SCRIPT_DIR:~-1%"=="\" set "SCRIPT_DIR=%SCRIPT_DIR:~0,-1%"
for %%I in ("%SCRIPT_DIR%\..\..") do set "ROOT=%%~fI"
set "HOST=%~1"
if not defined HOST set "HOST=0.0.0.0"

cd /d "%ROOT%\frontend"
npm run dev -- --host %HOST%
exit /b %errorlevel%
