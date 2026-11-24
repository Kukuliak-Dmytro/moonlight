@echo off
REM Emergency rescue script: pauses moonlight daemon and restores safe brightness & neutral RGB.
REM Tip: Create a Desktop shortcut to this file and assign a Shortcut key (e.g. Ctrl+Alt+R).

setlocal
set "SCRIPT_DIR=%~dp0"
set "BIN=%SCRIPT_DIR%..\dist\moonlight-windows-amd64.exe"

if exist "%BIN%" (
    "%BIN%" reset
) else (
    moonlight reset
)
pause
