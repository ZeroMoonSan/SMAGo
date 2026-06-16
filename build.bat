@echo off
setlocal
if not exist smago.ico (
  echo === Generating tray icon ===
  go run ./cmd/genicon smago.ico
  if errorlevel 1 exit /b 1
)
echo === Building agent (console) ===
go build -o agent.exe .
if errorlevel 1 exit /b 1
echo === Building agent (background, no console) ===
go build -ldflags "-H windowsgui" -o smago-bg.exe .
if errorlevel 1 exit /b 1
echo === Building supervisor (with tray icon) ===
go build -ldflags "-H windowsgui" -o supervisor-bg.exe ./cmd/supervisor
if errorlevel 1 exit /b 1
echo === Seed v0 (copy of current agent) ===
if not exist data\versions\v0 mkdir data\versions\v0
copy /Y agent.exe data\versions\v0\agent.exe >nul
echo.
echo Done.
echo   agent.exe         - console (for debugging)
echo   smago-bg.exe      - background, no console
echo   supervisor-bg.exe - silent supervisor with system tray icon
echo.
echo Start:  start-supervised.bat
endlocal
