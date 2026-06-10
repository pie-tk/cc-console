//go:build darwin

package theme

// IsSystemDarkMode 检测 macOS 系统是否为暗色模式。
// TODO: 实现 NSUserDefaults 查询
func IsSystemDarkMode() bool {
	return false
}
