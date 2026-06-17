//go:build windows

package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// resolveClaudePath 预检 claude 可执行文件是否可用（仅用于启动前的错误提示）。
// 优先级：exec.LookPath("claude") → LookPath("claude.exe") → 复用现有运行实例的 exe 路径
// （排除 Claude 桌面版）。实际启动命令统一用命令名 "claude"（交给 PowerShell 的 PATH 解析），
// 不传本函数返回的路径——避免路径含空格（如 C:\Users\PIE TK\...）时引号被剥离导致解析失败。
func resolveClaudePath() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("claude.exe"); err == nil {
		return p, nil
	}
	// 回退：从任一存活的 Claude Code 实例取 exe 路径
	for _, proc := range enumerateClaudeProcesses() {
		exe := queryFullProcessImageName(uint32(proc.pid))
		if exe != "" && !strings.Contains(strings.ToLower(exe), `\anthropicclaude\`) {
			return exe, nil
		}
	}
	return "", fmt.Errorf("未找到 claude 可执行文件，请确认已安装 Claude Code（npm i -g @anthropic-ai/claude-code）且在 PATH 中")
}

// buildClaudeArgs 根据当前设置构建 claude 命令行参数。
// LaunchYolo=true 时附加 --permission-mode bypassPermissions（等价于 claude.yolo）。
func buildClaudeArgs() string {
	if GetSettings().LaunchYolo {
		return "claude --permission-mode bypassPermissions"
	}
	return "claude"
}

// resolveShell 返回系统默认可用的 PowerShell 可执行文件名，供启动终端实例使用。
// 不写死版本以适配各人环境：优先 PowerShell 7+（pwsh，更现代、默认 RemoteSigned 不弹签名确认），
// 回退 Windows PowerShell 5.1（powershell，Windows 内置必然存在）。
func resolveShell() string {
	if _, err := exec.LookPath("pwsh.exe"); err == nil {
		return "pwsh"
	}
	return "powershell"
}

// LaunchClaudeInDir 在 workdir 启动 claude，按 mode 决定终端窗口显示方式：
//   - "show"：可见窗口（优先 Windows Terminal，回退 PowerShell）
//   - 默认（"hide"/"minimize"）：最小化窗口到任务栏（不抢焦点，可点击查看）
//
// 终端使用 PowerShell（而非 cmd），claude 启动参数由 buildClaudeArgs 控制。
// 返回终端描述（供前端反馈）。
func LaunchClaudeInDir(workdir string, mode string) (string, error) {
	if strings.TrimSpace(workdir) == "" {
		return "", fmt.Errorf("工作目录不能为空")
	}
	info, err := os.Stat(workdir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("工作目录不存在或不是目录: %s", workdir)
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		abs = workdir
	}
	// 预检 claude 是否可用（给出清晰错误，不启动）
	if _, err := resolveClaudePath(); err != nil {
		return "", err
	}

	switch mode {
	case "show":
		return launchVisible(abs)
	default: // "hide" / "minimize" → 最小化启动
		return launchMinimized(abs)
	}
}

// launchVisible 可见窗口启动：优先 Windows Terminal（-- 分隔 wt 选项与命令），回退 PowerShell Start-Process。
func launchVisible(abs string) (string, error) {
	claudeArgs := buildClaudeArgs()
	if wt, e := exec.LookPath("wt.exe"); e == nil {
		// -- 分隔符确保后面的命令被当作命令而非 wt 参数
		// -ExecutionPolicy Bypass 跳过执行策略提示：用户若把策略设为 AllSigned，
		// PowerShell 启动时会因加载 PSReadLine 模块的 .ps1xml 弹"不可信发布者"确认框，挡住 claude 启动。
		shell := resolveShell()
		cmd := exec.Command(wt, "-d", abs, "--", shell, "-ExecutionPolicy", "Bypass", "-NoExit", "-Command", claudeArgs)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		}
		if err := cmd.Start(); err == nil {
			return "Windows Terminal", nil
		}
		// wt 启动失败则继续回退 PowerShell Start-Process
	}
	// 回退：用 PowerShell Start-Process 在独立窗口启动
	return startPowerShell(abs, "Normal")
}

// launchMinimized 最小化窗口启动：用 Start-Process -WindowStyle Minimized，不抢焦点、出现在任务栏。
// 用户可随时点击任务栏图标查看终端输出（含 claude 启动错误）。
func launchMinimized(abs string) (string, error) {
	return startPowerShell(abs, "Minimized")
}

// startPowerShell 在 abs 目录启动新的 PowerShell 窗口运行 claude。
// windowStyle: PowerShell Start-Process 的 -WindowStyle 参数值（Normal / Minimized）。
// 路径中的单引号会被转义，防止 PowerShell 注入。
func startPowerShell(abs string, windowStyle string) (string, error) {
	claudeArgs := buildClaudeArgs()
	escapedPath := strings.ReplaceAll(abs, "'", "''")
	shell := resolveShell()

	// Start-Process 启动新 PowerShell 窗口；-NoExit 保持窗口不关闭
	// shell 由 resolveShell() 决定（pwsh 优先，回退 powershell），适配各人环境，不写死版本。
	// -ExecutionPolicy Bypass 跳过执行策略提示（详见 launchVisible 注释）：
	// 内层 ArgumentList 是实际运行 claude 的窗口；外层是执行 Start-Process 的辅助进程。
	// 两处都加，否则任一处卡在"不可信发布者"确认框，claude 都无法启动。
	psCmd := fmt.Sprintf(
		"Start-Process -FilePath %s -WorkingDirectory '%s' -WindowStyle %s -ArgumentList '-ExecutionPolicy Bypass -NoExit -Command %s'",
		shell, escapedPath, windowStyle, claudeArgs,
	)
	cmd := exec.Command(shell, "-ExecutionPolicy", "Bypass", "-Command", psCmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动 PowerShell 失败: %w", err)
	}
	if windowStyle == "Minimized" {
		return "最小化窗口", nil
	}
	return "PowerShell", nil
}
