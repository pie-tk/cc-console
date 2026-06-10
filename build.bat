@echo off
REM Build Windows GUI binary (no console window, stripped debug info)
REM Requires Go 1.26+: https://go.dev/dl/
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .
if errorlevel 1 (
    echo.
    echo BUILD FAILED
    pause
    exit /b 1
)
echo BUILD OK: claude-monitor.exe
