//go:build !windows

package monitor

// SetAutoStart 非 Windows 平台暂不支持。
func SetAutoStart(bool) error { return nil }

// IsAutoStartEnabled 非 Windows 平台暂不支持。
func IsAutoStartEnabled() (bool, error) { return false, nil }
