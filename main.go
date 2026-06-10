package main

import (
	_ "embed"
	"fmt"
	"os"
	"time"

	"claude-monitor/internal/monitor"
)

//go:embed icon.ico
var iconBytes []byte

func main() {
	monitor.LoadConfig()

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

	runWailsApp()
}

func runList() {
	live, stale, err := monitor.Detect()
	if err != nil {
		fmt.Fprintln(os.Stderr, "检测出错:", err)
		os.Exit(1)
	}

	head := fmt.Sprintf("在线 Claude Code 实例: %d", len(live))
	if n := monitor.CountStatus(live, "busy") + monitor.CountStatus(live, "idle"); n > 0 {
		head += fmt.Sprintf("   (● 忙碌 %d   ○ 空闲 %d)", monitor.CountStatus(live, "busy"), monitor.CountStatus(live, "idle"))
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
		topic := monitor.TopicDisplay(it)
		if len([]rune(topic)) > 26 {
			topic = monitor.TruncateRunes(topic, 25)
		}
		fmt.Printf("%-7d  %-8s  %-12s  %-16s  %-8s  %-26s  %s\n",
			it.Pid, monitor.StatusText(it.Status), monitor.ModelDisplay(it),
			monitor.ContextDisplayPlain(it), monitor.OutputDisplay(it), topic, cwd)
	}
	fmt.Println()
	fmt.Printf("合计 Context: %s", monitor.FormatTokens(monitor.TotalContext(live)))
	if len(stale) > 0 {
		fmt.Printf("    另有 %d 个残留会话（进程已退出）", len(stale))
	}
	fmt.Println()
}

// buildStatsLine 构建统计行文本（前端也会生成，此函数用于 CLI 和托盘 tooltip）。
func buildStatsLine(online, busy, idle int, totalCtx int64, stale int) string {
	if online == 0 {
		return "🌙  当前无实例运行"
	}
	parts := []string{
		fmt.Sprintf("在线 %d", online),
		fmt.Sprintf("🔴 %d 忙碌", busy),
		fmt.Sprintf("🟢 %d 空闲", idle),
	}
	if totalCtx > 0 {
		parts = append(parts, fmt.Sprintf("📦 %s context", monitor.FormatTokensCompact(totalCtx)))
	}
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("🌓 %d 残留", stale))
	}
	return monitor.JoinWithDot(parts)
}

// unused keeps time import available for CLI mode
var _ = time.Sleep
