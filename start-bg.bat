@echo off
REM Start SMAGo in the background (no console window).
REM Logs go to data\smago.log. PID in data\smago.pid.

if not exist bin\smago-bg.exe (
  echo bin\smago-bg.exe not found. Run build.bat first.
  pause
  exit /b 1
)

if exist data\smago.pid (
  for /f "delims=" %%P in (data\smago.pid) do (
    tasklist /FI "PID eq %%P" 2>nul | find /I "smago-bg" >nul
    if not errorlevel 1 (
      echo Already running with PID %%P.
      exit /b 0
    )
  )
  del data\smago.pid >nul 2>&1
)

start "" /b bin\smago-bg.exe
echo Started SMAGo in background. Logs: data\smago.log
endlocal
