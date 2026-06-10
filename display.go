package main

import (
	"fmt"
	"strings"
	"time"
)

// ---- 展示辅助函数（CLI + GUI 共用） ----

func statusRank(s string) int {
	switch s {
	case "busy":
		return 0
	case "idle":
		return 1
	}
	return 2
}

func countStatus(insts []Instance, s string) int {
	c := 0
	for _, it := range insts {
		if it.Status == s {
			c++
		}
	}
	return c
}

func totalContext(insts []Instance) int64 {
	var t int64
	for _, it := range insts {
		t += it.ContextTokens
	}
	return t
}

func statusText(s string) string {
	switch s {
	case "busy":
		return "● 忙碌"
	case "idle":
		return "○ 空闲"
	case "", "unknown":
		return "? 未知"
	}
	return "? " + s
}

func modelDisplay(it Instance) string {
	if !it.HasConversation {
		return "（新）"
	}
	if it.Model == "" {
		return "—"
	}
	return it.Model
}

func topicDisplay(it Instance) string {
	if !it.HasConversation {
		return "（新会话·无消息）"
	}
	if it.Topic == "" {
		return "（暂无主题）"
	}
	return it.Topic
}

func outputDisplay(it Instance) string {
	if !it.HasConversation {
		return "（新）"
	}
	return formatTokens(it.OutputTokens)
}

// contextDisplay: 用于 GUI 表格的 Context 列。
// 已知上限时渲染 Unicode 进度条：「━━━━━━━─── 74% · 148k/200k」
// 颜色由 styleCellFunc.contextCellColor 决定。
func contextDisplay(it Instance) string {
	if !it.HasConversation {
		return "（新会话）"
	}
	if it.ContextTokens <= 0 {
		return "—"
	}
	if it.ContextLimit > 0 {
		pct := it.ContextTokens * 100 / it.ContextLimit
		return fmt.Sprintf("%s  %d%% · %s/%s",
			unicodeBar(int(pct), 10), pct, compactK(it.ContextTokens), compactK(it.ContextLimit))
	}
	return compactK(it.ContextTokens)
}

// contextDisplayPlain 是 --list 模式用的纯文本版本（不带进度条字符，避免对齐错位）。
func contextDisplayPlain(it Instance) string {
	if !it.HasConversation {
		return "（新）"
	}
	if it.ContextTokens <= 0 {
		return "—"
	}
	if it.ContextLimit > 0 {
		pct := it.ContextTokens * 100 / it.ContextLimit
		return fmt.Sprintf("%d%%  %s/%s", pct, compactK(it.ContextTokens), compactK(it.ContextLimit))
	}
	return compactK(it.ContextTokens)
}

// unicodeBar 渲染 width 格进度条：已用 ━（U+2501 BOX DRAWINGS HEAVY HORIZONTAL），
// 未用 ─（U+2500 BOX DRAWINGS LIGHT HORIZONTAL）。两者宽度一致，对齐稳定。
func unicodeBar(pct, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	if pct > 0 && filled == 0 {
		filled = 1 // 有用量就至少显示 1 格
	}
	b := make([]rune, 0, width)
	for i := 0; i < width; i++ {
		if i < filled {
			b = append(b, '━')
		} else {
			b = append(b, '─')
		}
	}
	return string(b)
}

// ---- 格式化工具 ----

// compactK 整数缩写：1k / 148k / 1M
func compactK(n int64) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%dM", n/1000000)
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

// formatTokens 带小数缩写：1.2k / 1.5M / 10.0M。0 或负数返回 "—"。
func formatTokens(n int64) string {
	if n <= 0 {
		return "—"
	}
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// formatTokensCompact 带小数缩写，但 0 返回 "0"（用于 stats 行，"—" 语义不对）。
func formatTokensCompact(n int64) string {
	if n <= 0 {
		return "0"
	}
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func humanDuration(fromMs int64, now time.Time) string {
	if fromMs <= 0 {
		return "—"
	}
	d := now.Sub(time.UnixMilli(fromMs))
	if d < 0 {
		d = 0
	}
	sec := int64(d / time.Second)
	switch {
	case sec < 60:
		return fmt.Sprintf("%d 秒", sec)
	case sec < 3600:
		return fmt.Sprintf("%d 分钟", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%d 小时 %d 分", sec/3600, (sec%3600)/60)
	default:
		return fmt.Sprintf("%d 天 %d 小时", sec/86400, (sec%86400)/3600)
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// joinWithDot 用圆点分隔符连接字符串切片。
func joinWithDot(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "  ·  "
		}
		out += p
	}
	return out
}
