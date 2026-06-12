//go:build linux

package monitor

func init() {
	// TODO: 实现 Linux 进程存活 + 启动时间验证（/proc/<pid>/stat 第 22 字段 starttime）
	isProcessAlive = func(pid int, startedAt int64) bool {
		return false
	}
}
