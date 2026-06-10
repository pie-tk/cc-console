//go:build linux

package theme

// IsSystemDarkMode 检测 Linux 系统是否为暗色模式。
// TODO: 实现 GTK/GSettings 查询
func IsSystemDarkMode() bool {
	return false
}
