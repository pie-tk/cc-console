@echo off
REM 构建 Windows GUI 版本（无控制台窗口、剥离调试信息）
REM 需要先安装 Go 1.26+：https://go.dev/dl/
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .
if errorlevel 1 (
    echo 构建失败
    exit /b 1
)
echo 构建成功: claude-monitor.exe
