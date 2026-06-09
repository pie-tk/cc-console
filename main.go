package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

//go:embed icon.ico
var iconBytes []byte

var wantExit bool // 由“退出”菜单置位，放行真正的关闭

func main() {
	runtime.LockOSThread()
	loadConfig()

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

	if err := runGUI(); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
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
			contextDisplay(it), outputDisplay(it), topic, cwd)
	}
	fmt.Println()
	fmt.Printf("合计 Context: %s", formatTokens(totalContext(live)))
	if len(stale) > 0 {
		fmt.Printf("    另有 %d 个残留会话（进程已退出）", len(stale))
	}
	fmt.Println()
}

// ---- 表格数据模型 ----

type InstanceModel struct {
	walk.TableModelBase
	items []Instance
	now   time.Time
}

func (m *InstanceModel) RowCount() int { return len(m.items) }

func (m *InstanceModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.items) {
		return nil
	}
	it := m.items[row]
	switch col {
	case 0:
		return it.Pid
	case 1:
		return statusText(it.Status)
	case 2:
		return modelDisplay(it)
	case 3:
		return contextDisplay(it)
	case 4:
		return outputDisplay(it)
	case 5:
		return topicDisplay(it)
	case 6:
		return humanDuration(it.StartedAt, m.now)
	case 7:
		if it.Cwd == "" {
			return "(无 session 记录)"
		}
		return it.Cwd
	}
	return nil
}

// ---- GUI ----

func runGUI() error {
	var (
		mainWin     *walk.MainWindow
		statusLabel *walk.Label
		footLabel   *walk.Label
		tv          *walk.TableView
	)
	model := new(InstanceModel)

	mw := MainWindow{
		AssignTo: &mainWin,
		Title:   "Claude Code 实例监控",
		Size:    Size{Width: 1140, Height: 400},
		MinSize: Size{Width: 680, Height: 260},
		Layout:  VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 8},
		Children: []Widget{
			Label{AssignTo: &statusLabel, Text: "正在检测…",
				Font: Font{Family: "Microsoft YaHei", PointSize: 13, Bold: true}},
			TableView{
				AssignTo: &tv,
				Columns: []TableViewColumn{
					{Title: "PID", Width: 62},
					{Title: "状态", Width: 72},
					{Title: "模型", Width: 92},
					{Title: "Context", Width: 120},
					{Title: "本轮", Width: 70},
					{Title: "对话主题", Width: 230},
					{Title: "启动时长", Width: 92},
					{Title: "项目 / 工作目录", Width: 240},
				},
				Model:               model,
				LastColumnStretched: true,
			},
			Label{AssignTo: &footLabel, Text: "每 1 秒刷新",
				TextColor: walk.RGB(128, 128, 128)},
		},
	}

	if err := mw.Create(); err != nil {
		return err
	}

	// ---- 系统托盘 ----
	ni, err := walk.NewNotifyIcon(mainWin)
	if err != nil {
		return err
	}
	defer ni.Dispose()
	if ic := loadTrayIcon(); ic != nil {
		_ = ni.SetIcon(ic)
	}
	_ = ni.SetToolTip("Claude Code 实例监控")
	_ = ni.SetVisible(true)

	showWin := func() {
		mainWin.Show()
		mainWin.SetVisible(true)
		hwnd := mainWin.Handle()
		win.ShowWindow(hwnd, win.SW_RESTORE)
		win.SetForegroundWindow(hwnd)
	}

	// 右键菜单
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

	// 左键单击托盘 → 显示窗口
	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			showWin()
		}
	})

	// 关窗按钮 → 最小化到托盘（而非退出）
	mainWin.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !wantExit {
			*canceled = true
			mainWin.Hide()
		}
	})

	// ---- 刷新 ----
	refresh := func() {
		live, stale, derr := Detect()
		if derr != nil {
			footLabel.SetText("检测出错: " + derr.Error())
			return
		}
		now := time.Now()
		model.items = live
		model.now = now

		busy := countStatus(live, "busy")
		idle := countStatus(live, "idle")
		head := fmt.Sprintf("在线实例: %d     ● 忙碌 %d    ○ 空闲 %d     合计 Context %s", len(live), busy, idle, formatTokens(totalContext(live)))
		if len(stale) > 0 {
			head += fmt.Sprintf("     (残留 %d)", len(stale))
		}
		statusLabel.SetText(head)
		footLabel.SetText(fmt.Sprintf("每 1 秒刷新    更新于 %s", now.Format("15:04:05")))
		model.PublishRowsReset()

		_ = ni.SetToolTip(fmt.Sprintf("Claude Code 实例监控\n在线 %d（●忙碌 %d · ○空闲 %d）", len(live), busy, idle))
	}

	refresh()

	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for range t.C {
			mainWin.Synchronize(refresh)
		}
	}()

	mainWin.Show()
	mainWin.Run()
	return nil
}

// loadTrayIcon 优先从嵌入资源（id=1）取图标，失败则把嵌入的 ico 字节写到临时文件再加载。
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
