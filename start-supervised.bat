@echo off
REM Starts SMAGo silently with a system tray icon. Always does a clean
REM restart — kills any prior supervisor-bg and agent processes first.
REM See restart.bat for the kill logic.

call "%~dp0restart.bat" %*
