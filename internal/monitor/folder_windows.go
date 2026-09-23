//go:build windows

package monitor

import (
	"fmt"
	"os/exec"
)

// OpenInFolder 在资源管理器中打开指定目录。
// 注意 explorer.exe 无论成败都返回退出码 1，故只 Start 不等待退出码。
func OpenInFolder(path string) error {
	if err := exec.Command("explorer", path).Start(); err != nil {
		return fmt.Errorf("打开文件夹失败: %w", err)
	}
	return nil
}
