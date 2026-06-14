//go:build windows

package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// resolveClaudePath 预检 claude 可执行文件是否可用（仅用于启动前的错误提示）。
// 优先级：exec.LookPath("claude") → LookPath("claude.exe") → 复用现有运行实例的 exe 路径
// （排除 Claude 桌面版）。实际启动命令统一用命令名 "claude"（交给 cmd 的 PATH 解析），
// 不传本函数返回的路径——避免路径含空格（如 C:\Users\PIE TK\...）时 cmd /c 的引号被
// 剥离导致解析失败、claude 根本没启动。
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

// LaunchClaudeInDir 在 workdir 启动 claude，按 mode 决定终端窗口显示方式：
//   - "show"：可见窗口（优先 Windows Terminal，回退 cmd）
//   - "hide"：完全隐藏窗口（start 启动 + 枚举窗口 SW_HIDE）；旧的 "minimize" 值也走此分支
//
// 启动命令统一用命令名 "claude"（cmd 的 PATH 解析），不传 claudePath——避免路径含空格
// 时 cmd /c "path" 的引号被剥离导致解析失败。返回终端描述（供前端反馈）。
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
	default: // "hide"（兼容旧的 "minimize" 值）
		return launchHidden(abs)
	}
}

// launchVisible 可见窗口启动：优先 Windows Terminal，回退 cmd。
func launchVisible(abs string) (string, error) {
	if wt, e := exec.LookPath("wt.exe"); e == nil {
		cmd := exec.Command(wt, "-d", abs, "cmd", "/k", "claude")
		if err := cmd.Start(); err == nil {
			return "Windows Terminal", nil
		}
		// wt 启动失败则继续回退 cmd
	}
	cmd := exec.Command("cmd", "/c", "start", "", "/D", abs, "cmd", "/k", "claude")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动命令提示符失败: %w", err)
	}
	return "命令提示符", nil
}

// launchHidden 完全隐藏窗口启动：用 cmd /c start 可靠方式启动 claude，
// 然后另起 goroutine 枚举顶层窗口，把"启动后新增的、属于终端宿主进程的可见窗口"SW_HIDE。
// 用启动前快照排除用户已打开的终端窗口，避免误伤。需要查看时点卡片的「窗口」按钮还原。
//
// 不用 AttachConsole+GetConsoleWindow：在默认终端=Windows Terminal 时，GetConsoleWindow
// 只返回伪控制台窗口（本来就不可见），SW_HIDE 它是空操作，wt 真实窗口不会被隐藏。
// 也不用 CREATE_NEW_CONSOLE+HideWindow 直接 spawn：那样在某些配置下 claude 无法正常启动。
func launchHidden(abs string) (string, error) {
	before := snapshotVisibleWindows() // 启动前快照，用于识别"新增"的终端窗口
	cmd := exec.Command("cmd", "/c", "start", "", "/D", abs, "cmd", "/k", "claude")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动（隐藏）失败: %w", err)
	}
	go hideClaudeTerminal(before)
	return "隐藏窗口", nil
}

// hideClaudeTerminal 在启动后约 3 秒内轮询，隐藏新增的终端窗口。枚举窗口法同时覆盖
// conhost 与 Windows Terminal 两种默认终端（它们的窗口都是顶层窗口，属于终端宿主进程）。
func hideClaudeTerminal(before map[uintptr]bool) {
	for i := 0; i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		hideNewTerminalWindows(before)
	}
}

// hideNewTerminalWindows 枚举顶层窗口，把启动后新增的、属于终端宿主进程（cmd/wt/conhost
// 等）的可见窗口 SW_HIDE。用 before 快照排除启动前已存在的窗口，避免误伤用户其它终端。
func hideNewTerminalWindows(before map[uintptr]bool) {
	cb := syscall.NewCallback(func(hwnd uintptr, l uintptr) uintptr {
		if before[hwnd] {
			return 1 // 启动前就有的窗口，跳过
		}
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if isTerminalHostPID(pid) {
			procShowWindow.Call(hwnd, 0) // SW_HIDE = 0
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
}

// snapshotVisibleWindows 枚举所有可见顶层窗口，返回句柄集合（启动前快照）。
func snapshotVisibleWindows() map[uintptr]bool {
	set := make(map[uintptr]bool)
	cb := syscall.NewCallback(func(hwnd uintptr, l uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis != 0 {
			set[hwnd] = true
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return set
}

// isTerminalHostPID 判断 pid 是否为终端宿主进程（cmd/wt/conhost/OpenConsole/powershell）。
// 这些进程的顶层窗口是 claude 运行所在的终端窗口。
func isTerminalHostPID(pid uint32) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snapshot, &pe); err != nil {
		return false
	}
	for {
		if pe.ProcessID == pid {
			name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
			switch name {
			case "cmd.exe", "wt.exe", "conhost.exe", "OpenConsole.exe", "powershell.exe", "pwsh.exe":
				return true
			}
			return false
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}
	return false
}
