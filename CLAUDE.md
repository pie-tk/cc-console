# cc-console

桌面 GUI 工具，实时监控本机运行的 Claude Code 实例（数量、状态、模型、上下文用量等）。系统托盘常驻，每秒刷新。

## 技术栈

- Go 1.26
- GUI: [Wails v3](https://wails.io/) (WebView2) + Vanilla HTML/CSS/JS
- 前端构建: Vite
- 跨平台架构: build tag 隔离平台特定代码

## 项目结构

| 文件 | 职责 |
|------|------|
| `main.go` | 入口：CLI `--list` 或 Wails GUI |
| `app.go` | Wails 应用：窗口、系统托盘、单实例、关闭→隐藏 |
| `service/monitor_service.go` | Wails 服务：暴露给前端的所有 Go 方法 |
| `internal/monitor/types.go` | Instance + StatsInfo 结构体 |
| `internal/monitor/detector.go` | Detect() + 平台无关逻辑 |
| `internal/monitor/detector_windows.go` | Windows 进程枚举 (Toolhelp32) |
| `internal/monitor/detector_darwin.go` | macOS 存根 |
| `internal/monitor/session.go` | SessionInfo + `~/.claude/sessions/*.json` 加载 |
| `internal/monitor/conversation.go` | JSONL 对话解析 + mtime 缓存 |
| `internal/monitor/models.go` | 模型上下文上限表（60+ 条目）+ 配置加载 |
| `internal/monitor/display.go` | 格式化工具函数 |
| `internal/monitor/inject.go` | ConsoleInput 接口定义 |
| `internal/monitor/inject_windows.go` | Win32 控制台输入注入实现 |
| `internal/monitor/inject_darwin.go` | macOS 存根 |
| `internal/monitor/folder.go` | OpenInFolder 跨平台（macOS open / Linux xdg-open） |
| `internal/monitor/folder_windows.go` | Windows 资源管理器打开目录 |
| `internal/monitor/clipboard_windows.go` | CF_HDROP 剪贴板文件路径读取（粘贴附件） |
| `internal/monitor/clipboard_darwin.go` | macOS 剪贴板存根（粘贴附件暂不可用，拖放不受影响） |
| `internal/monitor/update.go` | latest.json manifest 解析 + 平台 key（windows-x86_64 / darwin-arm64 / darwin-amd64） |
| `internal/monitor/update_windows.go` | 下载安装包 + minisign 校验 + 静默自替换 |
| `internal/monitor/update_darwin.go` | 下载 dmg + 打开挂载（macOS 手动拖拽安装） |
| `internal/theme/palette.go` | Notion 风格调色板（light/dark）+ CSS 变量映射 |
| `internal/theme/detect_windows.go` | Windows 注册表读取暗色模式 |
| `internal/theme/anim.go` | 脉冲因子（Go 端状态） |
| `frontend/index.html` | 主页面结构 |
| `frontend/public/style.css` | Notion 风格 CSS（light/dark 变量 + CSS 动画） |
| `frontend/src/main.js` | 刷新循环 + 卡片渲染 + 操作处理 |
| `frontend/bindings/` | Wails 自动生成的 JS 绑定 |
| `icon.ico` | 应用图标（`//go:embed` 嵌入） |
| `bin/` | 编译中间产物（exe / bridge.mjs，gitignore，勿提交） |
| `Taskfile.yml` | 构建任务 |
| `build-mac.sh` | macOS universal DMG 打包（只能在 Mac 上运行） |
| `build/darwin/Info.plist` | .app bundle 模板（`{{VERSION}}` 占位） |

## 构建

**任何修改后都必须执行 `./build.sh`**（Windows 端）：产出安装包 `cc-console-setup.exe`（**唯一交付产物**）后**自动静默安装并启动**，直接对安装版测试。`bin/` 下的 exe / bridge.mjs 只是打包中间件（gitignore），便携版 exe 不再交付。若改动涉及更新逻辑或发布链路，还要一并验证发布元数据流程。macOS 产物在 Mac 上另行构建（见下）。

```bash
# 一键本地构建（推荐）— 生成安装包 → 静默安装 → 启动测试
./build.sh
./build.sh --no-install   # 只出安装包，不安装不启动

# 发布前构建（强制生成 .minisig 与 latest.json，默认不安装）
./build.sh --release
# 若本机已准备 `cc-console.local.sec`（免密、仅本地保存的签名副本），脚本会优先使用它，
# 这样 Claude 可直接完成发布而无需交互输入口令。

# 或使用 Taskfile
task build
task release-build

# macOS 构建（必须在 Mac 上运行，Windows 不交叉编译 mac）
./build-mac.sh            # 产出 bin/cc-console-setup-macos-universal.dmg（universal arm64+amd64）
./build-mac.sh --release  # 额外生成 .minisig 与 latest.json（自动合并双平台条目）

# CLI 模式（无 WebView，纯终端）
go run . --list
```

分步手动执行（仅限你明确知道自己在做什么；正常情况一律走 `./build.sh`）：
1. `cd frontend && npm run build && cd ..`
2. `$(go env GOPATH)/bin/rsrc -ico icon.ico -o rsrc_windows.syso`（仅链接期需要，编译后删除）
3. `mkdir -p bin && go build -ldflags="-H windowsgui -s -w" -o bin/cc-console.exe .`
4. `go build -ldflags="-s -w" -o bin/cc-console-sl.exe ./cmd/slhook && cp cmd/slhook/bridge.mjs bin/bridge.mjs`
5. `powershell -Command "& 'C:\Program Files (x86)\Inno Setup 6\ISCC.exe' /DMyAppVersion=<version> setup.iss"`
6. （仅发布）`minisign -S -s cc-console.sec -m cc-console-setup.exe -x cc-console-setup.exe.minisig -t "cc-console v<version>"`
7. （仅发布）latest.json 按 build.sh 的 `write_manifest` 逻辑生成（含双平台合并，勿手拼）

## 发布

**双平台（Windows + macOS）同步发布**，规则对齐 toolbox 项目（E:\tools\toolbox）：

- **改版本号前必查远程**（`./build.sh --release` / `./build-mac.sh --release` 内置守卫）：
  - 线上同版本**缺本平台**条目（另一端先发、这端未发）→ **不升版本**，补建同版本产物即可；
  - 线上同版本**已有本平台**条目 → 任何改动必须升版本（同版本产物永不覆盖）；
  - 线上已有更新版本 → 先拉代码协调版本号。
- **版本号单点同步**：`service/monitor_service.go` 的 `const Version`，两台机器构建前必须一致。
- **latest.json 只收「同版本产物已就位」的平台条目**，谁后构建谁自动补齐另一平台条目
  （脚本读线上 manifest 合并）。**严禁把旧版本的平台条目照抄进新版本 manifest**——
  旧包 + 新版本号 → 该平台客户端无限更新循环。
- **macOS 必须在 Mac 上构建**（`./build-mac.sh --release`，Windows 不交叉编译 mac）：
  universal（arm64+amd64 lipo）DMG，产出 `bin/cc-console-setup-macos-universal.dmg` + `.minisig` + `latest.json`。
- **macOS 产物必须带 ad-hoc 代码签名**（`codesign --force --deep --sign -`）：macOS Sequoia
  的「本地网络」隐私控制会静默丢弃无签名进程的组播流量。build-mac.sh 已内置，勿去掉。
- **签名私钥** `cc-console.sec` / 免密副本 `cc-console.local.sec`：Windows/Mac 两台机器各存一份，
  两平台共用同一把（minisign 签名与文件名无关）。**丢失即永远无法发更新**。
- **平台命名**：latest.json 的 platforms key（updater 按 `runtime.GOARCH` 查表）用
  `windows-x86_64` / `darwin-arm64` / `darwin-amd64`——写错该平台永远收不到更新；
  面向人的命名（Release 标题/说明/DMG 文件名）用 `macOS`。
- 缺某平台条目时，该端客户端静默无更新（`CheckLatestRelease` 返回无更新），等另一台机器
  构建 push 后自动补齐，**不要**为凑 manifest 照抄旧条目。

每次发布前必须先执行 `./build.sh --release`（Windows）与 `./build-mac.sh --release`（macOS），
确认产物与 `latest.json`（含全部已就位平台条目）都已生成，再上传到 GitHub Release。

**GitHub Release 上传必须使用命令行 `gh` CLI**：先 `git push origin <branch>` 推送提交，再用 `gh release create`/`gh release upload` 上传资产。不要为发布上传调用 `web-access`、CDP 或浏览器页面操作；除非用户明确要求网页登录/网页操作，发布链路一律走 `git` + `gh` 命令。先发的平台创建 release 并传本平台资产；另一平台补发时用 `gh release upload` 追加资产并**更新** `latest.json`（双平台条目版）。

```bash
# 先发（创建 release）
gh release create v<version> ./cc-console-setup.exe ./cc-console-setup.exe.minisig ./latest.json --title "v<version>"

# 补发（另一平台，如 macOS）：追加资产 + 覆盖 latest.json 为合并版
gh release upload v<version> ./cc-console-setup-macos-universal.dmg ./cc-console-setup-macos-universal.dmg.minisig ./latest.json --clobber
```

Release 资产：
- `cc-console-setup.exe` + `.minisig` — Windows Inno Setup 安装包及签名
- `cc-console-setup-macos-universal.dmg` + `.minisig` — macOS universal DMG 及签名
- `latest.json` — 更新检查读取的 manifest（必需，含全部已就位平台条目）

注意：自动更新读取 `releases/latest/download/latest.json`，所以发布时务必使用**正式 release**，不要设为 prerelease。

## 开发

```bash
# 安装前端依赖
cd frontend && npm install

# 生成 JS 绑定（修改 Go 服务方法后执行）
go run github.com/wailsapp/wails/v3/cmd/wails3@latest generate bindings

# 开发模式（热重载）
task dev
```

## 数据来源

- 实例列表: `~/.claude/sessions/<pid>.json`
- 对话详情: `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl`
- 模型上限覆盖: `~/.cc-console.json` 的 `modelLimits` 字段
- Claude Code 设置: `~/.claude/settings.json` 的模型环境变量（解析 `[xxx]` 标注）

## 关键设计决策

- **交付产物**: 唯一交付 `cc-console-setup.exe`；build.sh 构建后自动静默安装并启动，测试一律针对安装版（便携版 exe 已废弃，bin/ 下的 exe 仅作打包中间件）
- **实例判定**: `claude.exe` 进程必须有对应 session 文件 + 启动时间匹配（容差 15s）
- **输入注入**: Win32 `AttachConsole` + `WriteConsoleInput`，通过 `ConsoleInput` 接口隔离平台差异
- **JSONL 缓存**: 按 mtime 缓存对话文件解析结果，避免每秒重读大文件
- **关窗行为**: 点关闭按钮 → 隐藏到托盘（RegisterHook + Cancel），只有托盘菜单「退出」才真正退出
- **单实例**: Wails 内置 `SingleInstanceOptions`，第二个启动自动激活已有窗口
- **主题**: CSS 变量驱动 light/dark 切换，Go 端检测系统主题并推送 CSS 映射
- **动画**: CSS `@keyframes` 实现 busy 脉冲呼吸和 footer 消息淡出，零 Go 端开销
- **跨平台**: build tag 隔离 detector/inject/theme 的平台特定实现，macOS/Linux 暂为存根

## 代码风格

- 中文注释，英文标识符
- 内联短逻辑，不过度抽象
- 平台特定代码走 `//go:build` build tag
- 前端 Vanilla JS，零框架依赖
