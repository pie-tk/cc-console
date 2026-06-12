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
	return &DetectResult{
		Live:  live,
		Stale: stale,
		Stats: monitor.StatsInfo{
			Online:  len(live),
			Busy:    monitor.CountStatus(live, "busy"),
			Idle:    monitor.CountStatus(live, "idle"),
			Stale:   len(stale),
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

// ---- 设置 ----

// SettingsResult 返回给前端的设置数据。
type SettingsResult struct {
	CloseQuits bool   `json:"closeQuits"`
	AutoStart  bool   `json:"autoStart"`
	Version    string `json:"version"`
}

// Version 应用版本号。
const Version = "1.2.0"

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

// DownloadUpdate 下载并应用更新。成功后本进程会退出。
func (s *MonitorService) DownloadUpdate(url string) error {
	return monitor.DownloadAndReplace(url)
}
