//go:build windows

package theme

import (
	"golang.org/x/sys/windows/registry"
)

// IsSystemDarkMode 检测 Windows 系统是否为暗色模式。
func IsSystemDarkMode() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return val == 0
}
