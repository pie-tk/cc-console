package main

import (
	"fmt"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// Notion 风格的实例卡片块。
//
// 视觉结构：
//   ┌─ 1px 柔和边框 ────────────────────────────────────────┐
//   │  ⋮⋮  🟠  PID 12345     ·  忙碌  ·  claude-opus-4-8  ⏱ 2h 28m │
//   │      📁  E:/test/build/monitor                            │
//   │      💬  实现 UI 优化和动效                                 │
//   │      ━━━━━━━━━━━━━━━─────  74%   ·  148k / 200k    ✨ 本轮 1.2k │
//   └──────────────────────────────────────────────────────┘
//
// 注意：walk 的 VBox 不支持子项重排，所以一旦 PID 集合或顺序变化，
// 就 Dispose 所有卡片后重建；否则只调用 update() 更新文本。
// 控件构造 < 50ms，体感无感。

type instanceCard struct {
	pid int

	root  *walk.Composite // 外层（边框层）
	inner *walk.Composite // 内层（CardBG + padding）

	// 行 1：emoji + 主信息
	emoji      *walk.Label
	pidLabel   *walk.Label
	statusText *walk.Label
	separator1 *walk.Label
	modelTag   *walk.Label
	duration   *walk.Label

	// 行 2/3：路径 + 主题
	cwdLabel   *walk.Label
	topicLabel *walk.Label

	// 行 4：进度条 + 输出量
	bar         *walk.Label
	contextSub  *walk.Label
	outputLabel *walk.Label

	// 操作按钮
	actionPanel *walk.Composite
	btnClear    *walk.PushButton
	btnPrompt   *walk.PushButton
	btnRewind   *walk.PushButton
	btnShowWin  *walk.PushButton
}

var (
	cardListContainer *walk.Composite // ScrollView 内的 VBox 容器
	activeCards       = map[int]*instanceCard{}
	cardOrder         []int

	emptyState *walk.Composite // 空状态占位

	// 字体缓存
	fontPID    *walk.Font
	fontStatus *walk.Font
	fontTopic  *walk.Font
	fontBar    *walk.Font
	fontSubtle *walk.Font
	fontEmoji  *walk.Font
	fontHandle *walk.Font
	fontHeader *walk.Font
	fontHint   *walk.Font
	fontAction *walk.Font
)

func initCardFonts() {
	fontPID, _ = walk.NewFont("Segoe UI Variable", 11, walk.FontBold)
	fontStatus, _ = walk.NewFont("Segoe UI Variable", 9, 0)
	fontTopic, _ = walk.NewFont("Segoe UI Variable", 10, 0)
	fontBar, _ = walk.NewFont("Consolas", 10, 0)
	fontSubtle, _ = walk.NewFont("Segoe UI Variable", 9, 0)
	fontEmoji, _ = walk.NewFont("Segoe UI Emoji", 11, 0)
	fontHandle, _ = walk.NewFont("Segoe UI", 11, 0)
	fontHeader, _ = walk.NewFont("Segoe UI Variable", 11, walk.FontBold)
	fontHint, _ = walk.NewFont("Segoe UI Variable", 10, 0)
	fontAction, _ = walk.NewFont("Segoe UI Variable", 9, 0)
}

// newCard 创建一张卡片并挂到 parent 下。所有控件的 background 都设为 CardBG，
// 让背景无缝；外层 root 设为 CardBorder 色，配合 1px margin 产生"边框"。
// 右侧内嵌操作按钮面板，替代原来的右键菜单。
func newCard(parent walk.Container, pid int) *instanceCard {
	c := &instanceCard{pid: pid}

	borderBrush, _ := walk.NewSolidColorBrush(theme.CardBorder)
	cardBrush, _ := walk.NewSolidColorBrush(theme.CardBG)
	actionBrush, _ := walk.NewSolidColorBrush(blendColor(theme.CardBG, theme.CardBorder, 0.4))

	// ---- 外层：边框 ----
	c.root, _ = walk.NewComposite(parent)
	c.root.SetBackground(borderBrush)
	rootLayout := walk.NewVBoxLayout()
	rootLayout.SetMargins(walk.Margins{HNear: 1, VNear: 1, HFar: 1, VFar: 1})
	rootLayout.SetSpacing(0)
	c.root.SetLayout(rootLayout)

	// ---- 内容行：左卡片内容 + 右操作面板 ----
	contentRow, _ := walk.NewComposite(c.root)
	contentRow.SetBackground(cardBrush)
	crLayout := walk.NewHBoxLayout()
	crLayout.SetMargins(walk.Margins{HNear: 0, VNear: 0, HFar: 0, VFar: 0})
	crLayout.SetSpacing(0)
	contentRow.SetLayout(crLayout)

	// ---- 内层：卡片本体 ----
	c.inner, _ = walk.NewComposite(contentRow)
	c.inner.SetBackground(cardBrush)
	innerLayout := walk.NewVBoxLayout()
	innerLayout.SetMargins(walk.Margins{HNear: 16, VNear: 8, HFar: 10, VFar: 8})
	innerLayout.SetSpacing(4)
	c.inner.SetLayout(innerLayout)

	// ---- 行 1：emoji + PID + 状态 + 模型 + spacer + 时长 ----
	row1, _ := walk.NewComposite(c.inner)
	row1.SetBackground(cardBrush)
	l1 := walk.NewHBoxLayout()
	l1.SetMargins(walk.Margins{HNear: 0, VNear: 0, HFar: 0, VFar: 0})
	l1.SetSpacing(10)
	row1.SetLayout(l1)

	c.emoji = mkLabel(row1, statusEmoji("unknown"), theme.WindowText, fontEmoji, cardBrush)
	c.pidLabel = mkLabel(row1, fmt.Sprintf("PID %d", pid), theme.WindowText, fontPID, cardBrush)
	c.statusText = mkLabel(row1, "·  未知", theme.SecondaryText, fontStatus, cardBrush)
	c.separator1 = mkLabel(row1, "·", theme.SubtleText, fontStatus, cardBrush)
	c.modelTag = mkLabel(row1, "—", theme.SecondaryText, fontStatus, cardBrush)
	_, _ = walk.NewHSpacer(row1)
	c.duration = mkLabel(row1, "⏱ —", theme.SubtleText, fontStatus, cardBrush)

	// ---- 行 2：📁 cwd ----
	row2, _ := walk.NewComposite(c.inner)
	row2.SetBackground(cardBrush)
	l2 := walk.NewHBoxLayout()
	l2.SetMargins(walk.Margins{HNear: 0, VNear: 0, HFar: 0, VFar: 0})
	l2.SetSpacing(6)
	row2.SetLayout(l2)
	c.cwdLabel = mkLabel(row2, "📁  —", theme.SecondaryText, fontStatus, cardBrush)
	_, _ = walk.NewHSpacer(row2)

	// ---- 行 3：💬 topic ----
	row3, _ := walk.NewComposite(c.inner)
	row3.SetBackground(cardBrush)
	l3 := walk.NewHBoxLayout()
	l3.SetMargins(walk.Margins{HNear: 0, VNear: 0, HFar: 0, VFar: 0})
	l3.SetSpacing(6)
	row3.SetLayout(l3)
	c.topicLabel = mkLabel(row3, "💬  —", theme.WindowText, fontTopic, cardBrush)
	_, _ = walk.NewHSpacer(row3)

	// ---- 行 4：进度条 + 详情 + spacer + ✨ 输出 ----
	row4, _ := walk.NewComposite(c.inner)
	row4.SetBackground(cardBrush)
	l4 := walk.NewHBoxLayout()
	l4.SetMargins(walk.Margins{HNear: 0, VNear: 4, HFar: 0, VFar: 0})
	l4.SetSpacing(10)
	row4.SetLayout(l4)
	c.bar = mkLabel(row4, "", theme.SubtleText, fontBar, cardBrush)
	c.contextSub = mkLabel(row4, "", theme.SubtleText, fontSubtle, cardBrush)
	_, _ = walk.NewHSpacer(row4)
	c.outputLabel = mkLabel(row4, "", theme.SubtleText, fontSubtle, cardBrush)

	// ---- 右侧操作面板（横向排列） ----
	c.actionPanel, _ = walk.NewComposite(contentRow)
	c.actionPanel.SetBackground(actionBrush)
	apLayout := walk.NewHBoxLayout()
	apLayout.SetMargins(walk.Margins{HNear: 8, VNear: 4, HFar: 8, VFar: 4})
	apLayout.SetSpacing(2)
	c.actionPanel.SetLayout(apLayout)

	cardPid := pid // capture for closures
	c.btnClear = mkActionBtn(c.actionPanel, "清空", func() { actClear(cardPid) })
	c.btnPrompt = mkActionBtn(c.actionPanel, "对话", func() { actPrompt(cardPid) })
	c.btnRewind = mkActionBtn(c.actionPanel, "回溯", func() { actRewind(cardPid) })
	c.btnShowWin = mkActionBtn(c.actionPanel, "窗口", func() { actShowWin(cardPid) })

	return c
}

// mkActionBtn 创建一个紧凑的操作按钮。
func mkActionBtn(parent walk.Container, text string, onClick func()) *walk.PushButton {
	btn, _ := walk.NewPushButton(parent)
	_ = btn.SetText(text)
	if fontAction != nil {
		btn.SetFont(fontAction)
	}
	_ = btn.SetMinMaxSize(walk.Size{Width: 40, Height: 24}, walk.Size{Width: 40, Height: 24})
	btn.Clicked().Attach(onClick)
	return btn
}

// mkLabel 是创建带样式 Label 的便捷封装。
func mkLabel(parent walk.Container, text string, color walk.Color, font *walk.Font, bg walk.Brush) *walk.Label {
	lbl, _ := walk.NewLabel(parent)
	_ = lbl.SetText(text)
	lbl.SetTextColor(color)
	if font != nil {
		lbl.SetFont(font)
	}
	if bg != nil {
		lbl.SetBackground(bg)
	}
	return lbl
}

// update 把卡片内容刷成 it 当前状态。
func (c *instanceCard) update(it Instance, now time.Time) {
	c.pid = it.Pid

	_ = c.emoji.SetText(statusEmoji(it.Status))
	_ = c.pidLabel.SetText(fmt.Sprintf("PID  %d", it.Pid))
	_ = c.statusText.SetText("·  " + statusLabelText(it.Status))
	// busy 状态文字色 → 由动画 tick 在 refreshCardPulse() 里更新

	if it.HasConversation && it.Model != "" {
		_ = c.modelTag.SetText(it.Model)
		c.modelTag.SetTextColor(theme.SecondaryText)
	} else {
		_ = c.modelTag.SetText("新会话")
		c.modelTag.SetTextColor(theme.SubtleText)
	}

	_ = c.duration.SetText("⏱  " + humanDuration(it.StartedAt, now))

	cwd := it.Cwd
	if cwd == "" {
		cwd = "无 session 记录"
		c.cwdLabel.SetTextColor(theme.SubtleText)
	} else {
		c.cwdLabel.SetTextColor(theme.SecondaryText)
	}
	_ = c.cwdLabel.SetText("📁  " + cwd)

	switch {
	case !it.HasConversation:
		_ = c.topicLabel.SetText("💬  新会话 · 暂无消息")
		c.topicLabel.SetTextColor(theme.SubtleText)
	case it.Topic == "":
		_ = c.topicLabel.SetText("💬  暂无主题")
		c.topicLabel.SetTextColor(theme.SubtleText)
	default:
		_ = c.topicLabel.SetText("💬  " + it.Topic)
		c.topicLabel.SetTextColor(theme.WindowText)
	}

	// 进度条
	switch {
	case it.HasConversation && it.ContextTokens > 0 && it.ContextLimit > 0:
		pct := it.ContextTokens * 100 / it.ContextLimit
		_ = c.bar.SetText(unicodeBar(int(pct), 22) + fmt.Sprintf("   %d%%", pct))
		c.bar.SetTextColor(contextBarColor(pct))
		_ = c.contextSub.SetText(fmt.Sprintf("·  %s / %s", compactK(it.ContextTokens), compactK(it.ContextLimit)))
	case it.HasConversation && it.ContextTokens > 0:
		_ = c.bar.SetText(compactK(it.ContextTokens) + " tokens")
		c.bar.SetTextColor(theme.SecondaryText)
		_ = c.contextSub.SetText("")
	default:
		// 空进度条 — 灰色
		_ = c.bar.SetText(unicodeBar(0, 22) + "   待用")
		c.bar.SetTextColor(theme.SubtleText)
		_ = c.contextSub.SetText("")
	}

	if it.HasConversation && it.OutputTokens > 0 {
		_ = c.outputLabel.SetText("✨  本轮 " + formatTokens(it.OutputTokens))
		c.outputLabel.SetTextColor(theme.SecondaryText)
	} else {
		_ = c.outputLabel.SetText("")
	}
}

// dispose 销毁卡片的整个 widget 树。
func (c *instanceCard) dispose() {
	if c.root != nil {
		c.root.Dispose()
		c.root = nil
	}
}

// syncCards 同步卡片列表与 items：
//   - PID 集合 / 顺序变化 → Dispose 全部 + 重建（VBox 不支持重排）
//   - 仅数据变化 → 只 update 文本
func syncCards(items []Instance, now time.Time) {
	if cardListContainer == nil {
		return
	}

	newOrder := make([]int, len(items))
	for i, it := range items {
		newOrder[i] = it.Pid
	}

	sameOrder := len(newOrder) == len(cardOrder)
	if sameOrder {
		for i := range newOrder {
			if newOrder[i] != cardOrder[i] {
				sameOrder = false
				break
			}
		}
	}

	if sameOrder {
		for _, it := range items {
			if c, ok := activeCards[it.Pid]; ok {
				c.update(it, now)
			}
		}
		return
	}

	// 顺序 / 集合变化：重建
	hwnd := cardListContainer.Handle()
	win.SendMessage(hwnd, win.WM_SETREDRAW, 0, 0)
	defer func() {
		win.SendMessage(hwnd, win.WM_SETREDRAW, 1, 0)
		win.RedrawWindow(hwnd, nil, 0, win.RDW_INVALIDATE|win.RDW_ERASE|win.RDW_ALLCHILDREN)
	}()

	for pid, c := range activeCards {
		c.dispose()
		delete(activeCards, pid)
	}

	for _, it := range items {
		card := newCard(cardListContainer, it.Pid)
		if card == nil {
			continue
		}
		card.update(it, now)
		activeCards[it.Pid] = card
	}
	cardOrder = newOrder

	updateEmptyState(len(items) == 0)
}

// refreshCardPulse 由动效 ticker 调用，让 busy 卡片的状态文字呼吸。
func refreshCardPulse() {
	for _, c := range activeCards {
		if c == nil || c.statusText == nil {
			continue
		}
		// 只有 busy 状态才脉冲
		if c.emoji != nil && c.emoji.Text() == statusEmoji("busy") {
			c.statusText.SetTextColor(statusColor("busy"))
		}
	}
}

// updateEmptyState 显示/隐藏空态。
func updateEmptyState(show bool) {
	if emptyState == nil {
		return
	}
	emptyState.SetVisible(show)
}

// buildEmptyState 创建一个垂直居中的"无实例"占位提示。
func buildEmptyState(parent walk.Container) *walk.Composite {
	bg, _ := walk.NewSolidColorBrush(theme.WindowBG)

	comp, _ := walk.NewComposite(parent)
	comp.SetBackground(bg)
	l := walk.NewVBoxLayout()
	l.SetMargins(walk.Margins{HNear: 24, VNear: 60, HFar: 24, VFar: 60})
	l.SetSpacing(6)
	comp.SetLayout(l)

	_, _ = walk.NewVSpacer(comp)

	icon, _ := walk.NewLabel(comp)
	_ = icon.SetText("🌙")
	icon.SetBackground(bg)
	if f, err := walk.NewFont("Segoe UI Emoji", 28, 0); err == nil {
		icon.SetFont(f)
	}
	icon.SetTextColor(theme.SubtleText)

	title, _ := walk.NewLabel(comp)
	_ = title.SetText("当前没有运行中的 Claude Code 实例")
	title.SetBackground(bg)
	if f, err := walk.NewFont("Segoe UI Variable", 12, walk.FontBold); err == nil {
		title.SetFont(f)
	}
	title.SetTextColor(theme.WindowText)

	hint, _ := walk.NewLabel(comp)
	_ = hint.SetText("启动一个 Claude Code 会话后，它会自动出现在这里")
	hint.SetBackground(bg)
	if fontHint != nil {
		hint.SetFont(fontHint)
	}
	hint.SetTextColor(theme.SubtleText)

	_, _ = walk.NewVSpacer(comp)

	return comp
}
