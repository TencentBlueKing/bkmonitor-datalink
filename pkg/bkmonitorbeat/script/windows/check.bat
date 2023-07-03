@echo off

set script_path=%~dp0
call %script_path%env.bat
:: output version
for /f "tokens=1,* delims= " %%i in (%script_path%VERSION) do (echo version: %%i)

:: output process id
set pid=""
for /F "tokens=1,2,* delims==" %%i in ('wmic process where "Caption='%cmd%.exe'" get ProcessId/value^| findstr ProcessId') do (set pid=%%j)
if %pid% == "" (exit 102)
echo pid: %pid%