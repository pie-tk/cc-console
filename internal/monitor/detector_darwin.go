//go:build darwin

package monitor

func init() {
	// TODO: 实现 macOS 进程存活 + 启动时间验证（sysctl KERN_PROC / kill(pid,0)）
	isProcessAlive = func(pid int, startedAt int64) bool {
		return false
	}
	// TODO: 实现 macOS claude 进程枚举（ps / libproc）+ 工作目录（proc_pidinfo）
	enumerateClaude = func() []claudeProc { return nil }
	enumerateChildren = func(claudePids []int) map[int][]int { return nil }
	procCwd = func(pid int) string { return "" }
}
