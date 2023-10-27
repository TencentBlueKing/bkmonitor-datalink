@echo off

set script_path=%~dp0
call %script_path%env.bat

if not exist %script_path%%cmd%.exe (
    echo "program not exist"
    exit 1
)
start /B %script_path%%cmd%.exe -httpprof localhost:6069 -E IDENT=%ident% -E VERSION=%version% -c %config% 1>nul 2>&1