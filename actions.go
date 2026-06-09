package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// 右键菜单项命令 ID。
const (
	cmdClear  = 1
	cmdPrompt = 2
	cmdRewind = 3
)

// 子类化与各动作共享的句柄。setupRowMenu 在主窗口创建后赋值。
var (
	gModel      *InstanceModel
	gTV         *walk.TableView
	gMW         *walk.MainWindow
	gFoot       *walk.Label
	origTVProc  uintptr
)

// setupRowMenu 给表格挂上右键菜单：清空 / 输入对话 / 回溯。
//
// Walk 的 TableView 行其实是两个未注册的 SysListView32 子窗口画的，
// SetContextMenu / tv.MouseDown 都收不到行上的右键。这里改用子类化表格窗口，
// 拦截 WM_NOTIFY 里的 NM_RCLICK（行的右键通知会发给父表格），命中行后弹出菜单。
func setupRowMenu(tv *walk.TableView, model *InstanceModel, mw *walk.MainWindow, foot *walk.Label) {
	gModel, gTV, gMW, gFoot = model, tv, mw, foot

	cb := syscall.NewCallback(tvSubclassProc)
	origTVProc = win.SetWindowLongPtr(tv.Handle(), win.GWLP_WNDPROC, cb)
}

func tvSubclassProc(hwnd win.HWND, msg uint32, wp, lp uintptr) uintptr {
	ret := win.CallWindowProc(origTVProc, hwnd, msg, wp, lp)
	if msg == win.WM_NOTIFY {
		nmh := (*win.NMHDR)(unsafe.Pointer(lp))
		if nmh != nil && nmh.Code == win.NM_RCLICK {
			showRowMenu()
		}
	}
	return ret
}

func showRowMenu() {
	if gTV == nil || gModel == nil {
		return
	}

	var cpt win.POINT
	win.GetCursorPos(&cpt) // 屏幕坐标（菜单定位用）
	client := cpt
	win.ScreenToClient(gTV.Handle(), &client)

	idx := gTV.IndexAt(int(client.X), int(client.Y))
	if idx < 0 || idx >= len(gModel.items) {
		return // 点在表头 / 空白：不弹菜单
	}
	_ = gTV.SetCurrentIndex(idx) // 高亮选中该行，让用户看清操作对象

	menu := win.CreatePopupMenu()
	if menu == 0 {
		return
	}
	defer win.DestroyMenu(menu)

	appendMenu(menu, win.MF_STRING, cmdClear, "清空会话    /clear")
	appendMenu(menu, win.MF_STRING, cmdPrompt, "输入对话…")
	appendMenu(menu, win.MF_STRING, cmdRewind, "回溯    (ESC ESC)")

	chosen := win.TrackPopupMenuEx(
		menu,
		win.TPM_RETURNCMD|win.TPM_LEFTALIGN|win.TPM_TOPALIGN|win.TPM_RIGHTBUTTON,
		cpt.X, cpt.Y,
		gMW.Handle(), nil,
	)

	switch chosen {
	case cmdClear:
		actClear(gModel.items[idx])
	case cmdPrompt:
		actPrompt(gModel.items[idx])
	case cmdRewind:
		actRewind(gModel.items[idx])
	}
}

func appendMenu(menu win.HMENU, flags uint32, id uintptr, text string) {
	var lp uintptr
	if text != "" {
		if w, err := windows.UTF16PtrFromString(text); err == nil {
			lp = uintptr(unsafe.Pointer(w))
		}
	}
	_, _, _ = procAppendMenuW.Call(uintptr(menu), uintptr(flags), id, lp)
}

// ---- 三个动作 ----

func actClear(it Instance) {
	if err := SendInputRecords(uint32(it.Pid), withEnter(textRecords("/clear"))); err != nil {
		msgBoxErr(fmt.Sprintf("清空失败（PID %d）", it.Pid), err)
		return
	}
	flashFoot(fmt.Sprintf("已向 PID %d 发送 /clear", it.Pid))
}

func actRewind(it Instance) {
	if err := SendInputRecords(uint32(it.Pid), escapeRecords()); err != nil {
		msgBoxErr(fmt.Sprintf("回溯失败（PID %d）", it.Pid), err)
		return
	}
	flashFoot(fmt.Sprintf("已向 PID %d 发送 ESC×2（回溯）", it.Pid))
}

func actPrompt(it Instance) {
	text, ok := runPromptDialog()
	if !ok || strings.TrimSpace(text) == "" {
		return
	}
	// 多行折叠为单行：换行在 TUI 里会触发提交；末尾再补一个回车。
	flat := strings.ReplaceAll(text, "\r\n", " ")
	flat = strings.ReplaceAll(flat, "\n", " ")
	if err := SendInputRecords(uint32(it.Pid), withEnter(textRecords(flat))); err != nil {
		msgBoxErr(fmt.Sprintf("发送失败（PID %d）", it.Pid), err)
		return
	}
	flashFoot(fmt.Sprintf("已向 PID %d 发送：%s", it.Pid, truncateForFoot(flat)))
}

// ---- 输入对话框 ----

func runPromptDialog() (string, bool) {
	var (
		dlg       *walk.Dialog
		te        *walk.TextEdit
		okBtn     *walk.PushButton
		cancelBtn *walk.PushButton
		accept    bool
		result    string // 关闭前就读取，Close 后窗口可能已销毁
	)

	builder := d.Dialog{
		AssignTo: &dlg,
		Title:    "向 Claude Code 实例发送对话",
		Size:     d.Size{Width: 520, Height: 280},
		MinSize:  d.Size{Width: 360, Height: 200},
		Layout:   d.VBox{Margins: d.Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 8},
		Children: []d.Widget{
			d.Label{Text: "输入要发送到该实例的文本（确定后自动追加回车提交）。建议单行；换行会被转为空格。"},
			d.TextEdit{AssignTo: &te, VScroll: true, MinSize: d.Size{Height: 120}},
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.HSpacer{},
					d.PushButton{AssignTo: &okBtn, Text: "确定",
						OnClicked: func() {
							if te != nil {
								result = te.Text()
							}
							accept = true
							dlg.Close(walk.DlgCmdOK)
						}},
					d.PushButton{AssignTo: &cancelBtn, Text: "取消",
						OnClicked: func() { dlg.Close(walk.DlgCmdCancel) }},
				},
			},
		},
		DefaultButton: &okBtn,
		CancelButton:  &cancelBtn,
	}

	if err := builder.Create(gMW); err != nil {
		return "", false
	}
	dlg.Run() // 阻塞，直到点 确定/取消 或按 Esc
	if !accept {
		return "", false
	}
	return result, true
}

// ---- 反馈 ----

func flashFoot(s string) {
	if gFoot != nil {
		_ = gFoot.SetText(s)
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
