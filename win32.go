package main

import "syscall"

// 集中声明所有 Win32 DLL / LazyProc，供各文件引用。
// 避免同一 DLL 在不同文件里重复 NewLazyDLL。

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procAttachConsole     = kernel32.NewProc("AttachConsole")
	procFreeConsole       = kernel32.NewProc("FreeConsole")
	procWriteConsoleInput = kernel32.NewProc("WriteConsoleInputW")
	procCreateMutexW      = kernel32.NewProc("CreateMutexW")
	procGetConsoleWindow  = kernel32.NewProc("GetConsoleWindow")

	user32DLL = syscall.NewLazyDLL("user32.dll")

	procAppendMenuW   = user32DLL.NewProc("AppendMenuW")
	procFindWindowW   = user32DLL.NewProc("FindWindowW")
	procGetAncestor   = user32DLL.NewProc("GetAncestor")
	procShowScrollBar = user32DLL.NewProc("ShowScrollBar")

	libdwmapi                 = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttribute = libdwmapi.NewProc("DwmSetWindowAttribute")
)
