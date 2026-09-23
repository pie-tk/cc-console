//go:build darwin

package monitor

import "fmt"

// ReadClipboardFilePaths macOS 存根：NSPasteboard 文件读取未实现，
// 前端粘贴附件功能在 mac 上暂不可用（拖放不受影响，走 Wails 原生回调）。
func ReadClipboardFilePaths() ([]string, error) {
	return nil, fmt.Errorf("macOS 暂不支持粘贴文件附件，请改为拖拽文件到输入框")
}
