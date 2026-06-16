@echo off
REM Start SMAGo under supervisor (the proper production mode).
REM supervisor.exe launches agent.exe, watches it, handles upgrades,
REM rolls back on crash.

if not exist supervisor.exe (
  echo supervisor.exe not found. Run build.bat first.
  pause
  exit /b 1
)

REM If a previous supervisor is running, kill it.
if exist data\supervisor.pid (
  for /f "delims=" %%P in (data\supervisor.pid) do (
    taskkill /PID %%P /F >nul 2>&1
  )
  del data\supervisor.pid >nul 2>&1
)

start "" /b supervisor.exe
echo Started supervisor. Agent will launch in a moment.
echo   supervisor health: http://127.0.0.1:7778/health
echo   inject (when running): http://127.0.0.1:7777/inject
endlocal
