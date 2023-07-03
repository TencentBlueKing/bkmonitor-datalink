@echo off

set script_path=%~dp0
call %script_path%env.bat
call %script_path%stop.bat
rmdir /S /Q %script_path% 1>nul 2>&1||exit 0