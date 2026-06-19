---
name: always-build-both
description: 编译 cc-console 时需同时产出便携版 exe 和安装包
metadata:
  type: feedback
---

任何代码修改后，必须同时构建 `cc-console.exe`（便携版）和 `cc-console-setup.exe`（Inno Setup 安装包），确保两个产物都是最新的。

**Why:** 之前只编译了便携版漏掉安装包，用户点击「窗口」按钮的 bug fix 需要在安装包中体现。Taskfile 的 `task build` 已内置 `task: setup` 子任务，裸 `go build` 会跳过安装包。

**How to apply:**
- 首选 `./build.sh` 一键构建
- 或 `task build`（如果 task 命令可用）
- 手动构建时必须执行：1) `go build` → 2) ISCC 打包
- `.claude/settings.json` 已配置 PostToolUse hook：检测到 `go build ... cc-console.exe` 后自动触发 ISCC 打包
