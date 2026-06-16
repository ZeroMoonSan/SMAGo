@echo off
if not exist data\smago.pid (
  echo Not running (no pid file).
  exit /b 0
)
for /f "delims=" %%P in (data\smago.pid) do (
  taskkill /PID %%P /F >nul 2>&1
)
del data\smago.pid >nul 2>&1
echo Stopped SMAGo.
endlocal
