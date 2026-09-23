#!/usr/bin/env bash
# build-mac.sh — macOS universal DMG 打包脚本（必须在 Mac 上运行，Windows 不交叉编译 mac）
# 用法:
#   ./build-mac.sh           # 本地构建，只出 DMG
#   ./build-mac.sh --release # 发布构建：额外生成 .minisig 与 latest.json（含双平台合并）
#
# 产出：bin/cc-console-setup-macos-universal.dmg（--release 时另生成 .minisig 与根目录 latest.json）
#   - universal（arm64 + amd64 via lipo）
#   - .app bundle（含 cc-console / cc-console-sl / bridge.mjs / icons.icns / Info.plist）
#   - ad-hoc 签名（codesign --force --deep --sign -）
#   - hdiutil UDZO 压缩 DMG
#
# 双平台发布纪律（同 Windows 端 ./build.sh --release，规则对齐 toolbox 项目）：
#   - 双端版本号同步（都改 service/monitor_service.go 的 const Version）；
#   - latest.json 只收「同版本产物已就位」的平台条目，谁后构建谁把 manifest 补成双平台；
#   - 线上同版本已有 darwin 条目 → 中止（同版本产物永不覆盖，必须升版本）；
#   - 线上同版本缺 darwin（Windows 先发）→ 补齐模式：不升版本，合并 windows 条目后发布；
#   - 严禁把旧版本的平台条目照抄进新版本 manifest（旧包 + 新版本号 → 该平台无限更新循环）。
#
# 已知限制：trayicon.png 源为 32x32 低分辨率，icns 循环基于先放大到 1024x1024 的
# 源图切尺寸，图标模糊（功能不受影响），留待用户提供高清源优化。
set -euo pipefail
cd "$(dirname "$0")"

RELEASE_MODE=0
if [ "${1:-}" = "--release" ]; then
  RELEASE_MODE=1
elif [ -n "${1:-}" ]; then
  echo "未知参数: $1" >&2
  echo "用法: ./build-mac.sh [--release]" >&2
  exit 1
fi

# 确保 Go SDK 可用（CI 环境可能未预置到 PATH）
if ! command -v go >/dev/null 2>&1; then
  if [ -x "$HOME/go-sdk/go/bin/go" ]; then
    export GOROOT="$HOME/go-sdk/go"
    export PATH="$GOROOT/bin:$PATH"
  fi
fi
export GOPROXY="${GOPROXY:-https://goproxy.cn}"
export GOSUMDB="${GOSUMDB:-off}"

VER=$(grep -m1 'const Version' service/monitor_service.go | sed 's/.*"\(.*\)".*/\1/')
[ -n "$VER" ] || { echo "无法解析版本号"; exit 1; }
APP_NAME="cc-console"
APP="bin/${APP_NAME}.app"
# 发布资产名固定不带版本（GitHub Release 每个版本独立命名空间，URL 指向固定名；
# 同 setup.exe 的 cc-console-setup 前缀 + 面向人的 macos 标识，updater key 另用 darwin-*）
DMG="bin/cc-console-setup-macos-universal.dmg"
STAGE="bin/stage"

echo "==> [1/7] 前端构建（npm ci 宽容回退）"
( cd frontend
  # package-lock.json 可能含 npmmirror resolved 变更导致 npm ci 严格校验失败，
  # 改用 npm install（node_modules 已存在则快速幂等）。目标：dist 更新。
  npm ci || npm install
  npm run build
)

echo "==> [2/7] 生成 icons.icns（源图先规整到 1024x1024）"
# 先清理上次中间产物（arm64/amd64 二进制 + iconset），避免部分失败运行后残留陈旧 lipo 输入。
rm -rf "$STAGE"
mkdir -p "$STAGE/icon.iconset"
# trayicon.png 仅 32x32，直接放大到 1024 会有模糊，但能保证 iconutil 接受正方形源。
sips -z 1024 1024 trayicon.png --out "$STAGE/icon-src.png" >/dev/null
SRC="$STAGE/icon-src.png"
for spec in "16 16x16" "32 16x16@2x" "32 32x32" "64 32x32@2x" "128 128x128" "256 128x128@2x" "256 256x256" "512 256x256@2x" "512 512x512" "1024 512x512@2x"; do
  set -- $spec; sz=$1; name=$2
  sips -z $sz $sz "$SRC" --out "$STAGE/icon.iconset/icon_${name}.png" >/dev/null
done
iconutil -c icns "$STAGE/icon.iconset" -o "$STAGE/icons.icns"

echo "==> [3/7] universal 二进制（arm64 + amd64 lipo）"
mkdir -p bin
LDFLAGS="-s -w"
# 两个架构都显式启用 CGO 并指定 cgo target，使脚本 host-agnostic：
# Wails v3 的 pkg/mac 是 cgo（Objective-C），
#   - 在 Apple Silicon 上 GOARCH=amd64 会令 go 默认 CGO_ENABLED=0 → cgo 文件被排除 → 编译失败；
#   - 在 Intel（x86_64）host 上跑 GOARCH=arm64 时，cgo（ObjC）若交由 host 默认 clang 会编成 amd64，
#     与 Go 的 arm64 代码架构不匹配 → 链接失败。
# 显式指定 clang -target（arm64-apple-darwin / x86_64-apple-darwin）后，无论 host 是
# Apple Silicon 还是 Intel Mac，另一架构都能用系统 SDK 正确交叉编译。
# 前提：装了 Xcode/CLT 的 clang。
#
# 固定 macOS deployment target = 11.0（Big Sur）：
#   - 兑现 build/darwin/Info.plist 的 LSMinimumSystemVersion=11.0 声明，避免 Wails pkg/mac 的
#     ObjC 对象按本机 SDK（15.x）编译、在 11.0-14.x 上因弱链接触发未定义符号崩溃；
#   - 消除 ld "building for newer macOS version ... than being linked (11.0)" 告警。
# 关键：-mmacosx-version-min 取的是 macOS 产品版本（11.0 = Big Sur）；-target triple 保持无版本，
#   因为 triple 里的数字是 Darwin 内核版本（darwin11 = macOS 10.7 Lion），若写成
#   -target arm64-apple-darwin11.0 会错误指向 macOS 10.7，arm64 构建会失败
#   （arm64 macOS 起步于 11.0 = darwin20）。
export MACOSX_DEPLOYMENT_TARGET=11.0
export CGO_ENABLED=1
ARM64_CC="clang -target arm64-apple-darwin -mmacosx-version-min=11.0"
AMD64_CC="clang -target x86_64-apple-darwin -mmacosx-version-min=11.0"
# 主程序
CGO_ENABLED=1 GOARCH=arm64 CC="$ARM64_CC" go build -ldflags="$LDFLAGS" -o "$STAGE/cc-console-arm64" .
CGO_ENABLED=1 GOARCH=amd64 CC="$AMD64_CC" go build -ldflags="$LDFLAGS" -o "$STAGE/cc-console-amd64" .
lipo -create -output "$STAGE/cc-console" "$STAGE/cc-console-arm64" "$STAGE/cc-console-amd64"
# slhook 桥接二进制
CGO_ENABLED=1 GOARCH=arm64 CC="$ARM64_CC" go build -ldflags="$LDFLAGS" -o "$STAGE/cc-console-sl-arm64" ./cmd/slhook
CGO_ENABLED=1 GOARCH=amd64 CC="$AMD64_CC" go build -ldflags="$LDFLAGS" -o "$STAGE/cc-console-sl-amd64" ./cmd/slhook
lipo -create -output "$STAGE/cc-console-sl" "$STAGE/cc-console-sl-arm64" "$STAGE/cc-console-sl-amd64"

echo "==> [4/7] 构造 .app bundle"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$STAGE/cc-console"      "$APP/Contents/MacOS/cc-console"
cp "$STAGE/cc-console-sl"   "$APP/Contents/Resources/cc-console-sl"
cp cmd/slhook/bridge.mjs    "$APP/Contents/Resources/bridge.mjs"
cp "$STAGE/icons.icns"      "$APP/Contents/Resources/icons.icns"
sed "s/{{VERSION}}/$VER/g" build/darwin/Info.plist > "$APP/Contents/Info.plist"

echo "==> [5/7] ad-hoc 签名"
codesign --force --deep --sign - "$APP"

echo "==> [6/7] 打 DMG"
rm -f "$DMG"
hdiutil create -volname "$APP_NAME" -srcfolder "$APP" -ov -format UDZO "$DMG"

if [ "$RELEASE_MODE" -eq 0 ]; then
  echo ""
  echo "完成：$DMG（本地构建；发布请用 ./build-mac.sh --release）"
  echo "--- lipo -info ---"
  lipo -info "$APP/Contents/MacOS/cc-console"
  echo "--- codesign verify ---"
  codesign --verify --deep --strict "$APP" && echo "codesign: OK"
  exit 0
fi

echo ""
echo "==> [7/7] 处理更新发布元数据（minisign + latest.json）"
# 私钥与 Windows 端同一把（cc-console.sec / cc-console.local.sec，两台机器各存一份，
# 丢失即永远无法发布更新）。manifest 合并用 node（mac 不强求 jq，npm 必在）。
MINISIGN_KEY="cc-console.sec"
MINISIGN_KEY_LOCAL="cc-console.local.sec"
MANIFEST_URL="https://github.com/pie-tk/cc-console/releases/latest/download/latest.json"

if ! command -v minisign >/dev/null 2>&1; then
  echo "缺少 minisign：发布构建必须先安装（brew install minisign）" >&2
  exit 1
fi
if [ -f "$MINISIGN_KEY_LOCAL" ]; then
  echo "使用免密本地签名副本 $MINISIGN_KEY_LOCAL"
  minisign -S -s "$MINISIGN_KEY_LOCAL" -m "$DMG" -x "${DMG}.minisig" -t "cc-console v$VER"
else
  echo "使用加密私钥 $MINISIGN_KEY 签名（将提示输入口令）"
  minisign -S -s "$MINISIGN_KEY" -m "$DMG" -x "${DMG}.minisig" -t "cc-console v$VER"
fi
echo "已生成 ${DMG}.minisig"

# release notes：与 Windows 端 build.sh 同源——上一个版本 tag 到 HEAD 的提交标题
PREV_TAG="$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || true)"
if [ -n "$PREV_TAG" ]; then
  RELEASE_NOTES="$(git log "$PREV_TAG..HEAD" --pretty=format:'%s' --no-merges)"
else
  RELEASE_NOTES="$(git log -1 --pretty=format:'%s')"
fi
RELEASE_NOTES="${RELEASE_NOTES:-Release v$VER}"

# 守卫 + 合并 + 组装 latest.json（守卫规则见文件头）。universal dmg 同时挂
# darwin-arm64 / darwin-amd64 两个 key（客户端按 runtime.GOARCH 查表），指向同一 URL。
# 直连失败时回退 git 配置的 http 代理（部分网络环境 GitHub 需走代理）。
REMOTE_JSON="$(curl -fsSL --max-time 15 "$MANIFEST_URL" 2>/dev/null || true)"
if [ -z "$REMOTE_JSON" ]; then
  PROXY="$(git config --get http.proxy 2>/dev/null || true)"
  if [ -n "$PROXY" ]; then
    REMOTE_JSON="$(curl -fsSL --max-time 20 --proxy "$PROXY" "$MANIFEST_URL" 2>/dev/null || true)"
  fi
fi
printf '%s' "$REMOTE_JSON" > "$STAGE/remote-latest.json"
SIG="$(head -2 "${DMG}.minisig" | tr -d '\r')"
VER="$VER" SIG="$SIG" NOTES="$RELEASE_NOTES" REMOTE="$STAGE/remote-latest.json" \
node <<'EOF' > latest.json
const { VER: ver, SIG: sig, NOTES: notes, REMOTE: remote } = process.env;
const fs = require("fs");
const entry = {
  signature: sig,
  url: `https://github.com/pie-tk/cc-console/releases/download/v${ver}/cc-console-setup-macos-universal.dmg`,
};
const localPlatforms = { "darwin-arm64": entry, "darwin-amd64": entry };
let extra = {};
let remoteText = "";
try { remoteText = fs.readFileSync(remote, "utf8").trim(); } catch (e) {}
if (remoteText) {
  let m = null;
  try { m = JSON.parse(remoteText); } catch (e) {}
  if (!m || !m.version) {
    console.error("⚠️  线上 manifest 不可解析：按全新 manifest 生成，发布前人工确认线上状态");
  } else {
    console.error(`线上 manifest 版本：v${m.version}（本地 v${ver}）`);
    if (m.version === ver) {
      const p = m.platforms || {};
      if ("darwin-arm64" in p || "darwin-amd64" in p) {
        console.error(`✗ 线上 v${ver} 已含 darwin 条目：同版本同平台产物永不覆盖，请升版本号（Windows 端同步升）`);
        process.exit(1);
      }
      // 补齐模式：带上线上另一平台（如 windows-x86_64）的同版本条目
      for (const [k, v] of Object.entries(p)) if (!(k in localPlatforms)) extra[k] = v;
      console.error(`补齐模式：合并线上同版本平台条目 ${Object.keys(extra).join(", ") || "(无)"}`);
    } else {
      // 线上已有更新版本 → 中止（本地版本落后，先拉代码协调）
      const sem = (s) => s.split(".").map(Number);
      const [rv, lv] = [sem(m.version), sem(ver)];
      let newer = false;
      for (let i = 0; i < 3; i++) {
        if (rv[i] > lv[i]) { newer = true; break; }
        if (rv[i] < lv[i]) break;
      }
      if (newer) {
        console.error(`✗ 线上已有更新版本 v${m.version}（本地 v${ver}）：请先拉代码协调版本号`);
        process.exit(1);
      }
    }
  }
} else {
  console.error("⚠️  无法获取线上 manifest（网络不可达或尚无 release）：按全新 manifest 生成");
}
const manifest = {
  version: ver,
  notes: notes,
  pub_date: new Date().toISOString().replace(/\.\d+Z$/, "Z"),
  platforms: Object.assign({}, extra, localPlatforms),
};
process.stdout.write(JSON.stringify(manifest, null, 2) + "\n");
console.error(`已生成 latest.json（平台：${Object.keys(manifest.platforms).join(", ")}）`);
EOF

echo ""
echo "完成（release）：$DMG / ${DMG}.minisig / latest.json"
echo "--- lipo -info ---"
lipo -info "$APP/Contents/MacOS/cc-console"
echo "--- codesign verify ---"
codesign --verify --deep --strict "$APP" && echo "codesign: OK"
