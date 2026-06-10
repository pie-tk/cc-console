//go:build windows

package monitor

import (
	"fmt"
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

	procGetAncestor       = user32DLL.NewProc("GetAncestor")
	procShowWindow        = user32DLL.NewProc("ShowWindow")
	procSetForegroundWindow = user32DLL.NewProc("SetForegroundWindow")
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

	r, _, _ := procAttachConsole.Call(uintptr(pid))
	if r == 0 {
		return fmt.Errorf("无法找到窗口（PID %d）\n请确认它在普通终端窗口里运行", pid)
	}
	defer func() { _, _, _ = procFreeConsole.Call() }()

	r, _, _ = procGetConsoleWindow.Call()
	hwnd := uintptr(r)
	if hwnd == 0 {
		return fmt.Errorf("未找到控制台窗口（PID %d）", pid)
	}

	// 向上取根属主窗口（兼容 conpty / Windows Terminal）
	if ro, _, _ := procGetAncestor.Call(hwnd, 3); ro != 0 {
		hwnd = ro
	}

	procShowWindow.Call(hwnd, uintptr(swRestore))
	procSetForegroundWindow.Call(hwnd)

	return nil
}
