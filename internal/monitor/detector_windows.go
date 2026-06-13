//go:build windows

package monitor

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	isProcessAlive = isProcessAliveWindows
	enumerateClaude = enumerateClaudeProcesses
	procCwd = func(pid int) string {
		cwd, err := getProcessCwd(uint32(pid))
		if err != nil {
			return ""
		}
		return cwd
	}
}

// isProcessAliveWindows 验证 pid 对应的进程仍在运行，且创建时间与给定 startedAt 一致
// （±15s 容差，排除 PID 复用）。
func isProcessAliveWindows(pid int, startedAt int64) bool {
	createMs := processCreateMs(uint32(pid))
	if createMs == 0 {
		return false
	}
	return abs64(createMs-startedAt) <= 15000
}

// processCreateMs 返回 pid 的进程创建时间（epoch 毫秒），失败返回 0。
func processCreateMs(pid uint32) int64 {
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

// enumerateClaudeProcesses 枚举当前所有 claude.exe 进程，返回 pid 与创建时间。
// 用 Toolhelp32 快照遍历，按进程名（大小写不敏感）匹配 claude.exe，
// 覆盖 npm/独立安装等所有安装方式——不依赖 Claude Code 写任何 session 文件。
func enumerateClaudeProcesses() []claudeProc {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)

	var out []claudeProc
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snapshot, &pe); err != nil {
		return nil
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		if name == "claude.exe" {
			pid := int(pe.ProcessID)
			// 排除 Claude 桌面应用(AnthropicClaude):它同样名为 claude.exe,但位于
			// Electron 安装目录,会派生多个子进程,不应算作 Claude Code 实例。
			exePath := strings.ToLower(queryFullProcessImageName(uint32(pid)))
			if !strings.Contains(exePath, `\anthropicclaude\`) {
				out = append(out, claudeProc{pid: pid, createMs: processCreateMs(uint32(pid))})
			}
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}
	return out
}

// queryFullProcessImageName 返回 pid 的完整 exe 路径,失败返回空串。
// 用于区分 Claude Code CLI 与同名 claude.exe 的 Claude 桌面应用。
func queryFullProcessImageName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var buf [1024]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageName.Call(
		uintptr(h), 0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// getProcessCwd 读取目标进程的工作目录（经 PEB → ProcessParameters → CurrentDirectory）。
// 偏移按 64 位 Windows PEB 布局（本应用只构建 64 位 GUI）。失败时调用方应 fallback。
func getProcessCwd(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	// 1. NtQueryInformationProcess(ProcessBasicInformation) → PEB 地址
	var pbi processBasicInformation
	status, _, _ := procNtQueryInformationProcess.Call(
		uintptr(h), uintptr(processBasicInfoClass),
		uintptr(unsafe.Pointer(&pbi)), uintptr(unsafe.Sizeof(pbi)), 0,
	)
	if status != 0 || pbi.PebBaseAddress == 0 {
		return "", fmt.Errorf("NtQueryInformationProcess 失败: 0x%x", status)
	}

	// 2. PEB + 0x20 → ProcessParameters 指针（64 位偏移）
	var procParams uintptr
	if err := readMem(h, pbi.PebBaseAddress+0x20, (*byte)(unsafe.Pointer(&procParams)), unsafe.Sizeof(procParams)); err != nil {
		return "", err
	}
	if procParams == 0 {
		return "", fmt.Errorf("ProcessParameters 为空")
	}

	// 3. ProcessParameters + 0x38 → CurrentDirectory.DosPath（RTL_UNICODE_STRING）
	var us rtlUnicodeString
	if err := readMem(h, procParams+0x38, (*byte)(unsafe.Pointer(&us)), unsafe.Sizeof(us)); err != nil {
		return "", err
	}
	if us.Length == 0 || us.Buffer == 0 {
		return "", nil
	}

	// 4. 读 DosPath.Buffer（UTF-16 编码，Length 为字节数）
	n := int(us.Length)
	if n > 2080 { // 1040 个 WCHAR，远超 MAX_PATH，防异常长度
		n = 2080
	}
	buf := make([]uint16, n/2)
	if err := readMem(h, us.Buffer, (*byte)(unsafe.Pointer(&buf[0])), uintptr(n)); err != nil {
		return "", err
	}
	cwd := windows.UTF16ToString(buf)
	// DosPath 形如 "C:\foo\bar\"，去掉末尾反斜杠以与 Claude Code 记录的 cwd 一致
	return strings.TrimRight(cwd, `\`), nil
}

// readMem 从目标进程 base 地址读 size 字节到 dst（dst 为 *byte）。
func readMem(h windows.Handle, base uintptr, dst *byte, size uintptr) error {
	var read uintptr
	if err := windows.ReadProcessMemory(h, base, dst, size, &read); err != nil || read != size {
		return fmt.Errorf("ReadProcessMemory 失败: %v", err)
	}
	return nil
}

// ---- PEB 相关结构（64 位布局）----

const processBasicInfoClass = 0 // ProcessBasicInformation

// processBasicInformation 对应 ntdll 的 PROCESS_BASIC_INFORMATION（64 位）。
type processBasicInformation struct {
	Reserved1       uintptr
	PebBaseAddress  uintptr
	Reserved2       [2]uintptr
	UniqueProcessId uintptr
	Reserved3       uintptr
}

// rtlUnicodeString 对应 RTL_UNICODE_STRING（64 位）：2+2+4(对齐)+8(Buffer) = 16 字节。
type rtlUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	_             [4]byte // 对齐到 8 字节边界
	Buffer        uintptr
}

// ---- ntdll 声明 ----

var (
	ntdll                         = syscall.NewLazyDLL("ntdll.dll")
	procNtQueryInformationProcess = ntdll.NewProc("NtQueryInformationProcess")
	// procQueryFullProcessImageName 复用 inject_windows.go 的 kernel32。
	procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
)
