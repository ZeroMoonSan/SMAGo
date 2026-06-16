@echo off  
timeout /t 2 /nobreak >nul  
start \"\" agent.exe --smago-version=v1 --smago-supervisor=1 
