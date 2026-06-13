package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings 持久化到 ~/.claude-monitor.json 的应用设置。
type Settings struct {
	ModelLimits   map[string]int64 `json:"modelLimits"`
	CloseQuits    bool             `json:"closeQuits"`
	AutoStart     bool             `json:"autoStart"`
	BridgeEnabled bool             `json:"bridgeEnabled"` // statusline 桥接（默认启用）
}

var currentSettings Settings

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", err
	}
	return filepath.Join(home, ".claude-monitor.json"), nil
}

// LoadSettings 从磁盘加载设置，首次运行返回默认值。
func LoadSettings() error {
	currentSettings = Settings{
		ModelLimits:   map[string]int64{},
		BridgeEnabled: true, // 默认启用 statusline 桥接
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // 文件不存在 → 用默认值
	}
	json.Unmarshal(data, &currentSettings)
	return nil
}

// SaveSettings 将设置写回磁盘。
func SaveSettings(s Settings) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	currentSettings = s
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// GetSettings 返回当前设置（线程安全由调用者保证）。
func GetSettings() Settings {
	return currentSettings
}

// IsCloseQuit 返回关闭按钮是否应直接退出。
func IsCloseQuit() bool {
	return currentSettings.CloseQuits
}
