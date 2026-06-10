//go:build windows

package monitor

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	listClaudeProcesses = listClaudeProcessesWindows
}

func listClaudeProcessesWindows() ([]procInfo, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	var out []procInfo
	// Toolhelp32 快照在进程增删瞬间偶尔会把同一个进程列出两次，
	// 导致同一 PID 在界面里重复成两行。按 PID 去重，保证每个 PID 最多一条。
	seen := make(map[int]bool)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snapshot, &pe); err != nil {
		return nil, err
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		name = strings.TrimSuffix(name, ".exe")
		if name == "claude" {
			pid := int(pe.ProcessID)
			if !seen[pid] {
				seen[pid] = true
				out = append(out, procInfo{
					pid:          pid,
					exePath:      processExePath(pe.ProcessID),
					createTimeMs: processCreateTimeMs(pe.ProcessID),
				})
			}
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}
	return out, nil
}

func processExePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var buf [windows.MAX_PATH + 1]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

func processCreateTimeMs(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0
	}
	return filetimeToEpochMs(c)
}

func filetimeToEpochMs(ft windows.Filetime) int64 {
	const epochDiff100ns = 116444736000000000
	n := uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
	if n < epochDiff100ns {
		return 0
	}
	return int64((n - epochDiff100ns) / 10000)
}
