@echo off 

set script_path=%~dp0
call %script_path%env.bat

:: check system type,select and copy application package
if %PROCESSOR_ARCHITECTURE% == AMD64 (
    set procName=%cmd%_amd64
) else (
    set procName=%cmd%_386
)
copy %script_path%%procName%.exe %script_path%%cmd%.exe 1>nul 2>&1

:: copy .yml file
:: copy %script_path%%cmd%_template.yml %script_path%%cmd%.yml 1>nul 2>&1
