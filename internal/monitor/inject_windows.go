//go:build windows

package monitor

import (
	"fmt"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	Injector = &windowsInjector{}
}

type windowsInjector struct{}

// ---- Win32 DLL 声明 ----

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procAttachConsole     = kernel32.NewProc("AttachConsole")
	procFreeConsole       = kernel32.NewProc("FreeConsole")
	procWriteConsoleInput = kernel32.NewProc("WriteConsoleInputW")
	procGetConsoleWindow  = kernel32.NewProc("GetConsoleWindow")

	user32DLL = syscall.NewLazyDLL("user32.dll")

	procGetAncestor            = user32DLL.NewProc("GetAncestor")
	procShowWindow             = user32DLL.NewProc("ShowWindow")
	procSetForegroundWindow    = user32DLL.NewProc("SetForegroundWindow")
	procEnumWindows            = user32DLL.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32DLL.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible        = user32DLL.NewProc("IsWindowVisible")
	procAttachThreadInput      = user32DLL.NewProc("AttachThreadInput")
	procGetCurrentThreadId     = kernel32.NewProc("GetCurrentThreadId")
	procGetWindowTextLengthW   = user32DLL.NewProc("GetWindowTextLengthW")
	procAllowSetForegroundWindow = user32DLL.NewProc("AllowSetForegroundWindow")
	procBringWindowToTop       = user32DLL.NewProc("BringWindowToTop")
	procGetCurrentProcessId     = kernel32.NewProc("GetCurrentProcessId")
)

// ---- Win32 控制台输入记录常量/结构 ----

const eventTypeKey = 0x0001 // INPUT_RECORD::EventType == KEY_EVENT

const (
	vkReturn  = 0x0D
	vkEscape  = 0x1B
	swRestore = 9 // SW_RESTORE
)

// keyEventRecord 严格对应 Win32 KEY_EVENT_RECORD（16 字节，无填充）。
type keyEventRecord struct {
	bKeyDown          int32
	wRepeatCount      uint16
	wVirtualKeyCode   uint16
	wVirtualScanCode  uint16
	uChar             uint16 // WCHAR：控制台用它来还原字符
	dwControlKeyState uint32
}

// inputRecord 对应 Win32 INPUT_RECORD（20 字节）：
// EventType(2) + 对齐(2) + 16 字节联合体。
type inputRecord struct {
	eventType uint16
	_         [2]byte
	event     [16]byte // 联合体：写入时按 keyEventRecord 重叠解释
}

func makeKeyRecord(down bool, vk uint16, ch uint16) inputRecord {
	kev := keyEventRecord{
		bKeyDown:        boolToInt32(down),
		wRepeatCount:    1,
		wVirtualKeyCode: vk,
		uChar:           ch,
	}
	var ir inputRecord
	ir.eventType = eventTypeKey
	*(*keyEventRecord)(unsafe.Pointer(&ir.event[0])) = kev
	return ir
}

func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// textRecords 把字符串拆成「逐字符 按下+抬起」的输入记录（按 UTF-16 码元）。
func textRecords(text string) []inputRecord {
	units := utf16.Encode([]rune(text))
	recs := make([]inputRecord, 0, len(units)*2)
	for _, u := range units {
		recs = append(recs, makeKeyRecord(true, 0, u))
		recs = append(recs, makeKeyRecord(false, 0, u))
	}
	return recs
}

// withEnter 末尾追加一个回车（VK_RETURN），用于提交输入。
func withEnter(recs []inputRecord) []inputRecord {
	return append(recs,
		makeKeyRecord(true, vkReturn, '\r'),
		makeKeyRecord(false, vkReturn, '\r'),
	)
}

// escapeRecords 产生两次 ESC「按下+抬起」——Claude Code 中等价于「回溯」。
func escapeRecords() []inputRecord {
	var recs []inputRecord
	for i := 0; i < 2; i++ {
		recs = append(recs,
			makeKeyRecord(true, vkEscape, 0x1B),
			makeKeyRecord(false, vkEscape, 0x1B),
		)
	}
	return recs
}

// sendInputRecords 把按键记录投递到指定进程所附属控制台的输入缓冲区。
func sendInputRecords(pid uint32, recs []inputRecord) error {
	if len(recs) == 0 {
		return nil
	}

	// 先尝试 detach（自身无控制台时为无害空操作），避免因「已附属控制台」而失败。
	_, _, _ = procFreeConsole.Call()

	r, _, _ := procAttachConsole.Call(uintptr(pid))
	if r == 0 {
		return fmt.Errorf("无法附加到该实例的控制台（PID %d）。\n请确认它在普通终端窗口里运行；经管道/重定向启动的实例不支持", pid)
	}
	defer func() { _, _, _ = procFreeConsole.Call() }()

	h, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil || h == 0 {
		return fmt.Errorf("GetStdHandle 失败: %v", err)
	}

	var written uint32
	r, _, e := procWriteConsoleInput.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&recs[0])),
		uintptr(len(recs)),
		uintptr(unsafe.Pointer(&written)),
	)
	if r == 0 {
		return fmt.Errorf("WriteConsoleInput 失败: %v", e)
	}
	return nil
}

// ---- ConsoleInput 接口实现 ----

func (w *windowsInjector) SendClear(pid int) error {
	return sendInputRecords(uint32(pid), withEnter(textRecords("/clear")))
}

func (w *windowsInjector) SendRewind(pid int) error {
	return sendInputRecords(uint32(pid), escapeRecords())
}

func (w *windowsInjector) SendPrompt(pid int, text string) error {
	return sendInputRecords(uint32(pid), withEnter(textRecords(text)))
}

func (w *windowsInjector) ShowWindow(pid int) error {
	_, _, _ = procFreeConsole.Call()

	// ---- 路径 1：原生控制台窗口（conhost）----
	r, _, _ := procAttachConsole.Call(uintptr(pid))
	if r != 0 {
		r, _, _ = procGetConsoleWindow.Call()
		hwnd := uintptr(r)
		if hwnd != 0 {
			// ConPTY 下 GetConsoleWindow 返回 shell（cmd/powershell）的终端面板窗口，
			// SetForegroundWindow 对其无效，需识别后跳过，交给路径 2 处理。
			if !isConsoleWindowShell(hwnd) {
				if ro, _, _ := procGetAncestor.Call(hwnd, 3); ro != 0 {
					hwnd = ro
				}
				procShowWindow.Call(hwnd, uintptr(swRestore))
				procSetForegroundWindow.Call(hwnd)
				_, _, _ = procFreeConsole.Call()
				return nil
			}
		}
		_, _, _ = procFreeConsole.Call()
	}

	// ---- 路径 2：ConPTY 伪控制台（IDE 内嵌终端等，无原生 HWND）----
	// 沿进程祖先链向上查找拥有可见顶层窗口的进程
	hwnd := findWindowForPID(uint32(pid))
	if hwnd == 0 {
		return fmt.Errorf("未找到窗口（PID %d）\n该实例可能运行在无窗口环境中", pid)
	}

	// 多步组合绕过 Windows 焦点锁定机制：
	//   1. AllowSetForegroundWindow 授予当前进程置前权限
	//   2. AttachThreadInput 连接两个线程的输入队列（绕过 Vista+ 限制）
	//   3. ShowWindow SW_RESTORE 还原最小化窗口
	//   4. SetForegroundWindow + BringWindowToTop 强制置前
	curPID, _, _ := procGetCurrentProcessId.Call()
	procAllowSetForegroundWindow.Call(curPID) // ASFW_ANY = 当前进程

	targetThread, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	curThread, _, _ := procGetCurrentThreadId.Call()

	if curThread != targetThread {
		procAttachThreadInput.Call(curThread, targetThread, 1)
		procShowWindow.Call(hwnd, uintptr(swRestore))
		procSetForegroundWindow.Call(hwnd)
		procBringWindowToTop.Call(hwnd)
		procAttachThreadInput.Call(curThread, targetThread, 0)
	} else {
		procShowWindow.Call(hwnd, uintptr(swRestore))
		procSetForegroundWindow.Call(hwnd)
		procBringWindowToTop.Call(hwnd)
	}
	return nil
}

// getParentPID 返回 pid 的父进程 PID，失败返回 0。
func getParentPID(pid uint32) uint32 {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snapshot)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snapshot, &pe); err != nil {
		return 0
	}
	for {
		if pe.ProcessID == pid {
			return pe.ParentProcessID
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}
	return 0
}

// findWindowForPID 沿进程祖先链向上查找拥有可见顶层窗口的进程，
// 最多向上追溯 5 级。跳过 cmd.exe / powershell.exe 等 shell 进程，
// 因为它们在 ConPTY 下会产生「幽灵」可见窗口（实际无法被 SetForegroundWindow 拉起）。
func findWindowForPID(pid uint32) uintptr {
	current := pid
	for range 5 {
		hwnd := findTopLevelWindow(current)
		if hwnd != 0 && !isShellProcess(current) {
			return hwnd
		}
		parent := getParentPID(current)
		if parent == 0 || parent == current {
			break
		}
		current = parent
	}
	return 0
}

// isShellProcess 判断一个进程是否是命令行 shell（在 ConPTY 下它们的窗口不可靠）。
func isShellProcess(pid uint32) bool {
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
			return name == "cmd.exe" || name == "powershell.exe" || name == "pwsh.exe"
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}
	return false
}

// isConsoleWindowShell 判断 GetConsoleWindow 返回的窗口是否属于 shell 进程。
// ConPTY 下 GetConsoleWindow 会返回 shell（powershell/cmd）的终端面板窗口，
// 该窗口虽然可见但无法被 SetForegroundWindow 正常拉起，需走祖先链回溯。
func isConsoleWindowShell(hwnd uintptr) bool {
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid != 0 && isShellProcess(pid)
}

// findTopLevelWindow 枚举顶层窗口，返回属于 pid 的第一个可见窗口句柄。
func findTopLevelWindow(pid uint32) uintptr {
	var result uintptr

	target := &struct {
		pid    uint32
		hwnd   uintptr
		found  bool
	}{pid: pid}

	cb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		var wndPID uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&wndPID)))
		if wndPID == target.pid {
			vis, _, _ := procIsWindowVisible.Call(hwnd)
			if vis != 0 {
				// 真正的应用主窗口必须有标题；无标题的是辅助/Popup 窗口，SetForegroundWindow 对它们无效
				titleLen, _, _ := procGetWindowTextLengthW.Call(hwnd)
				if titleLen > 0 {
					target.hwnd = hwnd
					target.found = true
					return 0 // 停止枚举
				}
			}
		}
		return 1 // 继续枚举
	})

	procEnumWindows.Call(cb, 0)
	if target.found {
		result = target.hwnd
	}
	return result
}
