package main

import (
	"embed"
	"fmt"
	"time"

	"claude-monitor/internal/monitor"
	"claude-monitor/service"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

// runWailsApp 启动 Wails GUI 应用。
func runWailsApp() {
	svc := service.NewMonitorService()

	app := application.New(application.Options{
		Name:        "Claude Code 监控",
		Description: "实时监控本机运行中的所有 Claude Code 实例",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "claude-monitor-ebc9d7a2",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				// 第二个实例启动时，显示已有窗口
				win := svc.GetWindow()
				if win != nil {
					win.Show()
					win.Focus()
				}
			},
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true, // 关闭窗口不退出，托盘控制退出
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Claude Code 监控",
		Width:            1040,
		Height:           680,
		MinWidth:         660,
		MinHeight:        420,
		BackgroundColour: application.NewRGB(255, 255, 255),
		URL:              "/",
	})

	svc.SetApp(app, win)

	// ---- 系统托盘 ----
	tray := app.SystemTray.New()
	tray.SetIcon(iconBytes)
	tray.SetTooltip("Claude Code 监控")

	menu := app.NewMenu()
	menu.Add("显示窗口").OnClick(func(ctx *application.Context) {
		win.Show()
		win.Focus()
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(ctx *application.Context) {
		app.Quit()
	})
	tray.SetMenu(menu)

	// ---- 关闭窗口 → 隐藏到托盘 ----
	win.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		win.Hide()
		event.Cancel()
	})

	// ---- 定时更新托盘 tooltip ----
	go func() {
		for {
			live, stale, _ := monitor.Detect()
			busy := monitor.CountStatus(live, "busy")
			idle := monitor.CountStatus(live, "idle")
			tooltip := fmt.Sprintf("Claude Code 监控\n在线 %d (🔴 忙碌 %d · 🟢 空闲 %d)", len(live), busy, idle)
			if len(stale) > 0 {
				tooltip += fmt.Sprintf("\n残留 %d", len(stale))
			}
			tray.SetTooltip(tooltip)
			time.Sleep(2 * time.Second)
		}
	}()

	err := app.Run()
	if err != nil {
		fmt.Println("应用运行出错:", err)
	}
}
