//go:build !windows

package monitor

import (
	"fmt"
	"os/exec"
)

// OpenInBrowser 在系统默认浏览器中打开 URL。
func OpenInBrowser(url string) error {
	// macOS: open, Linux: xdg-open
	var cmd string
	args := []string{url}
	if isWSL() {
		cmd = "cmd.exe"
		args = []string{"/c", "start", url}
	} else {
		cmd = "xdg-open"
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		return fmt.Errorf("打开浏览器失败: %w", err)
	}
	return nil
}

func isWSL() bool {
	_, err := exec.LookPath("cmd.exe")
	return err == nil
}
