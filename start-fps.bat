@echo off
setlocal
cd /d "%~dp0"

set "EXE=bin\fps.exe"
if not exist "%EXE%" set "EXE=dist\fps-windows-amd64.exe"
if not exist "%EXE%" (
  echo Binary not found. Build it first:
  echo   go build -o bin\fps.exe .\cmd\fps
  echo or: bash scripts\build.sh
  echo.
  pause
  exit /b 1
)

set "TUNNELX_OPEN_BROWSER=1"

echo ============================================================
echo  TunnelX server (fps) - double-click launcher
echo ============================================================
echo  Admin console : http://127.0.0.1:7100/admin/
echo  Control plane : 0.0.0.0:7000  TLS  (fpc connects here)
echo  Public HTTP   : 0.0.0.0:8080
echo  Metrics       : http://127.0.0.1:9090/metrics
echo.
echo  First start prints the admin password and the tx_ client token.
echo  Keep this window open. Press Ctrl+C to stop.
echo  The browser opens automatically once the console is ready.
echo.
"%EXE%" %*
set "CODE=%ERRORLEVEL%"
echo.
echo fps exited with code %CODE%.
pause
