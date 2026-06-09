# Claude Code 实例监控 · claude-code-monitor

一个 Windows 桌面小工具，**实时监控本机正在运行的 Claude Code 实例**：数量、忙/闲状态、所用模型、上下文用量与百分比、本轮输出 tokens、对话主题、启动时长、工作目录。系统托盘常驻，每秒自动刷新。

纯 Go（[Walk](https://github.com/lxn/walk) GUI）实现，无 cgo，单文件可执行。

> 适用场景：同时开了多个 Claude Code（不同项目 / 不同模型），想一眼看清谁在跑、谁闲着、上下文快满了没有。

---

## 功能特性

- **实例总览**：在线数、忙碌 / 空闲计数、合计 Context tokens。
- **每实例详情**（表格 8 列）：

  | 列 | 说明 |
  |---|---|
  | PID | 进程号 |
  | 状态 | ● 忙碌 / ○ 空闲 / ? 未知 |
  | 模型 | 最后一次回复使用的模型 |
  | Context | 上下文用量百分比 + 已用/上限（如 `74%  148k/200k`） |
  | 本轮 | 最近一轮的输出 tokens |
  | 对话主题 | AI 生成的会话标题；新会话显示「（新会话·无消息）」 |
  | 启动时长 | 进程已运行时间 |
  | 项目 / 工作目录 | 实例的工作目录 |

- **系统托盘常驻**：关闭窗口 → 最小化到托盘继续运行；左键托盘图标显示窗口，右键菜单「显示窗口 / 退出」。
- **每秒自动刷新**，带时间戳。
- **命令行模式**：`claude-monitor.exe --list` 打印一次表格后退出，方便脚本/日志。

### 它如何识别「真正的 Claude Code」

本机 `claude.exe` 被**两个产品**共用：

- **Claude Code CLI**（本工具监控对象）：路径含 `@anthropic-ai\claude-code`，会写 `~/.claude/sessions/<pid>.json`。
- **Claude 桌面版**（Electron）：`C:\Program Files\Claude Desktop\claude.exe`，一个主进程 + 一堆 `--type=gpu/renderer/utility/crashpad` 子进程，**不写 session 文件**。

判定标准：**一个 `claude.exe` 只有在「存在对应的 session 文件，且进程启动时间与会话 `startedAt` 一致」时才计为实例**（容差 15s，防 PID 复用）。这样能同时：

1. 排除 Claude 桌面版（无 session 文件）；
2. 排除 Claude Code **启动瞬间派生的短命辅助子进程**（版本/更新探测等，与主进程同路径但不写 session）——否则界面会「启动时冒出好几行、1~2 秒后消失」。

### 数据来源

| 信息 | 来源 |
|---|---|
| 状态 / cwd / sessionId / 启动时间 | `~/.claude/sessions/<pid>.json` |
| 模型 / tokens / 主题 | `~/.claude/projects/<编码后的cwd>/<sessionId>.jsonl`（cwd 把 `:` `/` `\` 全替换成 `-`） |
| 进程枚举 | `CreateToolhelp32Snapshot` + `QueryFullProcessImageName` + `GetProcessTimes` |

读取大 JSONL 时按 mtime 缓存，避免每秒重读。

---

## 环境要求

- **Windows**（纯 Win32 GUI，不支持 macOS / Linux）
- **Go 1.26+**（仅构建时需要）
- Claude Code CLI（已安装并产生 `~/.claude/` 数据）

已在 **Windows 10 + Claude Code 2.1.168（经 GLM 后端）** 验证。

## 构建

```bash
git clone <repo-url> claude-code-monitor
cd claude-code-monitor
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .
```

或直接双击 `build.bat`。

> - `-H windowsgui`：GUI 子系统，启动不弹黑色控制台窗口。
> - `-s -w`：剥离调试信息，缩小体积。
> - 仓库已带 `resource_windows_amd64.syso`（manifest + 图标），`go build` 会自动链接，开箱即用。
> - 如需重新生成 `.syso`（改了 `app.manifest` 或 `icon.ico` 后）：
>   ```bash
>   go install github.com/akavel/rsrc@latest
>   rsrc -arch amd64 -manifest app.manifest -ico icon.ico
>   ```
> - 国内网络拉依赖慢可设：`go env -w GOPROXY=https://goproxy.cn,direct`

## 使用

```bash
# GUI 模式（托盘常驻）
claude-monitor.exe

# 命令行模式：打印一次后退出
claude-monitor.exe --list
```

`--list` 输出示例：

```
在线 Claude Code 实例: 3   (● 忙碌 1   ○ 空闲 2)

PID      状态        模型            Context           本轮   对话主题                     项目 (工作目录)
43920    ● 忙碌      glm-5.1        26% 52k/200k      105    Build app to monitor Clau…   E:\test
22912    ○ 空闲      glm-5.1        13% 27k/200k      55     Update Claude Code version   C:\Users\Administrator
41832    ○ 空闲      （新）          （新）            （新） （新会话·无消息）             ...\BelaHome\belahome
```

## 配置：自定义模型上下文上限

Claude Code 的数据里 `contextWindow` 为 0，不是可靠上限。本工具内置一张「模型 → 上限」表（Claude 系列 200k、glm-4.x 128k 等）来估算百分比。

如果你的模型上限与默认不同，可在 `~/.claude-monitor.json` 覆盖：

```json
{ "modelLimits": { "glm-5.1": 256000 } }
```

## 已知限制

- 仅 Windows。
- 百分比是**估算**（依赖内置/配置的上限表），非权威值。
- 「本轮」= 最近一轮输出 tokens；多轮累计需另行计算。

## License

[MIT](LICENSE)
