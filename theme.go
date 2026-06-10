package main

import (
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows/registry"
)

// ---- Notion 风格调色板 ----
//
// 设计精神：极简、留白、柔和。
//
// Light：参考 Notion 默认主题 — 暖白底 #FFFFFF / 面板 #FBFBFA / 近黑文字 #37352F
// Dark： 参考 Notion Dark    — 主背景 #191919 / 卡片 #2F2F2F / 文字 #E7E7E7
// 重点：色彩对比克制，靠留白与字号建立层级；状态色仅作"标签强调"使用。
type palette struct {
	WindowBG      walk.Color // 最外层窗口背景
	PanelBG       walk.Color // 顶部 Hero 面板（比 WindowBG 略微沉一档）
	CardBG        walk.Color // 卡片本体
	CardHoverBG   walk.Color // 卡片悬浮高亮
	CardBorder    walk.Color // 柔和边框（极淡）
	Divider       walk.Color // 分隔线（比边框更淡）
	TagBG         walk.Color // 小标签背景（模型名 / 计数器）
	WindowText    walk.Color // 正文 #37352F
	SecondaryText walk.Color // 次级 #787774
	SubtleText    walk.Color // 弱化 #9B9A97
	Accent        walk.Color // Notion 蓝 #2383E2
	AccentSoft    walk.Color // 高亮闪烁柔色
	StatusBusy    walk.Color // 忙碌（暖橙）
	StatusBusyDim walk.Color // 忙碌脉冲低相
	StatusIdle    walk.Color // 空闲（草绿）
	StatusUnknown walk.Color
	ContextLow    walk.Color
	ContextMid    walk.Color
	ContextHigh   walk.Color
	Success       walk.Color
	Warning       walk.Color
	Danger        walk.Color
}

var (
	// Notion Light
	lightPalette = palette{
		WindowBG:      walk.RGB(255, 255, 255),
		PanelBG:       walk.RGB(251, 251, 250),
		CardBG:        walk.RGB(255, 255, 255),
		CardHoverBG:   walk.RGB(247, 247, 245),
		CardBorder:    walk.RGB(233, 233, 231),
		Divider:       walk.RGB(237, 237, 235),
		TagBG:         walk.RGB(241, 241, 239),
		WindowText:    walk.RGB(55, 53, 47),
		SecondaryText: walk.RGB(120, 119, 116),
		SubtleText:    walk.RGB(155, 154, 151),
		Accent:        walk.RGB(35, 131, 226),
		AccentSoft:    walk.RGB(231, 243, 252),
		StatusBusy:    walk.RGB(212, 76, 71),   // 忙碌（红 = Danger 色）
		StatusBusyDim: walk.RGB(234, 142, 138), // 忙碌脉冲低相
		StatusIdle:    walk.RGB(68, 131, 97),
		StatusUnknown: walk.RGB(155, 154, 151),
		ContextLow:    walk.RGB(68, 131, 97),
		ContextMid:    walk.RGB(203, 145, 47),
		ContextHigh:   walk.RGB(212, 76, 71),
		Success:       walk.RGB(68, 131, 97),
		Warning:       walk.RGB(203, 145, 47),
		Danger:        walk.RGB(212, 76, 71),
	}

	// Notion Dark
	darkPalette = palette{
		WindowBG:      walk.RGB(25, 25, 25),
		PanelBG:       walk.RGB(32, 32, 32),
		CardBG:        walk.RGB(47, 47, 47),
		CardHoverBG:   walk.RGB(55, 55, 55),
		CardBorder:    walk.RGB(64, 64, 64),
		Divider:       walk.RGB(47, 47, 47),
		TagBG:         walk.RGB(55, 55, 55),
		WindowText:    walk.RGB(231, 231, 231),
		SecondaryText: walk.RGB(155, 154, 151),
		SubtleText:    walk.RGB(120, 120, 117),
		Accent:        walk.RGB(35, 131, 226),
		AccentSoft:    walk.RGB(40, 60, 90),
		StatusBusy:    walk.RGB(255, 115, 105), // 忙碌（红 = Danger 色）
		StatusBusyDim: walk.RGB(180, 80, 75),  // 忙碌脉冲低相
		StatusIdle:    walk.RGB(77, 171, 154),
		StatusUnknown: walk.RGB(120, 120, 117),
		ContextLow:    walk.RGB(77, 171, 154),
		ContextMid:    walk.RGB(255, 163, 68),
		ContextHigh:   walk.RGB(255, 115, 105),
		Success:       walk.RGB(77, 171, 154),
		Warning:       walk.RGB(255, 163, 68),
		Danger:        walk.RGB(255, 115, 105),
	}

	theme  *palette
	isDark bool
)

// ---- 主题检测与初始化 ----

func initTheme() {
	isDark = isSystemDarkMode()
	if isDark {
		theme = &darkPalette
	} else {
		theme = &lightPalette
	}
}

func isSystemDarkMode() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return val == 0
}

// ---- DWM 暗色标题栏 ----

const DWMWA_USE_IMMERSIVE_DARK_MODE = 20

func enableDarkTitleBar(hwnd win.HWND) {
	if !isDark {
		return
	}
	value := int32(1)
	procDwmSetWindowAttribute.Call(
		uintptr(hwnd),
		DWMWA_USE_IMMERSIVE_DARK_MODE,
		uintptr(unsafe.Pointer(&value)),
		unsafe.Sizeof(value),
	)
}

// ---- 颜色工具 ----

// blendColor 按 t∈[0,1] 在 a 和 b 之间线性插值。
func blendColor(a, b walk.Color, t float64) walk.Color {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	ar, ag, ab := byte(a&0xff), byte((a>>8)&0xff), byte((a>>16)&0xff)
	br, bg, bb := byte(b&0xff), byte((b>>8)&0xff), byte((b>>16)&0xff)
	r := byte(float64(ar) + (float64(br)-float64(ar))*t)
	g := byte(float64(ag) + (float64(bg)-float64(ag))*t)
	bl := byte(float64(ab) + (float64(bb)-float64(ab))*t)
	return walk.RGB(r, g, bl)
}

// contextBarColor 按使用率返回三档色。
func contextBarColor(pct int64) walk.Color {
	switch {
	case pct < 50:
		return theme.ContextLow
	case pct < 80:
		return theme.ContextMid
	default:
		return theme.ContextHigh
	}
}

// statusEmoji 返回状态对应的 emoji（不带空格）。
func statusEmoji(status string) string {
	switch status {
	case "busy":
		return "🔴"
	case "idle":
		return "🟢"
	}
	return "⚪"
}

// statusLabelText 返回状态的中文文字（不含 emoji）。
func statusLabelText(status string) string {
	switch status {
	case "busy":
		return "忙碌"
	case "idle":
		return "空闲"
	}
	return "未知"
}

// statusColor 返回状态当前 tick 下的文字颜色（busy 走脉冲）。
func statusColor(status string) walk.Color {
	switch status {
	case "busy":
		return blendColor(theme.StatusBusyDim, theme.StatusBusy, pulseFactor())
	case "idle":
		return theme.StatusIdle
	}
	return theme.StatusUnknown
}
