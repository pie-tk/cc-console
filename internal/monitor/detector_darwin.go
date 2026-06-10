//go:build darwin

package monitor

import "fmt"

func init() {
	listClaudeProcesses = func() ([]procInfo, error) {
		// TODO: 实现 macOS 进程枚举（ps / syscall.Sysctl）
		return nil, fmt.Errorf("macOS 进程枚举尚未实现")
	}
}
