@echo off
setlocal
cd /d "%~dp0"

set "EXE=bin\fpc.exe"
if not exist "%EXE%" set "EXE=dist\fpc-windows-amd64.exe"
if not exist "%EXE%" (
  echo Binary not found. Build it first:
  echo   go build -o bin\fpc.exe .\cmd\fpc
  echo or: bash scripts\build.sh
  echo.
  pause
  exit /b 1
)

rem ==== EDIT THESE LINES ONCE, THEN JUST DOUBLE-CLICK THIS FILE ==========
rem FPC_SERVER  : the fps control address (host:port, no scheme)
rem FPC_TOKEN   : the tx_ token printed by fps on first start
rem               (or a token created in the admin console)
rem FPC_TUNNEL  : name=type://local-service  (http or tcp), repeatable
set "FPC_SERVER=127.0.0.1:7000"
set "FPC_TOKEN=REPLACE_WITH_tx_TOKEN"
set "FPC_TUNNEL=web=http://127.0.0.1:8080"
rem ======================================================================

rem You may also leave FPC_TOKEN empty and enter it later in the web UI.
rem The local console opens with a locally generated login password.
set "TUNNELX_OPEN_BROWSER=1"

echo ============================================================
echo  TunnelX client (fpc) - double-click launcher
echo ============================================================
echo  Local console : http://127.0.0.1:7200/
echo  The LOGIN PASSWORD is printed below on first start.
echo  The tx_ server token is entered later in the "Config" page.
echo.
echo  Keep this window open. Press Ctrl+C to stop.
echo.

if "%FPC_TOKEN%"=="REPLACE_WITH_tx_TOKEN" (
  echo No server token configured yet: the console will open so you can
  echo enter it in the Config page. To skip that step, edit this file
  echo and replace FPC_TOKEN with the tx_ token from fps.
  echo.
  "%EXE%" --server %FPC_SERVER% --tunnel %FPC_TUNNEL% --insecure %*
) else (
  "%EXE%" --server %FPC_SERVER% --token %FPC_TOKEN% --tunnel %FPC_TUNNEL% --insecure %*
)
set "CODE=%ERRORLEVEL%"
echo.
echo fpc exited with code %CODE%.
pause
