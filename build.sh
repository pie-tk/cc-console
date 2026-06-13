#!/usr/bin/env bash
# build.sh — 一键构建 claude-code-monitor（便携版 exe + Inno Setup 安装包）
# 用法: ./build.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "=== 1/4 前端构建 ==="
cd frontend && npm run build && cd ..

echo ""
echo "=== 2/4 Go 编译便携版 ==="
# 清除 GOROOT 让 go 自动检测（环境变量中带引号的 GOROOT 会导致 go 找不到目录）
unset GOROOT
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .

echo ""
echo "=== 3/4 获取版本号 ==="
VERSION=$(grep 'const Version' service/monitor_service.go | sed 's/.*"\(.*\)".*/\1/')
echo "Version: $VERSION"

echo ""
echo "=== 4/4 生成 Inno Setup 安装包 ==="
powershell -Command "& 'C:\Users\PIE TK\AppData\Local\Programs\Inno Setup 6\ISCC.exe' /DMyAppVersion=$VERSION setup.iss"

echo ""
echo "=== 完成 ==="
ls -lh claude-monitor.exe claude-monitor-setup.exe
