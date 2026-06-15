# Claude Code 监控 · claude-code-monitor

一个 Windows 桌面小工具，**实时监控本机正在运行的所有 Claude Code 实例**：谁在跑、谁闲着、用的什么模型、上下文快满了没有——并支持直接对实例下达指令（清空 / 发消息 / 回溯 / 置前窗口）、启动新实例、查看对话历史。系统托盘常驻，每秒自动刷新，跟随系统明暗主题。

基于 [Wails v3](https://wails.io/)（WebView2）+ Vanilla HTML/CSS/JS，Go 1.26。

![claude-code-monitor 截图](claude%20monitor.png)

> 适用场景：同时开了多个 Claude Code（不同项目 / 不同模型），想一眼看清全局；或想远程/批量对某个实例发指令，而不必逐个切到终端窗口。

📖 **完整功能说明见 [docs/功能介绍.md](docs/功能介绍.md)。**

---

## 功能特性

- **实时总览**：在线数、忙碌 / 空闲计数、合计 Context tokens、残留（已退出）会话数，每秒刷新带实时时钟。
- **实例卡片**：每个 Claude Code 一张卡片，展示 状态（忙碌脉冲呼吸动画）/ 工作目录 / PID / 模型 / 运行时长 / 对话主题 / **上下文用量进度条**（带百分比与 50%·80% 颜色分级）/ 本轮输出 tokens / **累计 tokens**（input·output·cache 明细）。
- **Statusline 桥接**：针对 Claude Code 2.1.177+ 活跃会话不落盘 JSONL 的问题，通过 statusline 通道注入 `claude-monitor-sl.exe` 实时获取每个实例的 model / tokens / session 等数据，写入 `~/.claude-monitor/live/<pid>.json`，按 PID 精确还原。
- **实例操作**：卡片右侧四键，通过 Win32 控制台输入注入直接驱动目标实例——
  - **清空** `/clear` · **对话** 发自定义文本（Shift+Enter 发送或回车发送可配置）· **回溯** `ESC×2` · **窗口** 置前终端。
- **启动新实例**：从监控器直接启动新的 Claude Code 终端，支持选择目录或从最近目录快捷启动，终端窗口模式可配置（显示 / 隐藏）。
- **对话历史**：点击实例卡片可查看完整对话消息（含工具调用 / 结果），支持折叠展开。
- **排序**：按 最后活动 / 建立时间 / Context 用量 排序，支持升降序；「最后活动」维度会先按「忙碌 → 空闲 → 残留」分组，最忙的永远在最上面。
- **版本更新检查**：支持检查 GitHub 最新 Release、下载并应用更新安装包。
- **设置面板**：关闭按钮直接退出、开机启动、启动终端窗口模式、消息发送键配置、关于 / GitHub。
- **系统托盘 + 单实例**：关闭默认隐藏到托盘（可改为直接退出），托盘 Tooltip 实时显示概要；第二次启动只激活已有窗口。
- **主题与动画**：跟随系统明暗模式切换 Notion 风格调色板，脉冲呼吸 / 消息淡出均为纯 CSS 动画。
- **命令行模式**：`claude-monitor.exe --list` 打印一次表格后退出，方便脚本 / 日志。

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
| 模型 / tokens / 主题（落盘会话） | `~/.claude/projects/<编码后的cwd>/<sessionId>.jsonl`（cwd 把 `:` `/` `\` 全替换成 `-`） |
| 模型 / tokens / 主题（活跃会话） | statusline 桥接 → `~/.claude-monitor/live/<pid>.json`（Claude Code 2.1.177+） |
| 模型上下文上限 | 内置「模型 → 上限」表（60+ 条目）+ `~/.claude-monitor.json` 的 `modelLimits` 覆盖 |
| Claude Code 模型设置 | `~/.claude/settings.json`（解析 `[xxx]` 标注的环境变量） |
| 进程枚举 | `CreateToolhelp32Snapshot` + `QueryFullProcessImageName` + `GetProcessTimes`（按 PID 去重，防快照重复） |

读取大 JSONL 时按 mtime 缓存，避免每秒重读。

---

## 环境要求

- **Windows 10/11**（WebView2 运行时，系统一般自带）。macOS / Linux 的平台特定逻辑（进程探测 / 输入注入 / 主题检测）目前为存根。
- **Go 1.26+**、**Node.js**（仅构建时需要）。
- Claude Code CLI（已安装并产生 `~/.claude/` 数据）。

已在 **Windows 10 + Claude Code 2.1.168（经 GLM 后端）** 验证。

## 安装

### 方式一：安装包（推荐）

从 [GitHub Releases](https://github.com/pie-tk/claude-code-monitor/releases) 下载 `claude-monitor-setup.exe`，双击安装。

安装包由 Inno Setup 构建，支持中文（简体/繁体）界面，自动创建桌面快捷方式。

### 方式二：便携版

从 [Releases](https://github.com/pie-tk/claude-code-monitor/releases) 下载 `claude-monitor.exe`，直接运行，无需安装。

## 构建

```bash
git clone git@github.com:pie-tk/claude-code-monitor.git
cd claude-code-monitor

# 一键构建（便携版 exe + 安装包）
./build.sh

# 或分步执行：
# 1. 构建前端
cd frontend && npm install && npm run build && cd ..

# 2. 构建 Go（嵌入 frontend/dist）
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .

# 3. 构建安装包（需要 Inno Setup 6）
powershell -Command "& 'C:\Program Files\Inno Setup 6\ISCC.exe' /DMyAppVersion=1.3.1 setup.iss"
```

或使用 [Taskfile](Taskfile.yml)：`task build`（构建）、`task dev`（热重载开发）。

> - `-H windowsgui`：GUI 子系统，启动不弹黑色控制台窗口。
> - `-s -w`：剥离调试信息，缩小体积。
> - 前端产物通过 `//go:embed all:frontend/dist` 嵌入二进制，最终仍是单文件可执行。
> - 国内网络拉依赖慢可设：`go env -w GOPROXY=https://goproxy.cn,direct`
> - 修改 Go 服务方法后需重新生成前端绑定：
>   `go run github.com/wailsapp/wails/v3/cmd/wails3@latest generate bindings`

## 使用

```bash
# GUI 模式（托盘常驻，每 1 秒刷新）
claude-monitor.exe

# 命令行模式：打印一次后退出
claude-monitor.exe --list

# 帮助
claude-monitor.exe -h
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

- GUI 与实例操作（输入注入）仅 Windows 可用；macOS / Linux 为存根。
- 上下文百分比为**估算**（依赖内置 / 配置的上限表），非权威值。
- Statusline 桥接需要 Claude Code 2.1.177+，旧版本不影响基本功能（从 JSONL 读取）。

## License

[MIT](LICENSE)
