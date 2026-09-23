#!/usr/bin/env bash
# build.sh — 一键构建 cc-console（便携版 exe + Inno Setup 安装包）
# 用法:
#   ./build.sh           # 本地构建，可跳过更新发布元数据
#   ./build.sh --release # 发布构建，强制生成 .minisig 与 latest.json
#
# 双平台发布纪律（macOS 在 Mac 上跑 ./build-mac.sh --release，规则相同）：
#   - latest.json 只收「同版本产物已就位」的平台条目，谁后构建谁把 manifest 补成双平台；
#   - 线上同版本已有本平台条目 → 中止（同版本产物永不覆盖，必须升版本）；
#   - 线上同版本缺本平台（另一端先发）→ 补齐模式：不升版本，合并另一平台条目后发布；
#   - 严禁把旧版本的平台条目照抄进新版本 manifest（旧包 + 新版本号 → 该平台无限更新循环）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

RELEASE_MODE=0
if [ "${1:-}" = "--release" ]; then
  RELEASE_MODE=1
elif [ -n "${1:-}" ]; then
  echo "未知参数: $1" >&2
  echo "用法: ./build.sh [--release]" >&2
  exit 1
fi

# 清除 GOROOT 让 go 自动检测（环境变量中带引号的 GOROOT 会导致 go 找不到目录）
unset GOROOT

VERSION=$(grep 'const Version' service/monitor_service.go | sed 's/.*"\(.*\)".*/\1/')
MINISIGN_KEY="cc-console.sec"
MINISIGN_KEY_LOCAL="cc-console.local.sec"

echo "=== 1/7 前端构建 ==="
cd frontend && npm run build && cd ..

echo ""
echo "=== 2/7 嵌入 Windows 图标资源 ==="
# rsrc 用于将 ICO 嵌入 Windows PE 资源（桌面/任务栏图标）
# 输出文件名带 _windows 后缀，使 Go 仅在 GOOS=windows 链接该 .syso；
# 否则无后缀的 rsrc.syso（COFF）会被 darwin/linux 链接，导致 ld 报 "unknown file type"。
RSRC="$(go env GOPATH | tr -d '"')/bin/rsrc"
"$RSRC" -ico icon.ico -o rsrc_windows.syso

echo ""
echo "=== 3/7 Go 编译便携版 ==="
go build -ldflags="-H windowsgui -s -w" -o cc-console.exe .

echo ""
echo "=== 4/7 编译 statusline 桥接 helper ==="
go build -ldflags="-s -w" -o cc-console-sl.exe ./cmd/slhook
cp cmd/slhook/bridge.mjs bridge.mjs

echo ""
echo "=== 5/7 生成 Inno Setup 安装包 ==="
echo "Version: $VERSION"
# 自动发现 ISCC：优先系统级安装，回退到用户级安装
ISCC_EXE=""
for cand in "/c/Program Files (x86)/Inno Setup 6/ISCC.exe" \
            "$(cygpath -u "${LOCALAPPDATA:-}" 2>/dev/null)/Programs/Inno Setup 6/ISCC.exe"; do
  if [ -f "$cand" ]; then ISCC_EXE="$cand"; break; fi
done
if [ -z "$ISCC_EXE" ]; then
  echo "未找到 ISCC.exe（Inno Setup 6），请先安装" >&2
  exit 1
fi
echo "ISCC: $ISCC_EXE"
powershell -Command "& '$(cygpath -w "$ISCC_EXE")' /DMyAppVersion=$VERSION setup.iss"

echo ""
echo "=== 6/7 处理更新发布元数据 ==="
# release notes：取上一个版本 tag 到 HEAD 的提交标题（多 commit 按行展开），
# 这是关于页「检查更新」要展示的具体更新内容；仓库尚无历史 tag 时退回 HEAD 单条，
# 仍取不到时退回旧占位串。通过 jq --arg 传入，避免特殊字符破坏 JSON。
PREV_TAG="$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || true)"
if [ -n "$PREV_TAG" ]; then
  RELEASE_NOTES="$(git log "$PREV_TAG..HEAD" --pretty=format:'%s' --no-merges)"
else
  RELEASE_NOTES="$(git log -1 --pretty=format:'%s')"
fi
RELEASE_NOTES="${RELEASE_NOTES:-Release v$VERSION}"

MANIFEST_URL="https://github.com/pie-tk/cc-console/releases/latest/download/latest.json"

# fetch_manifest 拉取线上 latest.json：直连失败时回退 git 配置的 http 代理
# （本机网络环境 GitHub 需走代理；curl 不会自动继承 git 的代理配置）。
# 拉不到返回空串，由调用方按「无法获取线上 manifest」处理。
fetch_manifest() {
  local out p
  out="$(curl -fsSL --max-time 15 "$MANIFEST_URL" 2>/dev/null || true)"
  if [ -z "$out" ]; then
    p="$(git config --get http.proxy 2>/dev/null || true)"
    if [ -n "$p" ]; then
      out="$(curl -fsSL --max-time 20 --proxy "$p" "$MANIFEST_URL" 2>/dev/null || true)"
    fi
  fi
  printf '%s' "$out"
}

# write_manifest 生成 latest.json：本平台条目 + （发布模式）线上同版本另一平台条目。
# 入参 $1 = 本平台 platforms 对象（JSON 文本，形如 {"windows-x86_64": {...}}）。
# 发布模式守卫：
#   - 线上同版本且已有本平台条目 → 中止（同版本产物永不覆盖，请升版本号）；
#   - 线上同版本缺本平台 → 补齐模式（合并线上另一平台条目，不升版本）；
#   - 线上已有更新版本 → 中止（先拉代码协调版本号）；
#   - 网络不可达 → 按全新 manifest 生成（仅本平台），发布前人工确认线上状态。
# 严禁把旧版本的平台条目照抄进新版本 manifest（旧包 + 新版本号 → 该平台无限更新循环）。
write_manifest() {
  LOCAL_PLATFORMS="$1"
  EXTRA_PLATFORMS="{}"

  if [ "$RELEASE_MODE" -eq 1 ]; then
    REMOTE_JSON="$(fetch_manifest)"
    if [ -n "$REMOTE_JSON" ] && REMOTE_OK="$(jq -r '.version // empty' <<<"$REMOTE_JSON" 2>/dev/null)" && [ -n "${REMOTE_OK:-}" ]; then
      REMOTE_VER="$REMOTE_OK"
      echo "线上 manifest 版本：v$REMOTE_VER（本地 v$VERSION）"
      if [ "$REMOTE_VER" = "$VERSION" ]; then
        if [ "$(jq -r --arg k windows-x86_64 '.platforms[$k] != null' <<<"$REMOTE_JSON")" = "true" ]; then
          echo "✗ 线上 v$VERSION 已含 windows-x86_64 条目：同版本同平台产物永不覆盖，请升版本号（macOS 端同步升）" >&2
          exit 1
        fi
        EXTRA_PLATFORMS="$(jq -r '.platforms // {} | del(.["windows-x86_64"])' <<<"$REMOTE_JSON")"
        echo "补齐模式：合并线上同版本平台条目 $(jq -r 'keys | join(", ")' <<<"$EXTRA_PLATFORMS")"
      else
        MAX_VER="$(printf '%s\n%s\n' "$REMOTE_VER" "$VERSION" | sort -V | tail -1)"
        if [ "$MAX_VER" = "$REMOTE_VER" ]; then
          echo "✗ 线上已有更新版本 v$REMOTE_VER（本地 v$VERSION）：请先拉代码协调版本号" >&2
          exit 1
        fi
      fi
    else
      echo "⚠️  无法获取线上 manifest（网络不可达或尚无 release）：按全新 manifest 生成"
    fi
  fi

  # manifest 内嵌两行主签名（untrusted comment + 文件签名），兼容已发布客户端；
  # 完整四行 .minisig 仍作为独立 release asset 上传，供外部验证工具使用。
  # tr -d '\r' 去掉 Windows CRLF 行尾，保持与 minisign 标准（纯 LF）一致。
  SIG="$(head -2 cc-console-setup.exe.minisig | tr -d '\r')"
  jq -n \
    --arg ver "$VERSION" \
    --arg sig "$SIG" \
    --arg notes "$RELEASE_NOTES" \
    --arg date "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --argjson extra "$EXTRA_PLATFORMS" \
    --argjson local "$LOCAL_PLATFORMS" \
    '{ version: $ver,
       notes: $notes,
       pub_date: $date,
       platforms: ($extra + $local)
     }' > latest.json
  echo "已生成 latest.json（平台：$(jq -r '.platforms | keys | join(", ")' latest.json)）"
}

if ! command -v minisign >/dev/null 2>&1; then
  if [ "$RELEASE_MODE" -eq 1 ]; then
    echo "缺少 minisign：发布构建必须先安装（示例: scoop install minisign）" >&2
    exit 1
  fi
  echo "⚠️  未安装 minisign，跳过签名与 manifest（本地构建允许；发布请用 ./build.sh --release）"
elif ! command -v jq >/dev/null 2>&1; then
  if [ "$RELEASE_MODE" -eq 1 ]; then
    echo "缺少 jq：发布构建必须先安装（示例: scoop install jq）" >&2
    exit 1
  fi
  echo "⚠️  未安装 jq，跳过签名与 manifest（本地构建允许；发布请用 ./build.sh --release）"
elif [ -f "$MINISIGN_KEY_LOCAL" ] || { [ -f "$MINISIGN_KEY" ] && [ "$RELEASE_MODE" -eq 1 ]; }; then
  if [ -f "$MINISIGN_KEY_LOCAL" ]; then
    echo "使用免密本地签名副本 $MINISIGN_KEY_LOCAL"
    minisign -S -s "$MINISIGN_KEY_LOCAL" -m cc-console-setup.exe -x cc-console-setup.exe.minisig -t "cc-console v$VERSION"
  else
    echo "使用加密私钥 $MINISIGN_KEY 签名（将提示输入口令）"
    minisign -S -s "$MINISIGN_KEY" -m cc-console-setup.exe -x cc-console-setup.exe.minisig -t "cc-console v$VERSION"
  fi
  echo "已生成 cc-console-setup.exe.minisig"

  echo ""
  echo "=== 7/7 生成 latest.json manifest ==="
  # 用 shell 而非 python：Windows 上 python 常被 Store「应用执行别名」拦截，
  # 导致 build.sh --release 在签名后静默卡死、latest.json 不生成。
  LOCAL_PLATFORMS="$(jq -n \
    --arg ver "$VERSION" \
    --arg sig "$(head -2 cc-console-setup.exe.minisig | tr -d '\r')" \
    '{ "windows-x86_64": {
         signature: $sig,
         url: ("https://github.com/pie-tk/cc-console/releases/download/v" + $ver + "/cc-console-setup.exe")
       } }')"
  write_manifest "$LOCAL_PLATFORMS"
else
  echo "⚠️  检测到仅有加密私钥 $MINISIGN_KEY，本地构建跳过签名与 manifest"
  echo "    若要免交互发布，请先创建免密副本 $MINISIGN_KEY_LOCAL"
fi

echo ""
echo "=== 完成 ==="
ls -lh cc-console.exe cc-console-sl.exe cc-console-setup.exe
if [ -f cc-console-setup.exe.minisig ] && [ -f latest.json ]; then
  echo ""
  echo "发布产物：cc-console-setup.exe / cc-console-setup.exe.minisig / latest.json"
else
  echo ""
  echo "本次未生成发布元数据；正式发布前请执行 ./build.sh --release"
fi
