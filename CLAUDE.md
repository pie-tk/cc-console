# claude-code-monitor

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
| `internal/theme/palette.go` | Notion 风格调色板（light/dark）+ CSS 变量映射 |
| `internal/theme/detect_windows.go` | Windows 注册表读取暗色模式 |
| `internal/theme/anim.go` | 脉冲因子（Go 端状态） |
| `frontend/index.html` | 主页面结构 |
| `frontend/public/style.css` | Notion 风格 CSS（light/dark 变量 + CSS 动画） |
| `frontend/src/main.js` | 刷新循环 + 卡片渲染 + 操作处理 |
| `frontend/bindings/` | Wails 自动生成的 JS 绑定 |
| `icon.ico` | 应用图标（`//go:embed` 嵌入） |
| `Taskfile.yml` | 构建任务 |

## 构建

**任何修改后都必须同时构建便携版 exe 和安装包**，确保两个产物都是最新的。

```bash
# 一键构建（推荐）— 便携版 exe + 安装包
./build.sh

# 或分步手动执行：
# 1. cd frontend && npm run build && cd ..
# 2. go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .
# 3. powershell -Command "& 'C:\Users\PIE TK\AppData\Local\Programs\Inno Setup 6\ISCC.exe' /DMyAppVersion=$(grep 'const Version' service/monitor_service.go | sed 's/.*\"\(.*\)\".*/\1/') setup.iss"

# 或使用 Taskfile
task build
task setup

# CLI 模式（无 WebView，纯终端）
go run . --list
```

## 发布

每次发布只上传安装包到 GitHub Release：

```bash
gh release create v<version> ./claude-monitor-setup.exe --title "v<version>"
```

Release 资产：`claude-monitor-setup.exe` — Inno Setup 安装包（不传便携版，容易被杀软误报）

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
- 模型上限覆盖: `~/.claude-monitor.json` 的 `modelLimits` 字段
- Claude Code 设置: `~/.claude/settings.json` 的模型环境变量（解析 `[xxx]` 标注）

## 关键设计决策

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
