//go:build windows

package monitor

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	processCmdline = func(pid int) string {
		s, _ := getProcessCmdline(uint32(pid))
		return s
	}
}

// getProcessCmdline 读目标进程命令行(经 PEB→ProcessParameters→CommandLine)。
// 复用 detector_windows.go 的 NtQueryInformationProcess + ReadProcessMemory 套路;
// 区别仅在偏移:CurrentDirectory.DosPath 在 +0x38(getProcessCwd 用),CommandLine 在 +0x70。
func getProcessCmdline(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	// 1. PEB 地址
	var pbi processBasicInformation
	status, _, _ := procNtQueryInformationProcess.Call(
		uintptr(h), uintptr(processBasicInfoClass),
		uintptr(unsafe.Pointer(&pbi)), uintptr(unsafe.Sizeof(pbi)), 0,
	)
	if status != 0 || pbi.PebBaseAddress == 0 {
		return "", fmt.Errorf("NtQueryInformationProcess 失败: 0x%x", status)
	}

	// 2. PEB + 0x20 → ProcessParameters
	var procParams uintptr
	if err := readMem(h, pbi.PebBaseAddress+0x20, (*byte)(unsafe.Pointer(&procParams)), unsafe.Sizeof(procParams)); err != nil {
		return "", err
	}
	if procParams == 0 {
		return "", fmt.Errorf("ProcessParameters 为空")
	}

	// 3. ProcessParameters + 0x70 → CommandLine(UNICODE_STRING)
	var us rtlUnicodeString
	if err := readMem(h, procParams+0x70, (*byte)(unsafe.Pointer(&us)), unsafe.Sizeof(us)); err != nil {
		return "", err
	}
	if us.Length == 0 || us.Buffer == 0 {
		return "", nil
	}

	// 4. 读 CommandLine.Buffer(UTF-16)
	n := int(us.Length)
	if n > 8192 { // 防异常长度
		n = 8192
	}
	buf := make([]uint16, n/2)
	if err := readMem(h, us.Buffer, (*byte)(unsafe.Pointer(&buf[0])), uintptr(n)); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}
