@echo off

set script_path=%~dp0
call %script_path%env.bat
%script_path%%cmd%.exe -E %cmd%.exe.mode=check -strict.perms false -path.data %temp% -path.logs %temp% -T -c %1 || exit 1