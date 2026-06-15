# Claude Code 监控 · claude-code-monitor

Windows 桌面小工具，**实时监控本机所有 Claude Code 实例**，支持远程操控（清空 / 发消息 / 回溯 / 置前窗口）、启动新实例、查看对话历史。系统托盘常驻，每秒刷新，跟随系统明暗主题。

![主界面](main%20page.png)

---

## 主要功能

### 📊 实时监控

顶部面板一眼看清全局——在线数、忙碌/空闲计数、上下文总用量、残留会话数。每个实例一张卡片，展示：

- 🔴/🟢 状态指示灯（忙碌时脉冲呼吸动画，老远就知道谁在干活）
- 工作目录、PID、模型、运行时长、对话主题
- **上下文用量进度条**：百分比 + 已用/上限，<50% 绿色 · 50%~80% 黄色 · ≥80% 红色
- 本轮输出 tokens + 累计 tokens（input / output / cache 明细）

### 🎮 远程操控

卡片右侧四键，无需切到终端窗口，直接驱动目标实例：

| 按钮 | 动作 |
|---|---|
| **清空** | 发送 `/clear`，清空对话上下文 |
| **对话** | 发送自定义文本到实例（Shift+Enter 或回车发送可配置） |
| **回溯** | 发送 `ESC×2`，中断当前操作 |
| **窗口** | 把实例的终端窗口拉到最前 |

### 💬 对话历史

点击实例卡片展开完整对话记录，包含消息、工具调用及结果，支持折叠浏览。

![对话历史界面](message%20page.png)

### 🚀 启动新实例

从监控器直接在新的终端窗口启动 Claude Code——支持手动选择目录，或从最近使用目录一键启动。终端窗口可配置为显示/隐藏。

### 🔄 版本更新

内置更新检查，自动检测 GitHub 最新 Release，支持一键下载安装包并替换，实时显示下载进度。

### 📋 排序与筛选

按「最后活动 / 建立时间 / Context 用量」排序，支持升降序。最后活动维度先按「忙碌 → 空闲 → 残留」分组，最忙的永远在最上面。

### ⚙️ 更多

- **系统托盘常驻**：关闭窗口默认隐藏到托盘，右键退出。托盘 Tooltip 实时显示概要
- **单实例**：重复启动自动激活已有窗口
- **明暗主题**：自动跟随系统，Notion 风格配色
- **命令行模式**：`claude-monitor.exe --list` 终端打印一次表格后退出，适合脚本/日志
- **开机启动**：设置面板一键开关

> 📖 更多细节见 [docs/功能介绍.md](docs/功能介绍.md)

---

## 安装

### 安装包（推荐）

从 [GitHub Releases](https://github.com/pie-tk/claude-code-monitor/releases) 下载 `claude-monitor-setup.exe`，双击安装。支持中文（简体/繁体），自动创建桌面快捷方式。

### 便携版

从 [Releases](https://github.com/pie-tk/claude-code-monitor/releases) 下载 `claude-monitor.exe`，直接运行。

> 环境要求：**Windows 10/11**（WebView2 系统自带）+ **Claude Code CLI** 已安装

---

## 使用

```bash
# GUI 模式（托盘常驻，每秒刷新）
claude-monitor.exe

# 命令行模式（打印一次后退出）
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

---

## 配置

### 自定义模型上下文上限

内置 60+ 模型的上下文上限表。如果你的模型与默认不同，在 `~/.claude-monitor.json` 覆盖：

```json
{ "modelLimits": { "glm-5.1": 256000 } }
```

### 应用设置

在 GUI 设置面板（⚙）中可直接配置：关闭按钮行为、开机启动、终端窗口模式、消息发送键。

---

## 构建

```bash
git clone git@github.com:pie-tk/claude-code-monitor.git
cd claude-code-monitor

# 一键构建（exe + 安装包）
./build.sh

# 或分步：
cd frontend && npm install && npm run build && cd ..
go build -ldflags="-H windowsgui -s -w" -o claude-monitor.exe .
```

> 依赖 Go 1.26+、Node.js。国内网络设 `go env -w GOPROXY=https://goproxy.cn,direct`

---

## License

[MIT](LICENSE)
