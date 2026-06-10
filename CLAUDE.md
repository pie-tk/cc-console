# claude-code-monitor

Windows 桌面 GUI 工具，实时监控本机运行的 Claude Code 实例（数量、状态、模型、上下文用量等）。系统托盘常驻，每秒刷新。

## 技术栈

- Go 1.26，无 cgo
- GUI: [lxn/walk](https://github.com/lxn/walk) + lxn/win（Win32 API 声明）
- 构建: `build.bat`，产物输出到项目根目录 `claude-monitor.exe`

## 项目结构

| 文件 | 职责 |
|------|------|
| `main.go` | 入口 + GUI 窗口（walk MainWindow / ScrollView 卡片 / NotifyIcon）+ CLI `--list` |
| `detector.go` | 实例检测核心：Detect()、进程枚举、isClaudeCode 判定 |
| `session.go` | SessionInfo 结构体 + `~/.claude/sessions/*.json` 加载 |
| `conversation.go` | JSONL 对话解析（model / tokens / topic）+ mtime 缓存 + 缓存清理 |
| `models.go` | 模型上下文上限表（60+ 条目前缀匹配）+ 配置加载（`~/.claude-monitor.json` + `settings.json`） |
| `display.go` | 展示辅助：statusText / formatTokens / unicodeBar / humanDuration 等 |
| `cards.go` | Notion 风格实例卡片（创建 / 更新 / 销毁） |
| `theme.go` | 暗色/亮色主题调色板 + DWM 暗色标题栏 + blendColor |
| `anim.go` | 动效系统：busy 脉冲呼吸 + 底部消息 TTL 淡出 + 按需动画驱动 |
| `actions.go` | 操作按钮（清空 / 对话 / 回溯 / 窗口置前）+ 输入对话框 |
| `inject.go` | 控制台输入注入：AttachConsole → WriteConsoleInput → FreeConsole |
| `win32.go` | 集中声明所有 Win32 DLL / LazyProc（kernel32 / user32 / dwmapi） |
| `build.bat` | 构建脚本 |
| `app.manifest` | Windows manifest（Common Controls v6 + DPI 感知） |
| `icon.ico` | 应用图标（通过 `//go:embed` 嵌入） |

## 构建

```bash
# 需要 Go 1.26+
# -H windowsgui: GUI 子系统，不弹控制台窗口
# -s -w: 剥离调试信息
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .
```

若改了 `app.manifest` 或 `icon.ico`，需重新生成 `.syso` 资源文件：
```bash
go install github.com/akavel/rsrc@latest
rsrc -manifest app.manifest -ico icon.ico -o rsrc.syso
```

## 数据来源

- 实例列表: `~/.claude/sessions/<pid>.json`
- 对话详情: `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl`
- 模型上限覆盖: `~/.claude-monitor.json` 的 `modelLimits` 字段
- Claude Code 设置: `~/.claude/settings.json` 的模型环境变量（解析 `[xxx]` 标注）

## 关键设计决策

- **实例判定**: `claude.exe` 进程必须有对应 session 文件 + 启动时间匹配（容差 15s），以此排除 Claude 桌面版和 Claude Code 的短命辅助子进程
- **输入注入**: 利用 `AttachConsole` + `WriteConsoleInput` 向目标实例的控制台投递按键，等价于用户在终端亲手输入
- **JSONL 缓存**: 按 mtime 缓存对话文件解析结果，避免每秒重读大文件；每次 Detect() 时清理已失效缓存条目
- **关窗行为**: 点关闭按钮 → 隐藏到托盘（`*canceled = true`），只有托盘菜单「退出」才真正退出
- **动画按需**: 80ms 动画 ticker 仅在存在 busy 实例或未过期底部消息时执行 GUI 更新，空闲时零开销

## 代码风格

- 中文注释，英文标识符
- 内联短逻辑，不过度抽象
- Windows API 调用走 `syscall.NewLazyDLL`（统一声明在 `win32.go`）+ `golang.org/x/sys/windows`
- walk 声明式布局（`declarative` 包的 dot import）
