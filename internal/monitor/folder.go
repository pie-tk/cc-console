//go:build !windows

package monitor

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenInFolder 在系统文件管理器中打开指定目录。
func OpenInFolder(path string) error {
	switch runtime.GOOS {
	case "darwin":
		// macOS: open
		if err := exec.Command("open", path).Start(); err != nil {
			return fmt.Errorf("打开文件夹失败: %w", err)
		}
		return nil
	default:
		// Linux: xdg-open（含 WSL 分支）
		var cmd string
		args := []string{path}
		if isWSL() {
			cmd = "cmd.exe"
			args = []string{"/c", "start", path}
		} else {
			cmd = "xdg-open"
		}
		if err := exec.Command(cmd, args...).Start(); err != nil {
			return fmt.Errorf("打开文件夹失败: %w", err)
		}
		return nil
	}
}
