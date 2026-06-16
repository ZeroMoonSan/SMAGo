@echo off
REM restart.bat — guaranteed clean restart of SMAGo.
REM Kills any supervisor-bg and agent processes left over from a previous
REM run, clears the pid files, then starts the supervisor fresh.

setlocal

echo === Killing any running SMAGo processes ===

REM Kill supervisor-bg by recorded pid (if the file exists).
if exist data\supervisor.pid (
  for /f "delims=" %%P in (data\supervisor.pid) do (
    echo   taskkill supervisor-bg PID=%%P
    taskkill /PID %%P /F >nul 2>&1
  )
  del data\supervisor.pid >nul 2>&1
)

REM Belt-and-suspenders: kill by image name. The supervisor will have
REM respawned the agent after we killed it, so we sweep both names.
taskkill /IM supervisor-bg.exe /F >nul 2>&1
taskkill /IM agent.exe /F >nul 2>&1

REM Give Windows a moment to release the pid files.
timeout /t 1 /nobreak >nul

REM Drop any leftover pid files.
if exist data\smago.pid del data\smago.pid >nul 2>&1
if exist data\supervisor.pid del data\supervisor.pid >nul 2>&1

if not exist supervisor-bg.exe (
  echo supervisor-bg.exe not found. Run build.bat first.
  pause
  exit /b 1
)

echo === Starting supervisor-bg ===
start "" /b supervisor-bg.exe
echo SMAGo started. Look for the tray icon.

endlocal
