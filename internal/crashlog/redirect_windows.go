//go:build windows

package crashlog

import (
	"os"

	"golang.org/x/sys/windows"
)

// redirectStdHandles 把进程的标准输出/错误句柄指向日志文件，
// 使 Go runtime 写到 fd 1/2 的内容（panic / fatal error 堆栈）落入日志。
// 实验验证：windowsgui 下 SetStdHandle 能让 runtime fatal error 的完整堆栈落盘。
func redirectStdHandles(f *os.File) {
	h := windows.Handle(f.Fd())
	windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, h)
	windows.SetStdHandle(windows.STD_ERROR_HANDLE, h)
}
