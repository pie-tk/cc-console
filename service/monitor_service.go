package service

import (
	"fmt"
	"strings"
	"time"

	"claude-monitor/internal/monitor"
	"claude-monitor/internal/theme"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// MonitorService 是 Wails 服务，所有导出方法自动暴露给前端 JS。
type MonitorService struct {
	app    *application.App
	window *application.WebviewWindow
}

// NewMonitorService 创建服务实例。
func NewMonitorService() *MonitorService {
	return &MonitorService{}
}

// SetApp 在 ServiceStartup 中设置 app 引用。
func (s *MonitorService) SetApp(app *application.App, win *application.WebviewWindow) {
	s.app = app
	s.window = win
}

// GetWindow 返回当前窗口引用。
func (s *MonitorService) GetWindow() *application.WebviewWindow {
	return s.window
}

// ---- 数据查询 ----

// DetectResult 是 DetectInstances 的返回结构。
type DetectResult struct {
	Live  []monitor.Instance `json:"live"`
	Stale []monitor.Instance `json:"stale"`
	Stats monitor.StatsInfo  `json:"stats"`
}

// DetectInstances 检测当前所有 Claude Code 实例。
func (s *MonitorService) DetectInstances() (*DetectResult, error) {
	live, stale, err := monitor.Detect()
	if err != nil {
		return nil, err
	}
	offline := 0
	for _, inst := range live {
		if !inst.Live {
			offline++
		}
	}
	return &DetectResult{
		Live:  live,
		Stale: stale,
		Stats: monitor.StatsInfo{
			Online:  len(live),
			Busy:    monitor.CountStatus(live, "busy"),
			Idle:    monitor.CountStatus(live, "idle"),
			Stale:   len(stale),
			Offline: offline,
			TotalTokens: monitor.TotalTokens(live),
		},
	}, nil
}

// ThemeInfo 返回当前主题信息。
type ThemeInfo struct {
	IsDark bool              `json:"isDark"`
	CSS    map[string]string `json:"css"`
}

// GetTheme 返回当前系统主题状态和 CSS 变量。
func (s *MonitorService) GetTheme() *ThemeInfo {
	dark := theme.IsSystemDarkMode()
	return &ThemeInfo{
		IsDark: dark,
		CSS:    theme.PaletteToCSSMap(dark),
	}
}

// GetClock 返回当前时间字符串。
func (s *MonitorService) GetClock() string {
	return time.Now().Format("15:04:05")
}

// ---- 操作 ----

// ActClear 清空目标实例的对话。
func (s *MonitorService) ActClear(pid int) error {
	return monitor.Injector.SendClear(pid)
}

// ActRewind 回溯目标实例。
func (s *MonitorService) ActRewind(pid int) error {
	return monitor.Injector.SendRewind(pid)
}

// ActPrompt 向目标实例发送文本。
func (s *MonitorService) ActPrompt(pid int, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("输入不能为空")
	}
	flat := strings.ReplaceAll(text, "\r\n", " ")
	flat = strings.ReplaceAll(flat, "\n", " ")
	return monitor.Injector.SendPrompt(pid, flat)
}

// ActShowWindow 将目标实例的终端窗口置前。
func (s *MonitorService) ActShowWindow(pid int) error {
	return monitor.Injector.ShowWindow(pid)
}

// GetChatHistory 返回指定 PID 实例的完整会话消息历史（含工具调用/结果）。
func (s *MonitorService) GetChatHistory(pid int) (*monitor.ChatHistoryResult, error) {
	si, ok := monitor.GetCachedSession(pid)
	if !ok {
		return nil, fmt.Errorf("未找到 PID %d 的会话（实例可能已退出）", pid)
	}
	result := monitor.GetChatHistory(si)
	return &result, nil
}

// ---- statusline 桥接 ----

// BridgeInfo 返回 statusline 桥接的配置状态 + 当前实时接入比例。
type BridgeInfo struct {
	monitor.BridgeStatus
	HookedCount int `json:"hookedCount"` // 有新鲜 live 文件的实例数(实时接入)
	Total       int `json:"total"`       // 在线实例总数
}

// GetBridgeStatus 返回桥接状态及当前接入比例。
func (s *MonitorService) GetBridgeStatus() (*BridgeInfo, error) {
	st := monitor.GetBridgeStatus()
	live, _, _ := monitor.Detect()
	hooked := 0
	for _, inst := range live {
		if inst.Live {
			hooked++
		}
	}
	return &BridgeInfo{BridgeStatus: st, HookedCount: hooked, Total: len(live)}, nil
}

// EnableBridge 启用桥接:保存设置并写入 ~/.claude/settings.json。
func (s *MonitorService) EnableBridge() error {
	cfg := monitor.GetSettings()
	cfg.BridgeEnabled = true
	if err := monitor.SaveSettings(cfg); err != nil {
		return err
	}
	_, err := monitor.EnsureBridge()
	return err
}

// DisableBridge 禁用桥接:还原 settings.json 并保存设置。
func (s *MonitorService) DisableBridge() error {
	cfg := monitor.GetSettings()
	cfg.BridgeEnabled = false
	if err := monitor.SaveSettings(cfg); err != nil {
		return err
	}
	return monitor.DisableBridge()
}

// ---- 设置 ----

// SettingsResult 返回给前端的设置数据。
type SettingsResult struct {
	CloseQuits bool   `json:"closeQuits"`
	AutoStart  bool   `json:"autoStart"`
	Version    string `json:"version"`
}

// Version 应用版本号。
const Version = "1.3.0"

// GetSettings 返回当前设置。
func (s *MonitorService) GetSettings() *SettingsResult {
	cfg := monitor.GetSettings()
	auto, _ := monitor.IsAutoStartEnabled()
	return &SettingsResult{
		CloseQuits: cfg.CloseQuits,
		AutoStart:  auto,
		Version:    Version,
	}
}

// SaveSettings 保存设置并同步开机自启状态。
func (s *MonitorService) SaveSettings(closeQuits bool, autoStart bool) error {
	cfg := monitor.GetSettings()
	cfg.CloseQuits = closeQuits
	cfg.AutoStart = autoStart
	if err := monitor.SetAutoStart(autoStart); err != nil {
		return err
	}
	return monitor.SaveSettings(cfg)
}

// ShouldQuitOnClose 返回关闭按钮是否应直接退出。
func (s *MonitorService) ShouldQuitOnClose() bool {
	return monitor.IsCloseQuit()
}

// OpenURL 在系统默认浏览器中打开 URL。
func (s *MonitorService) OpenURL(url string) error {
	return monitor.OpenInBrowser(url)
}

// CheckUpdate 检查 GitHub 最新版本。
// 返回 (info, nil) 表示有新版本可用；
// 返回 (nil, nil) 表示已是最新；
// 返回 (nil, error) 表示检查失败（网络/API 错误）。
func (s *MonitorService) CheckUpdate() (*monitor.ReleaseInfo, error) {
	info, err := monitor.CheckLatestRelease("pie-tk", "claude-code-monitor", monitor.GitHubToken())
	if err != nil {
		return nil, err
	}
	if info == nil || !monitor.IsNewer(info.Version, Version) {
		return nil, nil
	}
	return info, nil
}

// DownloadUpdate 下载并应用更新。异步执行，通过 Events 推送进度，立即返回。
func (s *MonitorService) DownloadUpdate(url string) error {
	go func() {
		onProgress := func(downloaded, total int64) {
			pct := 0
			if total > 0 {
				pct = int(downloaded * 100 / total)
			}
			s.window.EmitEvent("update:progress", map[string]any{
				"status":     "downloading",
				"downloaded": downloaded,
				"total":      total,
				"percent":    pct,
			})
		}
		if err := monitor.DownloadAndReplace(url, onProgress); err != nil {
			s.window.EmitEvent("update:progress", map[string]any{
				"status":  "error",
				"message": err.Error(),
			})
		}
	}()
	return nil
}
