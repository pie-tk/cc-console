---
name: build-install-test
description: cc-console 改完代码后走 ./build.sh：只出安装包并自动安装、启动测试（便携版 exe 已废弃）
metadata:
  type: feedback
---

任何代码修改后执行 `./build.sh`：编译（中间产物进 `bin/`）→ 生成 `cc-console-setup.exe`（**唯一交付产物**）→ 静默安装 → 启动测试。**不要**再单独构建或分发便携版 `cc-console.exe`。

**Why:** 之前同时维护便携版 exe + 安装包，根目录一堆产物很乱，而且容易测到未打包的便携 exe，导致 bug fix 没进安装包。现在统一以安装版为准，构建完直接装上打开测试。

**How to apply:**
- 首选 `./build.sh`（出包 + 自动安装 + 启动）；只想出包用 `./build.sh --no-install`
- 手动 `go build -o …cc-console….exe` 会触发 `.claude/settings.json` 的 PostToolUse hook 自动补跑 `./build.sh`
- 发布走 `./build.sh --release`（生成 .minisig + latest.json，默认不安装），见 [[cc-console-release-flow]]
- `bin/` 下的 cc-console.exe / cc-console-sl.exe / bridge.mjs 只是打包中间件（gitignore），测试一律针对 `%LOCALAPPDATA%\cc-console\` 的安装版
