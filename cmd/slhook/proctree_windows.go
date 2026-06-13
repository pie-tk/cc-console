//go:build windows

package main

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// findClaudePID 从自身进程起沿父进程链向上查找 claude.exe,返回其 pid。
// statusLine 命令是 claude.exe(直接或经 cmd/node)启动的子进程,故父链上必有 claude.exe。
// 找不到则回退到直接父进程(兜底,保证有 key 可写)。
func findClaudePID() int {
	parent, name := snapshotProcesses()
	if len(parent) == 0 {
		// 快照失败,至少回退直接父进程
		if ppid := parentPID(getCurrentPID()); ppid != 0 {
			return int(ppid)
		}
		return 0
	}

	cur := getCurrentPID()
	for i := 0; i < 8; i++ {
		p, ok := parent[cur]
		if !ok || p == 0 || p == cur {
			break
		}
		if strings.EqualFold(name[p], "claude.exe") {
			return int(p)
		}
		cur = p
	}
	// 回退:直接父进程
	if p, ok := parent[getCurrentPID()]; ok && p != 0 {
		return int(p)
	}
	return 0
}

// snapshotProcesses 一次快照,返回 pid→父pid 和 pid→进程名(小写)。
func snapshotProcesses() (parent map[uint32]uint32, name map[uint32]string) {
	parent = map[uint32]uint32{}
	name = map[uint32]string{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return
	}
	for {
		parent[pe.ProcessID] = pe.ParentProcessID
		name[pe.ProcessID] = strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return
}

func getCurrentPID() uint32 {
	return windows.GetCurrentProcessId()
}

// parentPID 单 pid 取父 pid(快照失败时的兜底)。
func parentPID(pid uint32) uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return 0
	}
	for {
		if pe.ProcessID == pid {
			return pe.ParentProcessID
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return 0
}
