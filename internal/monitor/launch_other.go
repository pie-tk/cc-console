//go:build !windows

package monitor

import "fmt"

func resolveClaudePath() (string, error) {
	return "", fmt.Errorf("当前平台暂不支持解析 claude 路径")
}

// LaunchClaudeInDir 非 Windows 平台存根。
func LaunchClaudeInDir(workdir string, mode string) (string, error) {
	return "", fmt.Errorf("当前平台暂不支持启动 claude 实例（仅 Windows 已实现）")
}
