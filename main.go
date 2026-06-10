package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
	"time"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

//go:embed icon.ico
var iconBytes []byte

var wantExit bool // 由"退出"菜单置位，放行真正的关闭

// singletonHandle 保持互斥体句柄存活，防止被 GC 或进程退出前释放。
var singletonHandle uintptr

func main() {
	runtime.LockOSThread()
	loadConfig()
	initTheme()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--list", "-l", "list":
			runList()
			return
		case "-h", "--help", "help":
			fmt.Println("claude-monitor              启动 GUI 监控窗口（系统托盘常驻，每 1 秒刷新）")
			fmt.Println("claude-monitor --list       以命令行表格形式打印一次后退出")
			return
		}
	}

	// 单实例限制：已有实例则拉起其窗口后退出
	if !acquireSingleton() {
		activateExistingWindow()
		return
	}

	if err := runGUI(); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
}

// acquireSingleton 尝试创建全局命名互斥体。
// 返回 true 表示是第一个实例，false 表示已有实例在运行。
func acquireSingleton() bool {
	name, _ := syscall.UTF16PtrFromString("Global\\claude-monitor")
	r, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if r == 0 {
		return true // 创建失败，放行
	}
	singletonHandle = r
	return !(lastErr != nil && lastErr == syscall.Errno(183)) // ERROR_ALREADY_EXISTS
}

// activateExistingWindow 找到已运行实例的窗口并拉到前台。
func activateExistingWindow() {
	title, _ := syscall.UTF16PtrFromString("Claude Code 监控")
	r, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	hwnd := win.HWND(r)
	if hwnd == 0 {
		return
	}
	win.ShowWindow(hwnd, win.SW_SHOW)
	win.ShowWindow(hwnd, win.SW_RESTORE)
	win.SetForegroundWindow(hwnd)
}

func runList() {
	live, stale, err := Detect()
	if err != nil {
		fmt.Fprintln(os.Stderr, "检测出错:", err)
		os.Exit(1)
	}

	head := fmt.Sprintf("在线 Claude Code 实例: %d", len(live))
	if n := countStatus(live, "busy") + countStatus(live, "idle"); n > 0 {
		head += fmt.Sprintf("   (● 忙碌 %d   ○ 空闲 %d)", countStatus(live, "busy"), countStatus(live, "idle"))
	}
	fmt.Println(head)
	fmt.Println()
	fmt.Printf("%-7s  %-8s  %-12s  %-16s  %-8s  %-26s  %s\n",
		"PID", "状态", "模型", "Context", "本轮", "对话主题", "项目 (工作目录)")
	fmt.Println("------------------------------------------------------------------------------------------------------------------------")
	for _, it := range live {
		cwd := it.Cwd
		if cwd == "" {
			cwd = "(无 session 记录)"
		}
		if len([]rune(cwd)) > 34 {
			r := []rune(cwd)
			cwd = "..." + string(r[len(r)-31:])
		}
		topic := topicDisplay(it)
		if len([]rune(topic)) > 26 {
			topic = truncateRunes(topic, 25)
		}
		fmt.Printf("%-7d  %-8s  %-12s  %-16s  %-8s  %-26s  %s\n",
			it.Pid, statusText(it.Status), modelDisplay(it),
			contextDisplayPlain(it), outputDisplay(it), topic, cwd)
	}
	fmt.Println()
	fmt.Printf("合计 Context: %s", formatTokens(totalContext(live)))
	if len(stale) > 0 {
		fmt.Printf("    另有 %d 个残留会话（进程已退出）", len(stale))
	}
	fmt.Println()
}

// ---- GUI ----

func runGUI() error {
	initCardFonts()

	var (
		mainWin       *walk.MainWindow
		scroll        *walk.ScrollView
		scrollContent *walk.Composite

		titleEmoji *walk.Label
		titleText  *walk.Label
		subTitle   *walk.Label

		statsLabel *walk.Label
		timeLabel  *walk.Label

		footLabel *walk.Label
		footHint  *walk.Label
	)

	bgWindow := theme.WindowBG
	bgPanel := theme.PanelBG

	mw := d.MainWindow{
		AssignTo:   &mainWin,
		Title:      "Claude Code 监控",
		Size:       d.Size{Width: 1040, Height: 680},
		MinSize:    d.Size{Width: 660, Height: 420},
		Font:       d.Font{Family: "Segoe UI Variable", PointSize: 9},
		Background: d.SolidColorBrush{Color: bgWindow},
		Layout:     d.VBox{MarginsZero: true, Spacing: 0},
		Children: []d.Widget{
			// ============ 顶部 Hero（透明沉浸） ============
			d.Composite{
				Background: d.SolidColorBrush{Color: bgWindow},
				Layout: d.VBox{
					Margins: d.Margins{Left: 22, Top: 14, Right: 22, Bottom: 16},
					Spacing: 6,
				},
				Children: []d.Widget{
							// 标题行
							d.Composite{
								Background: d.SolidColorBrush{Color: bgWindow},
								Layout:     d.HBox{MarginsZero: true, Spacing: 12},
								Children: []d.Widget{
									d.Label{
										AssignTo:   &titleEmoji,
										Text:       "\U0001F4CA",
										Background: d.SolidColorBrush{Color: bgWindow},
										Font:       d.Font{Family: "Segoe UI Emoji", PointSize: 18},
									},
									d.Label{
										AssignTo:   &titleText,
										Text:       "Claude Code 监控",
										TextColor:  theme.WindowText,
										Background: d.SolidColorBrush{Color: bgWindow},
										Font:       d.Font{Family: "Segoe UI Variable", PointSize: 17, Bold: true},
									},
								},
							},
							d.Label{
								AssignTo:   &subTitle,
								Text:       "实时监控本机运行中的所有 Claude Code 实例",
								TextColor:  theme.SecondaryText,
								Background: d.SolidColorBrush{Color: bgWindow},
								Font:       d.Font{Family: "Segoe UI Variable", PointSize: 10},
							},
							// 间距
							d.Composite{
								Background: d.SolidColorBrush{Color: bgWindow},
								MinSize:    d.Size{Height: 8},
								MaxSize:    d.Size{Height: 8},
								Layout:     d.HBox{MarginsZero: true},
							},
							// stats 行
							d.Composite{
								Background: d.SolidColorBrush{Color: bgWindow},
								Layout:     d.HBox{MarginsZero: true, Spacing: 6},
								Children: []d.Widget{
									d.Label{
										AssignTo:   &statsLabel,
										Text:       "正在检测...",
										TextColor:  theme.SecondaryText,
										Background: d.SolidColorBrush{Color: bgWindow},
										Font:       d.Font{Family: "Segoe UI Variable", PointSize: 10},
									},
									d.HSpacer{},
									d.Label{
										AssignTo:   &timeLabel,
										Text:       "",
										TextColor:  theme.SubtleText,
										Background: d.SolidColorBrush{Color: bgWindow},
										Font:       d.Font{Family: "Consolas", PointSize: 9},
									},
								},
					},
				},
			},
			// ============ 卡片列表 ============
			d.ScrollView{
				AssignTo:        &scroll,
				HorizontalFixed: true,
				Background:      d.SolidColorBrush{Color: bgWindow},
				Layout: d.VBox{
					Margins: d.Margins{Left: 14, Top: 16, Right: 14, Bottom: 18},
					Spacing: 10,
				},
				Children: []d.Widget{
					d.Composite{
						AssignTo:   &scrollContent,
						Background: d.SolidColorBrush{Color: bgWindow},
						Layout: d.VBox{
							MarginsZero: true,
							Spacing:     10,
						},
					},
				},
			},
			// 分隔线
			d.Composite{
				MinSize:    d.Size{Height: 1},
				MaxSize:    d.Size{Height: 1},
				Background: d.SolidColorBrush{Color: theme.Divider},
				Layout:     d.HBox{MarginsZero: true},
			},
			// ============ 底部 footer ============
			d.Composite{
				Background: d.SolidColorBrush{Color: bgPanel},
				Layout: d.HBox{
					Margins: d.Margins{Left: 22, Top: 10, Right: 22, Bottom: 10},
					Spacing: 8,
				},
				Children: []d.Widget{
					d.Label{
						AssignTo:   &footLabel,
						Text:       "正在初始化...",
						TextColor:  theme.SubtleText,
						Background: d.SolidColorBrush{Color: bgPanel},
						Font:       d.Font{Family: "Segoe UI Variable", PointSize: 9},
					},
					d.HSpacer{},
					d.Label{
						AssignTo:   &footHint,
						Text:       "\U0001F4A1  卡片右侧：清空 · 对话 · 回溯 · 窗口",
						TextColor:  theme.SubtleText,
						Background: d.SolidColorBrush{Color: bgPanel},
						Font:       d.Font{Family: "Segoe UI Variable", PointSize: 9},
					},
				},
			},
		},
	}

	if err := mw.Create(); err != nil {
		return err
	}

	// ---- 暗色标题栏 ----
	enableDarkTitleBar(mainWin.Handle())

	// ---- 隐藏滚动条（仅隐藏一次，不干扰滚动功能）----
	if scroll != nil {
		scrollViewHWND = scroll.Handle()
		hideScrollBars(scrollViewHWND)
	}

	// 卡片容器
	cardListContainer = scrollContent

	// 空状态
	emptyState = buildEmptyState(cardListContainer)
	emptyState.SetVisible(false)

	// 全局引用
	gMW = mainWin
	gFoot = footLabel

	// ---- 系统托盘 ----
	ni, err := walk.NewNotifyIcon(mainWin)
	if err != nil {
		return err
	}
	defer ni.Dispose()
	if ic := loadTrayIcon(); ic != nil {
		_ = ni.SetIcon(ic)
	}
	_ = ni.SetToolTip("Claude Code 监控")
	_ = ni.SetVisible(true)

	showWin := func() {
		mainWin.Show()
		mainWin.SetVisible(true)
		hwnd := mainWin.Handle()
		win.ShowWindow(hwnd, win.SW_RESTORE)
		win.SetForegroundWindow(hwnd)
	}

	acts := ni.ContextMenu().Actions()
	aShow := walk.NewAction()
	_ = aShow.SetText("显示窗口")
	aShow.Triggered().Attach(showWin)
	acts.Add(aShow)
	acts.Add(walk.NewSeparatorAction())
	aExit := walk.NewAction()
	_ = aExit.SetText("退出")
	aExit.Triggered().Attach(func() {
		wantExit = true
		mainWin.Close()
	})
	acts.Add(aExit)

	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			showWin()
		}
	})

	mainWin.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !wantExit {
			*canceled = true
			mainWin.Hide()
		}
	})

	// ---- 数据刷新 ----
	refresh := func() {
		live, stale, derr := Detect()
		if derr != nil {
			_ = footLabel.SetText("检测出错: " + derr.Error())
			footLabel.SetTextColor(theme.Danger)
			return
		}
		now := time.Now()

		busy := countStatus(live, "busy")
		idle := countStatus(live, "idle")
		total := totalContext(live)

		_ = statsLabel.SetText(buildStatsLine(len(live), busy, idle, total, len(stale)))
		_ = timeLabel.SetText("⏱  " + now.Format("15:04:05"))

		syncCards(live, now)

		clearFootIfStale()
		if msg, prog := footMessage(); msg != "" {
			c := blendColor(theme.WindowText, theme.SubtleText, prog)
			_ = footLabel.SetText(msg)
			footLabel.SetTextColor(c)
		} else {
			if len(live) == 0 {
				_ = footLabel.SetText("待机中 · 没有运行中的实例")
			} else {
				_ = footLabel.SetText(fmt.Sprintf("正在监控 %d 个实例 · 每 1 秒刷新", len(live)))
			}
			footLabel.SetTextColor(theme.SubtleText)
		}

		_ = ni.SetToolTip(fmt.Sprintf("Claude Code 监控\n在线 %d (\U0001F534 忙碌 %d · \U0001F7E2 空闲 %d)",
			len(live), busy, idle))
	}

	refresh()

	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for range t.C {
			mainWin.Synchronize(refresh)
		}
	}()

	startAnimationLoop(mainWin, nil, func() {
		refreshCardPulse()
		if msg, prog := footMessage(); msg != "" {
			c := blendColor(theme.WindowText, theme.SubtleText, prog)
			_ = footLabel.SetText(msg)
			footLabel.SetTextColor(c)
		}
	})

	mainWin.Show()
	mainWin.Run()
	return nil
}

// ============ 构建辅助 ============

func buildStatsLine(online, busy, idle int, totalCtx int64, stale int) string {
	if online == 0 {
		return "\U0001F319  当前无实例运行"
	}
	parts := []string{
		fmt.Sprintf("在线 %d", online),
		fmt.Sprintf("\U0001F534 %d 忙碌", busy),
		fmt.Sprintf("\U0001F7E2 %d 空闲", idle),
	}
	if totalCtx > 0 {
		parts = append(parts, fmt.Sprintf("\U0001F4E6 %s context", formatTokensCompact(totalCtx)))
	}
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("\U0001F312 %d 残留", stale))
	}
	return joinWithDot(parts)
}

func loadTrayIcon() *walk.Icon {
	if ic, err := walk.NewIconFromResourceId(1); err == nil {
		return ic
	}
	tmp := filepath.Join(os.TempDir(), "claude-monitor-icon.ico")
	if os.WriteFile(tmp, iconBytes, 0644) == nil {
		if ic, err := walk.NewIconFromFile(tmp); err == nil {
			return ic
		}
	}
	return nil
}

// ============ 隐藏 ScrollView 滚动条 ============

var scrollViewHWND win.HWND

func hideScrollBars(hwnd win.HWND) {
	if hwnd == 0 {
		return
	}
	procShowScrollBar.Call(uintptr(hwnd), uintptr(win.SB_VERT), 0)
	procShowScrollBar.Call(uintptr(hwnd), uintptr(win.SB_HORZ), 0)
}
