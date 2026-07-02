//go:build windows

package monitor

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath         = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName       = `CCConsole`
	legacyRunValueName = `ClaudeCodeMonitor` // 改名前（v1.3.9 之前）的旧值名；旧 exe 名已不存在，残留项开机必失败，启动时清理
)

// SetAutoStart 设置/取消开机自启动。
func SetAutoStart(enable bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if enable {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		return key.SetStringValue(runValueName, exePath)
	}
	// 删除不存在的值不算错误（首次运行）
	if err := key.DeleteValue(runValueName); err != nil {
		if !isRegistryNotFound(err) {
			return err
		}
	}
	return nil
}

// isRegistryNotFound 判断注册表操作错误是否因键/值不存在。
func isRegistryNotFound(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == 2 // ERROR_FILE_NOT_FOUND
	}
	return false
}

// IsAutoStartEnabled 查询当前是否已设置开机自启。
func IsAutoStartEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false, nil // 键不存在 = 未启用
	}
	defer key.Close()
	_, _, err = key.GetStringValue(runValueName)
	if err != nil {
		return false, nil // 值不存在 = 未启用
	}
	return true, nil
}

// cleanupLegacyAutoStart 清理改名前残留的旧注册表自启项。
// v1.3.9 改名时值名从 ClaudeCodeMonitor 改为 CCConsole、exe 名从 claude-monitor.exe 改为
// cc-console.exe，旧值名指向的旧 exe 已不存在，老用户升级后开机启动会静默失败。
// 启动时幂等删除；值不存在不算错误。
func cleanupLegacyAutoStart() {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	_ = key.DeleteValue(legacyRunValueName)
}
