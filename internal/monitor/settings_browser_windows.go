//go:build windows

package monitor

import (
	"fmt"
	"os/exec"
)

// OpenInBrowser 在系统默认浏览器中打开 URL。
func OpenInBrowser(url string) error {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		return fmt.Errorf("打开浏览器失败: %w", err)
	}
	return nil
}
