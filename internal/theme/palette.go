package theme

import "fmt"

// Color 表示 RGB 颜色（0x00BBGGRR，与 Win32 COLORREF 兼容）。
type Color uint32

// RGB 创建 Color。
func RGB(r, g, b byte) Color {
	return Color(uint32(r) | uint32(g)<<8 | uint32(b)<<16)
}

// ---- Notion 风格调色板 ----
//
// 设计精神：极简、留白、柔和。
//
// Light：参考 Notion 默认主题 — 暖白底 #FFFFFF / 面板 #FBFBFA / 近黑文字 #37352F
// Dark： 参考 Notion Dark    — 主背景 #191919 / 卡片 #2F2F2F / 文字 #E7E7E7
// 重点：色彩对比克制，靠留白与字号建立层级；状态色仅作"标签强调"使用。
type Palette struct {
	WindowBG      Color // 最外层窗口背景
	PanelBG       Color // 顶部 Hero 面板
	CardBG        Color // 卡片本体
	CardHoverBG   Color // 卡片悬浮高亮
	CardBorder    Color // 柔和边框
	Divider       Color // 分隔线
	TagBG         Color // 小标签背景
	WindowText    Color // 正文
	SecondaryText Color // 次级
	SubtleText    Color // 弱化
	Accent        Color // Notion 蓝
	AccentSoft    Color // 高亮闪烁柔色
	StatusBusy    Color // 忙碌（红）
	StatusBusyDim Color // 忙碌脉冲低相
	StatusIdle    Color // 空闲（绿）
	StatusUnknown Color
	ContextLow    Color
	ContextMid    Color
	ContextHigh   Color
	Success       Color
	Warning       Color
	Danger        Color
}

var (
	// Notion Light
	LightPalette = Palette{
		WindowBG:      RGB(255, 255, 255),
		PanelBG:       RGB(251, 251, 250),
		CardBG:        RGB(255, 255, 255),
		CardHoverBG:   RGB(247, 247, 245),
		CardBorder:    RGB(233, 233, 231),
		Divider:       RGB(237, 237, 235),
		TagBG:         RGB(241, 241, 239),
		WindowText:    RGB(55, 53, 47),
		SecondaryText: RGB(120, 119, 116),
		SubtleText:    RGB(155, 154, 151),
		Accent:        RGB(35, 131, 226),
		AccentSoft:    RGB(231, 243, 252),
		StatusBusy:    RGB(212, 76, 71),
		StatusBusyDim: RGB(234, 142, 138),
		StatusIdle:    RGB(68, 131, 97),
		StatusUnknown: RGB(155, 154, 151),
		ContextLow:    RGB(68, 131, 97),
		ContextMid:    RGB(203, 145, 47),
		ContextHigh:   RGB(212, 76, 71),
		Success:       RGB(68, 131, 97),
		Warning:       RGB(203, 145, 47),
		Danger:        RGB(212, 76, 71),
	}

	// Notion Dark
	DarkPalette = Palette{
		WindowBG:      RGB(25, 25, 25),
		PanelBG:       RGB(32, 32, 32),
		CardBG:        RGB(47, 47, 47),
		CardHoverBG:   RGB(55, 55, 55),
		CardBorder:    RGB(64, 64, 64),
		Divider:       RGB(47, 47, 47),
		TagBG:         RGB(55, 55, 55),
		WindowText:    RGB(231, 231, 231),
		SecondaryText: RGB(155, 154, 151),
		SubtleText:    RGB(120, 120, 117),
		Accent:        RGB(35, 131, 226),
		AccentSoft:    RGB(40, 60, 90),
		StatusBusy:    RGB(255, 115, 105),
		StatusBusyDim: RGB(180, 80, 75),
		StatusIdle:    RGB(77, 171, 154),
		StatusUnknown: RGB(120, 120, 117),
		ContextLow:    RGB(77, 171, 154),
		ContextMid:    RGB(255, 163, 68),
		ContextHigh:   RGB(255, 115, 105),
		Success:       RGB(77, 171, 154),
		Warning:       RGB(255, 163, 68),
		Danger:        RGB(255, 115, 105),
	}
)

// Current 返回当前主题调色板。
func Current(dark bool) *Palette {
	if dark {
		return &DarkPalette
	}
	return &LightPalette
}

// ContextBarColorCSS 返回上下文条颜色（CSS hex 格式）。
func ContextBarColorCSS(pct int64, dark bool) string {
	p := Current(dark)
	switch {
	case pct < 50:
		return colorToCSS(p.ContextLow)
	case pct < 80:
		return colorToCSS(p.ContextMid)
	default:
		return colorToCSS(p.ContextHigh)
	}
}

// StatusEmoji 返回状态对应的 emoji。
func StatusEmoji(status string) string {
	switch status {
	case "busy":
		return "🔴"
	case "idle":
		return "🟢"
	}
	return "⚪"
}

// StatusLabelText 返回状态的中文文字。
func StatusLabelText(status string) string {
	switch status {
	case "busy":
		return "忙碌"
	case "idle":
		return "空闲"
	}
	return "未知"
}

// ---- 颜色工具 ----

// BlendColor 按 t∈[0,1] 在 a 和 b 之间线性插值。
func BlendColor(a, b Color, t float64) Color {
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
	return RGB(r, g, bl)
}

// colorToCSS 将 Color 转换为 CSS hex 格式 (#RRGGBB)。
func colorToCSS(c Color) string {
	r := byte(c & 0xff)
	g := byte((c >> 8) & 0xff)
	b := byte((c >> 16) & 0xff)
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

// PaletteToCSSMap 返回当前调色板的 CSS 变量映射。
func PaletteToCSSMap(dark bool) map[string]string {
	p := Current(dark)
	return map[string]string{
		"--window-bg":      colorToCSS(p.WindowBG),
		"--panel-bg":       colorToCSS(p.PanelBG),
		"--card-bg":        colorToCSS(p.CardBG),
		"--card-hover-bg":  colorToCSS(p.CardHoverBG),
		"--card-border":    colorToCSS(p.CardBorder),
		"--divider":        colorToCSS(p.Divider),
		"--tag-bg":         colorToCSS(p.TagBG),
		"--window-text":    colorToCSS(p.WindowText),
		"--secondary-text": colorToCSS(p.SecondaryText),
		"--subtle-text":    colorToCSS(p.SubtleText),
		"--accent":         colorToCSS(p.Accent),
		"--accent-soft":    colorToCSS(p.AccentSoft),
		"--status-busy":    colorToCSS(p.StatusBusy),
		"--status-busy-dim":colorToCSS(p.StatusBusyDim),
		"--status-idle":    colorToCSS(p.StatusIdle),
		"--status-unknown": colorToCSS(p.StatusUnknown),
		"--context-low":    colorToCSS(p.ContextLow),
		"--context-mid":    colorToCSS(p.ContextMid),
		"--context-high":   colorToCSS(p.ContextHigh),
		"--success":        colorToCSS(p.Success),
		"--warning":        colorToCSS(p.Warning),
		"--danger":         colorToCSS(p.Danger),
	}
}
