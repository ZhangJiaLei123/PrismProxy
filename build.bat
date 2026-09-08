@echo off
setlocal EnableExtensions EnableDelayedExpansion
chcp 65001 >nul 2>&1

rem ============================================================
rem  PrismProxy one-click build and package script
rem  Usage:
rem    build.bat              normal release build (embedded WebView2)
rem    build.bat nsis         build + NSIS installer
rem    build.bat debug        debug build
rem    build.bat upx          release build + UPX compression
rem    build.bat clean        clean all build artifacts first
rem    build.bat -h / help    show this help
rem  Multiple options can be combined, e.g.  build.bat clean upx
rem ============================================================

set "MODE=release"
set "DO_NSIS=0"
set "DO_UPX=0"
set "DO_CLEAN=0"
set "VERSION=1.1.0"

rem ---------- parse arguments ----------
if "%~1"=="" goto :args_done
:arg_loop
if "%~1"=="" goto :args_done
set "ARG=%~1"
if /i "%ARG%"=="-h"       goto :usage
if /i "%ARG%"=="/h"       goto :usage
if /i "%ARG%"=="--help"   goto :usage
if /i "%ARG%"=="help"     goto :usage
if /i "%ARG%"=="nsis"     (set "DO_NSIS=1" & goto :arg_next)
if /i "%ARG%"=="installer"(set "DO_NSIS=1" & goto :arg_next)
if /i "%ARG%"=="upx"      (set "DO_UPX=1" & goto :arg_next)
if /i "%ARG%"=="clean"    (set "DO_CLEAN=1" & goto :arg_next)
if /i "%ARG%"=="debug"    (set "MODE=debug" & goto :arg_next)
if /i "%ARG%"=="release"  (set "MODE=release" & goto :arg_next)
echo [WARN] Unknown argument ignored: %ARG%
:arg_next
shift
goto :arg_loop
:args_done

rem ---------- switch to script directory (project root) ----------
cd /d "%~dp0"

echo ============================================================
echo   PrismProxy Build
echo   Mode     : %MODE%
echo   NSIS     : %DO_NSIS%
echo   UPX      : %DO_UPX%
echo   Clean    : %DO_CLEAN%
echo   Version  : %VERSION%
echo ============================================================

rem ---------- environment checks ----------
echo.
echo [1/5] Checking build environment...

where wails >nul 2>&1
if errorlevel 1 (
    echo [ERROR] wails CLI not found in PATH.
    echo         Install it with:  go install github.com/wailsapp/wails/v2/cmd/wails@latest
    exit /b 1
)
for /f "delims=" %%i in ('wails version 2^>nul ^| findstr /r "^v[0-9]"') do echo         wails  : %%i

where go >nul 2>&1
if errorlevel 1 (
    echo [ERROR] go toolchain not found in PATH.
    exit /b 1
)
for /f "delims=" %%i in ('go version') do echo         %%i

where node >nul 2>&1
if errorlevel 1 (
    echo [ERROR] node not found in PATH. Node.js 18+ is required.
    exit /b 1
)
for /f "delims=" %%i in ('node --version') do echo         node   : %%i

rem ---------- abort if PrismProxy is running (exe locked) ----------
tasklist /fi "imagename eq PrismProxy.exe" 2>nul | find /i "PrismProxy.exe" >nul
if not errorlevel 1 (
    echo [ERROR] PrismProxy.exe is currently running. Please close it before building.
    exit /b 1
)

rem ---------- clean ----------
rem NOTE: clean removes build\bin but preserves build\bin\config under it
rem (Root CA cert/key, settings.json, ctl token). Otherwise config\ca is lost
rem after rebuild -> LoadOrCreateCA generates a brand-new CA while the trusted
rem root store still holds the old one -> all HTTPS handshakes fail and fall
rem back to blind tunneling (list shows CONNECT only); the cert has to be
rem reinstalled.
rem (Keep this file ASCII-only: cmd.exe mis-parses UTF-8 multibyte chars after
rem chcp 65001 and may execute fragments of rem comments as commands.)
if "%DO_CLEAN%"=="1" (
    echo.
    echo [2/5] Cleaning previous build artifacts [keeping build\bin\config]...
    if exist "build\_config_keep" rmdir /s /q "build\_config_keep"
    if exist "build\bin\config" move /y "build\bin\config" "build\_config_keep" >nul
    if exist "build\bin"       rmdir /s /q "build\bin"
    if exist "frontend\dist"   rmdir /s /q "frontend\dist"
    if exist "dist"            rmdir /s /q "dist"
    if exist "build\_config_keep" (
        mkdir "build\bin" 2>nul
        move /y "build\_config_keep" "build\bin\config" >nul
        echo         Preserved build\bin\config [CA cert + settings].
    )
    echo         Done.
) else (
    echo.
    echo [2/5] Skipping clean.
)

rem ---------- build ----------
echo.
echo [3/5] Building (wails build - this also builds the frontend)...

set "BUILD_CMD=wails build -trimpath -webview2 embed -ldflags "-s -w""

if "%MODE%"=="debug" set "BUILD_CMD=wails build -debug -devtools -webview2 embed"
if "%DO_NSIS%"=="1"  set "BUILD_CMD=%BUILD_CMD% -nsis"
if "%DO_UPX%"=="1"   set "BUILD_CMD=%BUILD_CMD% -upx"

echo         %BUILD_CMD%
echo.
%BUILD_CMD%
if errorlevel 1 (
    echo.
    echo [ERROR] Build failed.
    exit /b 1
)

rem ---------- verify artifact ----------
echo.
echo [4/5] Verifying build output...
if not exist "build\bin\PrismProxy.exe" (
    echo [ERROR] build\bin\PrismProxy.exe not found after build.
    exit /b 1
)
for %%f in ("build\bin\PrismProxy.exe") do (
    set "EXE_SIZE=%%~zf"
    set "EXE_DATE=%%~tf"
)
echo         PrismProxy.exe
echo         Size : !EXE_SIZE! bytes
echo         Time : !EXE_DATE!

rem ---------- package ----------
echo.
echo [5/5] Packaging zip to dist\...
if not exist "dist" mkdir "dist"

if "%MODE%"=="debug" (
    set "PKG_NAME=PrismProxy-%VERSION%-debug-win64"
) else (
    set "PKG_NAME=PrismProxy-%VERSION%-win64"
)
if "%DO_UPX%"=="1" set "PKG_NAME=!PKG_NAME!-upx"

set "STAGE=build\bin\_stage\!PKG_NAME!"
if exist "build\bin\_stage" rmdir /s /q "build\bin\_stage"
mkdir "!STAGE!"
copy /y "build\bin\PrismProxy.exe" "!STAGE!\" >nul

if exist "dist\!PKG_NAME!.zip" del /q "dist\!PKG_NAME!.zip"
powershell -NoProfile -Command "Compress-Archive -Path '!STAGE!\*' -DestinationPath 'dist\!PKG_NAME!.zip' -Force"
if errorlevel 1 (
    echo [WARN] zip packaging failed, raw exe is still in build\bin\.
    rmdir /s /q "build\bin\_stage"
    exit /b 1
)
rmdir /s /q "build\bin\_stage"

if exist "build\bin\PrismProxy_amd64-installer.exe" (
    copy /y "build\bin\PrismProxy_amd64-installer.exe" "dist\!PKG_NAME!-setup.exe" >nul
    echo         Installer: dist\!PKG_NAME!-setup.exe
)

echo.
echo ============================================================
echo   Build completed successfully.
echo   Exe : build\bin\PrismProxy.exe
echo   Zip : dist\!PKG_NAME!.zip
echo ============================================================
echo.
pause
exit /b 0

:usage
echo.
echo PrismProxy one-click build script
echo.
echo Usage:
echo   build.bat                Release build (trimpath, stripped, embedded WebView2)
echo   build.bat debug          Debug build with DevTools enabled
echo   build.bat nsis          Also generate NSIS installer (needs NSIS installed)
echo   build.bat upx           Compress binary with UPX (needs UPX installed)
echo   build.bat clean         Remove build/bin, frontend/dist, dist before building
echo                           (keeps build\bin\config: CA cert, settings, ctl token)
echo   build.bat help          Show this help
echo.
echo Options are combinable, e.g.  build.bat clean upx
echo.
exit /b 0
