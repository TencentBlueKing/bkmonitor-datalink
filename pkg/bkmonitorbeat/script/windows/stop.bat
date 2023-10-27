@echo off

set script_path=%~dp0
call %script_path%env.bat
set pid=""
for /f "tokens=1,2,* delims==" %%i in ('wmic process where "Caption='%cmd%.exe'" get ProcessId/value 2^>nul^| findstr ProcessId') do (set pid=%%j)
if %pid% == "" (goto :end)
taskkill /PID %pid% /T /F
if %ERRORLEVEL% NEQ 0 EXIT 1

:end