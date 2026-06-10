//go:build linux

package monitor

import "fmt"

func init() {
	listClaudeProcesses = func() ([]procInfo, error) {
		// TODO: 实现 Linux 进程枚举（/proc）
		return nil, fmt.Errorf("Linux 进程枚举尚未实现")
	}
}
