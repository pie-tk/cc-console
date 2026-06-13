#!/usr/bin/env bash
# build.sh — 一键构建 claude-code-monitor（便携版 exe + Inno Setup 安装包）
# 用法: ./build.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# 清除 GOROOT 让 go 自动检测（环境变量中带引号的 GOROOT 会导致 go 找不到目录）
unset GOROOT

echo "=== 1/5 前端构建 ==="
cd frontend && npm run build && cd ..

echo ""
echo "=== 2/5 嵌入 Windows 图标资源 ==="
# rsrc 用于将 ICO 嵌入 Windows PE 资源（桌面/任务栏图标）
RSRC="$(go env GOPATH | tr -d '"')/bin/rsrc"
"$RSRC" -ico icon.ico -o rsrc.syso

echo ""
echo "=== 3/5 Go 编译便携版 ==="
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .

echo ""
echo "=== 3b/5 编译 statusline 桥接 helper ==="
go build -ldflags="-s -w" -o claude-monitor-sl.exe ./cmd/slhook
cp cmd/slhook/bridge.mjs bridge.mjs

echo ""
echo "=== 4/5 获取版本号 ==="
VERSION=$(grep 'const Version' service/monitor_service.go | sed 's/.*"\(.*\)".*/\1/')
echo "Version: $VERSION"

echo ""
echo "=== 5/5 生成 Inno Setup 安装包 ==="
powershell -Command "& 'C:\Users\PIE TK\AppData\Local\Programs\Inno Setup 6\ISCC.exe' /DMyAppVersion=$VERSION setup.iss"

echo ""
echo "=== 完成 ==="
ls -lh claude-monitor.exe claude-monitor-sl.exe claude-monitor-setup.exe
