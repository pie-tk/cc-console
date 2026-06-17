package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings 持久化到 ~/.claude-monitor.json 的应用设置。
type Settings struct {
	ModelLimits      map[string]int64 `json:"modelLimits"`
	CloseQuits       bool             `json:"closeQuits"`
	AutoStart        bool             `json:"autoStart"`
	BridgeEnabled    bool             `json:"bridgeEnabled"`    // statusline 桥接（默认启用）
	RecentDirs       []string         `json:"recentDirs"`       // 最近工作目录（≤8，最近在前）
	LaunchWindowMode string           `json:"launchWindowMode"` // 启动终端窗口模式: show 显示 / hide 最小化到任务栏
	EnterToSend      bool             `json:"enterToSend"`      // 回车直接发送（默认 true）；false 时 Shift+Enter 发送
	LaunchYolo       bool             `json:"launchYolo"`       // 新建实例时使用 bypassPermissions 模式（默认 true）
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
		ModelLimits:      map[string]int64{},
		BridgeEnabled:    true,       // 默认启用 statusline 桥接
		LaunchWindowMode: "hide",     // 默认最小化到任务栏（不抢焦点）
		EnterToSend:      true,       // 默认回车直接发送（Shift+Enter 换行）
		LaunchYolo:       true,       // 默认 yolo 模式（跳过权限确认）
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
