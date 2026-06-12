//go:build darwin

package monitor

func init() {
	// TODO: 实现 macOS 进程存活 + 启动时间验证（sysctl KERN_PROC / kill(pid,0)）
	isProcessAlive = func(pid int, startedAt int64) bool {
		return false
	}
}
