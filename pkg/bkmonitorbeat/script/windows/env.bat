@echo off
set script_path=%~dp0
set cmd=bkmonitorbeat
set config=%cmd%.yml
set ident=bkmonitorbeat_daemon
for /F "tokens=1,* delims= " %%i in (%script_path%VERSION) do (set version=%%i)