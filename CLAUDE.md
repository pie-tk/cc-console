# claude-code-monitor

Windows 桌面 GUI 工具，实时监控本机运行的 Claude Code 实例（数量、状态、模型、上下文用量等）。系统托盘常驻，每秒刷新。

## 技术栈

- Go 1.26，无 cgo
- GUI: [lxn/walk](https://github.com/lxn/walk) + lxn/win（Win32 API 声明）
- 构建: `build.bat`，产物输出到项目根目录 `claude-monitor.exe`

## 项目结构

| 文件 | 职责 |
|------|------|
| `main.go` | 入口 + GUI 窗口（walk MainWindow / TableView / NotifyIcon）+ InstanceModel |
| `detector.go` | 核心：实例检测、session 解析、进程枚举、对话 JSONL 解析、模型上下文上限表、展示辅助函数 |
| `actions.go` | 右键菜单（清空/输入对话/回溯）+ 输入对话框 + 子类化 ListView |
| `inject.go` | 控制台输入注入：AttachConsole → WriteConsoleInput → FreeConsole |
| `build.bat` | 构建脚本 |
| `app.manifest` | Windows manifest（Common Controls v6 + DPI 感知） |
| `icon.ico` | 应用图标 |

## 构建

```bash
# 需要 Go 1.26+
# -H windowsgui: GUI 子系统，不弹控制台窗口
# -s -w: 剥离调试信息
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .
```

若改了 `app.manifest` 或 `icon.ico`，需重新生成 `.syso` 资源文件：
```bash
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
- **JSONL 缓存**: 按 mtime 缓存对话文件解析结果，避免每秒重读大文件
- **关窗行为**: 点关闭按钮 → 隐藏到托盘（`*canceled = true`），只有托盘菜单「退出」才真正退出

## 代码风格

- 中文注释，英文标识符
- 内联短逻辑，不过度抽象
- Windows API 调用走 `syscall.NewLazyDLL` + `golang.org/x/sys/windows`
- walk 声明式布局（`declarative` 包的 dot import）
