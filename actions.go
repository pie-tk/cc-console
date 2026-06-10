package main

import (
	"fmt"
	"strings"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// 全局句柄（卡片操作 / 错误提示 / 反馈消息要用到）。
var (
	gMW   *walk.MainWindow
	gFoot *walk.Label
)

// ---- 三个动作 ----

func actClear(pid int) {
	result := walk.MsgBox(gMW,
		"确认清空会话",
		fmt.Sprintf("确定要清空 PID %d 的会话吗？\n此操作将清除当前对话内容。", pid),
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion|walk.MsgBoxDefButton2)
	if result != walk.DlgCmdYes {
		return
	}
	if err := SendInputRecords(uint32(pid), withEnter(textRecords("/clear"))); err != nil {
		msgBoxErr(fmt.Sprintf("清空失败（PID %d）", pid), err)
		return
	}
	flashFoot(fmt.Sprintf("✓  已向 PID %d 发送 /clear", pid))
}

func actRewind(pid int) {
	if err := SendInputRecords(uint32(pid), escapeRecords()); err != nil {
		msgBoxErr(fmt.Sprintf("回溯失败（PID %d）", pid), err)
		return
	}
	flashFoot(fmt.Sprintf("↺  已向 PID %d 发送 ESC×2（回溯）", pid))
}

func actPrompt(pid int) {
	text, ok := runPromptDialog()
	if !ok || strings.TrimSpace(text) == "" {
		return
	}
	flat := strings.ReplaceAll(text, "\r\n", " ")
	flat = strings.ReplaceAll(flat, "\n", " ")
	if err := SendInputRecords(uint32(pid), withEnter(textRecords(flat))); err != nil {
		msgBoxErr(fmt.Sprintf("发送失败（PID %d）", pid), err)
		return
	}
	flashFoot(fmt.Sprintf("✓  已向 PID %d 发送：%s", pid, truncateForFoot(flat)))
}

// actShowWin 找到目标实例所在的终端窗口并把它提到最前面。
//
// 原理：AttachConsole(pid) → GetConsoleWindow() → GetAncestor(GA_ROOTOWNER)
// 向上取到真正可见的顶层窗口（兼容传统 conhost 和 Windows Terminal / conpty），
// 再用 ShowWindow + SetForegroundWindow 置前。
func actShowWin(pid int) {
	_, _, _ = procFreeConsole.Call()

	r, _, _ := procAttachConsole.Call(uintptr(pid))
	if r == 0 {
		msgBoxErr(fmt.Sprintf("无法找到窗口（PID %d）", pid),
			fmt.Errorf("无法附加到该实例的控制台\n请确认它在普通终端窗口里运行"))
		return
	}
	defer func() { _, _, _ = procFreeConsole.Call() }()

	r, _, _ = procGetConsoleWindow.Call()
	hwnd := win.HWND(r)
	if hwnd == 0 {
		msgBoxErr(fmt.Sprintf("无法找到窗口（PID %d）", pid),
			fmt.Errorf("未找到控制台窗口"))
		return
	}

	// 向上取根属主窗口（兼容 conpty / Windows Terminal）
	if ro, _, _ := procGetAncestor.Call(uintptr(hwnd), 3); ro != 0 {
		hwnd = win.HWND(ro)
	}

	win.ShowWindow(hwnd, win.SW_RESTORE)
	win.SetForegroundWindow(hwnd)

	flashFoot(fmt.Sprintf("🪟  已将 PID %d 的窗口置前", pid))
}

// ---- 输入对话框 ----
//
// Notion 风格：标题区暖白底 + 副标 + 大留白；按钮区柔和分隔。
func runPromptDialog() (string, bool) {
	var (
		dlg       *walk.Dialog
		te        *walk.TextEdit
		okBtn     *walk.PushButton
		cancelBtn *walk.PushButton
		accept    bool
		result    string
	)

	bgWindow := theme.WindowBG
	bgPanel := theme.PanelBG

	builder := d.Dialog{
		AssignTo:   &dlg,
		Title:      "发送对话",
		Size:       d.Size{Width: 580, Height: 340},
		MinSize:    d.Size{Width: 460, Height: 260},
		Font:       d.Font{Family: "Segoe UI Variable", PointSize: 9},
		Background: d.SolidColorBrush{Color: bgWindow},
		Layout:     d.VBox{MarginsZero: true, Spacing: 0},
		Children: []d.Widget{
			// 标题区
			d.Composite{
				Background: d.SolidColorBrush{Color: bgPanel},
				Layout: d.VBox{
					Margins: d.Margins{Left: 28, Top: 22, Right: 28, Bottom: 18},
					Spacing: 6,
				},
				Children: []d.Widget{
					d.Composite{
						Background: d.SolidColorBrush{Color: bgPanel},
						Layout:     d.HBox{MarginsZero: true, Spacing: 10},
						Children: []d.Widget{
							d.Label{
								Text:       "✏️",
								Background: d.SolidColorBrush{Color: bgPanel},
								Font:       d.Font{Family: "Segoe UI Emoji", PointSize: 14},
							},
							d.Label{
								Text:       "发送对话到该实例",
								TextColor:  theme.WindowText,
								Background: d.SolidColorBrush{Color: bgPanel},
								Font:       d.Font{Family: "Segoe UI Variable", PointSize: 13, Bold: true},
							},
						},
					},
					d.Label{
						Text:       "输入文字后点击 发送 ⏎ 或按 Enter。多行会被折叠为空格。",
						TextColor:  theme.SecondaryText,
						Background: d.SolidColorBrush{Color: bgPanel},
						Font:       d.Font{Family: "Segoe UI Variable", PointSize: 9},
					},
				},
			},
			// 柔和分隔线
			d.Composite{
				MinSize:    d.Size{Height: 1},
				MaxSize:    d.Size{Height: 1},
				Background: d.SolidColorBrush{Color: theme.Divider},
				Layout:     d.HBox{MarginsZero: true},
			},
			// 输入区
			d.Composite{
				Background: d.SolidColorBrush{Color: bgWindow},
				Layout: d.VBox{
					Margins: d.Margins{Left: 28, Top: 18, Right: 28, Bottom: 0},
					Spacing: 8,
				},
				Children: []d.Widget{
					d.TextEdit{
						AssignTo: &te,
						VScroll:  true,
						MinSize:  d.Size{Height: 130},
					},
				},
			},
			// 按钮区
			d.Composite{
				Background: d.SolidColorBrush{Color: bgWindow},
				Layout: d.HBox{
					Margins: d.Margins{Left: 28, Top: 14, Right: 28, Bottom: 18},
					Spacing: 8,
				},
				Children: []d.Widget{
					d.HSpacer{},
					d.PushButton{
						AssignTo:  &cancelBtn,
						Text:      "取消",
						MinSize:   d.Size{Width: 96, Height: 34},
						OnClicked: func() { dlg.Close(walk.DlgCmdCancel) },
					},
					d.PushButton{
						AssignTo: &okBtn,
						Text:     "发送  ⏎",
						MinSize:  d.Size{Width: 120, Height: 34},
						OnClicked: func() {
							if te != nil {
								result = te.Text()
							}
							accept = true
							dlg.Close(walk.DlgCmdOK)
						},
					},
				},
			},
		},
		DefaultButton: &okBtn,
		CancelButton:  &cancelBtn,
	}

	if err := builder.Create(gMW); err != nil {
		return "", false
	}

	enableDarkTitleBar(dlg.Handle())
	if isDark && te != nil {
		te.SetTextColor(theme.WindowText)
		if bg, err := walk.NewSolidColorBrush(theme.CardBG); err == nil {
			te.SetBackground(bg)
		}
	}

	dlg.Run()
	if !accept {
		return "", false
	}
	return result, true
}

// ---- 反馈 ----

// flashFoot 在底部状态条短暂显示一条消息（带 TTL，自动淡出）。
func flashFoot(s string) {
	setFootMessage(s)
	if gFoot != nil {
		_ = gFoot.SetText(s)
		gFoot.SetTextColor(theme.WindowText)
	}
}

func msgBoxErr(title string, err error) {
	if gMW != nil {
		walk.MsgBox(gMW, title, err.Error(), walk.MsgBoxIconError)
	}
}

func truncateForFoot(s string) string {
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}
