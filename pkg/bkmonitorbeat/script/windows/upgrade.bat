@echo off

set script_path=%~dp0
call %script_path%env.bat
set package=%1
set old_dir=%cmd%.old

:: not specify package, exit
if x%package%x == xx (
    echo "not package specified"
    exit 1
)

:: backup the old files
rd /S /Q %script_path%%old_dir% >nul 2>&1
md %script_path%%old_dir% >nul 2>&1
move %script_path%%cmd%.exe %script_path%%old_dir% >nul 2>&1
move %script_path%%cmd%_* %script_path%%old_dir% >nul 2>&1
move %script_path%VERSION %script_path%%old_dir% >nul 2>&1
(
  echo @echo off
  echo for /f "delims=" %%%%i in ^('dir /b /a-d %script_path% ^^^| findstr -I ".bat"'^) do ^(move %script_path%%%%%i %script_path%%old_dir%^)
  echo %package% -y -o%script_path%
  echo del %package%
  echo call %script_path%install.bat
  echo call %script_path%restart.bat
  echo call %script_path%check.bat
  echo del %script_path%%old_dir%\temp.bat
)>%script_path%%old_dir%\temp.bat

:: extract new files, delete new package
start /B cmd /k %script_path%%old_dir%\temp.bat