//go:build windows

package monitor

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---- 剪贴板文件路径读取（CF_HDROP）----
// WebView2 的 JS paste 事件只给 File 对象（无绝对路径），资源管理器 Ctrl+C
// 复制的文件路径在本机剪贴板以 CF_HDROP 存放，这里读出来供前端做“粘贴附件”。

const cfHDROP = 15 // CF_HDROP

var (
	shell32DLL = syscall.NewLazyDLL("shell32.dll")

	procDragQueryFileW = shell32DLL.NewProc("DragQueryFileW")

	procOpenClipboard             = user32DLL.NewProc("OpenClipboard")
	procCloseClipboard            = user32DLL.NewProc("CloseClipboard")
	procIsClipboardFormatAvailable = user32DLL.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData          = user32DLL.NewProc("GetClipboardData")
	procGlobalLock                = kernel32.NewProc("GlobalLock")
	procGlobalUnlock              = kernel32.NewProc("GlobalUnlock")
)

// ReadClipboardFilePaths 读取剪贴板中的文件路径列表（资源管理器复制的文件）。
// 剪贴板无文件时返回空列表；被其他程序占用时短暂重试后报错。
func ReadClipboardFilePaths() ([]string, error) {
	// OpenClipboard 可能被其他进程短暂持有，重试几次
	var opened uintptr
	for i := 0; i < 5; i++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			opened = r
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if opened == 0 {
		return nil, fmt.Errorf("无法打开剪贴板（正被其他程序占用）")
	}
	defer procCloseClipboard.Call()

	r, _, _ := procIsClipboardFormatAvailable.Call(cfHDROP)
	if r == 0 {
		return nil, nil // 剪贴板里没有文件，正常返回空
	}

	h, _, _ := procGetClipboardData.Call(cfHDROP)
	if h == 0 {
		return nil, fmt.Errorf("读取剪贴板文件数据失败")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil, fmt.Errorf("锁定剪贴板内存失败")
	}
	defer procGlobalUnlock.Call(h)

	// DragQueryFileW(hDrop, 0xFFFFFFFF, nil, 0) 返回文件个数
	count, _, _ := procDragQueryFileW.Call(ptr, 0xFFFFFFFF, 0, 0)
	paths := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		// 先查该路径所需字符数（不含结尾 0），再取内容
		n, _, _ := procDragQueryFileW.Call(ptr, i, 0, 0)
		if n == 0 {
			continue
		}
		buf := make([]uint16, n+1)
		procDragQueryFileW.Call(ptr, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		paths = append(paths, windows.UTF16ToString(buf))
	}
	return paths, nil
}
