package main

import (
	"fmt"
	"unicode/utf16"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// 这一组常量 / 结构体对应 Win32 控制台输入记录。golang.org/x/sys/windows 没有导出
// AttachConsole / FreeConsole / WriteConsoleInput 和 INPUT_RECORD，所以这里按字节布局
// 自己定义，并通过 kernel32 的 LazyProc 调用。

const eventTypeKey = 0x0001 // INPUT_RECORD::EventType == KEY_EVENT

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
// vk 留 0、只设 uChar——Claude Code（Node）读控制台 KEY_EVENT 时以 uChar 为准。
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
		makeKeyRecord(true, win.VK_RETURN, '\r'),
		makeKeyRecord(false, win.VK_RETURN, '\r'),
	)
}

// escapeRecords 产生两次 ESC「按下+抬起」——Claude Code 中等价于「回溯」。
func escapeRecords() []inputRecord {
	var recs []inputRecord
	for i := 0; i < 2; i++ {
		recs = append(recs,
			makeKeyRecord(true, win.VK_ESCAPE, 0x1B),
			makeKeyRecord(false, win.VK_ESCAPE, 0x1B),
		)
	}
	return recs
}

// SendInputRecords 把按键记录投递到指定进程所附属控制台的输入缓冲区。
//
// 原理：监控程序自身无控制台（-H windowsgui 构建），因此可以 AttachConsole(pid)
// 临时挂到目标 Claude Code 的控制台上，取其 STD_INPUT_HANDLE，用 WriteConsoleInput
// 投递 KEY_EVENT，再 FreeConsole 退出。这些按键对 Claude Code 而言与用户在终端里亲手
// 敲入完全等价——/clear、/rewind、普通提问都走同一条 stdin。
//
// 限制：目标进程必须「真正附在某个控制台上」（即在普通终端窗口里运行）。
// 若它由管道/重定向启动（没有控制台），AttachConsole 会失败。
func SendInputRecords(pid uint32, recs []inputRecord) error {
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
