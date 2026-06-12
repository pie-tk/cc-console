//go:build windows

package monitor

import (
	"golang.org/x/sys/windows"
)

func init() {
	isProcessAlive = isProcessAliveWindows
}

// isProcessAliveWindows 验证 pid 对应的进程仍在运行，且创建时间与 session 记录的
// startedAt 一致（±15s 容差，排除 PID 复用）。不依赖进程名——session 文件的 pid 由
// Claude Code 启动时写入，天然可信，只需确认它还活着且未被复用。
func isProcessAliveWindows(pid int, startedAt int64) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false // 进程已退出或无权限访问
	}
	defer windows.CloseHandle(h)
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return false
	}
	createMs := filetimeToEpochMs(c)
	if createMs == 0 {
		return false
	}
	return abs64(createMs-startedAt) <= 15000
}

func filetimeToEpochMs(ft windows.Filetime) int64 {
	const epochDiff100ns = 116444736000000000
	n := uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
	if n < epochDiff100ns {
		return 0
	}
	return int64((n - epochDiff100ns) / 10000)
}
