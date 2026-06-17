//go:build !windows

package crashlog

import "os"

// redirectStdHandles 非 Windows：os.Stdout/os.Stderr 已在 Setup 中指向文件，覆盖 Go 层输出。
// Unix 下捕获 runtime 层 fatal 需 syscall.Dup2 重定向 fd，本程序以 Windows 为主（macOS/Linux 暂为存根），此处从简。
func redirectStdHandles(f *os.File) {}
