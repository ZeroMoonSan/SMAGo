@echo off
setlocal
cd /d "%~dp0src"

if not exist ..\bin\smago.ico (
  echo === Generating tray icon ===
  go run ./cmd/genicon ..\bin\smago.ico
  if errorlevel 1 exit /b 1
)
echo === Building agent (console) ===
go build -o ..\bin\agent.exe .
if errorlevel 1 exit /b 1
echo === Building agent (background, no console) ===
go build -ldflags="-H windowsgui" -o ..\bin\smago-bg.exe .
if errorlevel 1 exit /b 1
echo === Building supervisor (with tray icon) ===
go build -ldflags="-H windowsgui" -o ..\bin\supervisor-bg.exe ./cmd/supervisor
if errorlevel 1 exit /b 1
echo === Seed v0 (copy of current agent) ===
if not exist ..\data\versions\v0 mkdir ..\data\versions\v0
copy /Y ..\bin\agent.exe ..\data\versions\v0\agent.exe >nul
echo.
echo Done.
echo   bin\agent.exe         - console (for debugging)
echo   bin\smago-bg.exe      - background, no console
echo   bin\supervisor-bg.exe - silent supervisor with system tray icon
echo.
echo Start:  start-supervised.bat
endlocal
