@echo off
if not exist bin\supervisor-bg.exe (
  echo bin\supervisor-bg.exe not found. Run build.bat first.
  pause
  exit /b 1
)
start "" bin\supervisor-bg.exe
echo Started SMAGo supervisor.
