// Package main: cc-console-sl 是 statusline 桥接的 helper。
//
// Claude Code 2.1.177+ 活跃会话不落盘 jsonl,实时 token/上下文只能通过
// statusline 通道获取。但 Claude Code 2.1.x 只执行 `node "mjs"` 形式的 statusLine,
// 故由 bridge.mjs(node)作入口,本 exe 由 bridge.mjs spawn 调用,负责核心重活:
//  1. 读 stdin(bridge 转发的 Claude 实时会话状态 JSON)
//  2. 从自身进程树向上找到 claude.exe → 得到 pid(实例主键)
//  3. 写 live/<pid>.json 供监控器读取
//
// 用 Go exe(而非 node)做这一步,是因为进程树遍历靠 Win32 API,且 exe 启动快。
// 链式调用原 statusLine(claude-hud)由 bridge.mjs 负责,本程序不输出。
package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// statuslineStdin 对应 Claude Code 推给 statusLine 命令的 JSON(只取需要的字段)。
type statuslineStdin struct {
	SessionID      string `json:"session_id"`
	SessionName    string `json:"session_name"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Version        string `json:"version"`
	Model          struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		UsedPercentage    *int  `json:"used_percentage"`
		ContextWindowSize int64 `json:"context_window_size"`
		CurrentUsage      *struct {
			InputTokens              int64 `json:"input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"current_usage"`
	} `json:"context_window"`
	Cost struct {
		TotalCostUsd    float64 `json:"total_cost_usd"`
		TotalDurationMs int64   `json:"total_duration_ms"`
	} `json:"cost"`
}

type hookStdin struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	ToolName       string `json:"tool_name"`
}

// liveRecord 是落盘到 live/<pid>.json 的结构,监控器 internal/monitor/bridge.go
// 用相同 JSON tag 解析。
type liveRecord struct {
	Pid            int     `json:"pid"`
	Ts             int64   `json:"ts"`
	SessionID      string  `json:"sessionId"`
	SessionName    string  `json:"sessionName"`
	Model          string  `json:"model"`
	ModelID        string  `json:"modelId"`
	Cwd            string  `json:"cwd"`
	TranscriptPath string  `json:"transcriptPath"`
	ContextTokens  int64   `json:"contextTokens"`
	ContextPercent int     `json:"contextPercent"`
	ContextLimit   int64   `json:"contextLimit"`
	Version        string  `json:"version"`
	CostUsd        float64 `json:"costUsd"`
	DurationMs     int64   `json:"durationMs"`
}

type hookState struct {
	Pid            int    `json:"pid"`
	Ts             int64  `json:"ts"`
	SessionID      string `json:"sessionId"`
	TranscriptPath string `json:"transcriptPath"`
	Cwd            string `json:"cwd"`
	LastEvent      string `json:"lastEvent"`
	Phase          string `json:"phase"`
	ToolName       string `json:"toolName"`
	ToolDepth      int    `json:"toolDepth"`
	TaskStartedAt  int64  `json:"taskStartedAt"`
}

func main() {
	raw := readStdin()
	pid := findClaudePID()
	if pid <= 0 {
		return
	}
	if event := hookEventArg(); event != "" {
		var stdin hookStdin
		_ = json.Unmarshal(raw, &stdin)
		if stdin.HookEventName == "" {
			stdin.HookEventName = event
		}
		writeHookState(pid, &stdin)
		return
	}
	var stdin statuslineStdin
	_ = json.Unmarshal(raw, &stdin) // 解析失败也无所谓,bridge 仍会链式透传
	writeLive(pid, &stdin)
	// 不输出:bridge.mjs 负责链式调用原 statusLine 并产出状态栏。
}

// readStdin 带超时读取全部 stdin(bridge 转发后会关闭)。
func readStdin() []byte {
	ch := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(os.Stdin)
		ch <- data
	}()
	select {
	case data := <-ch:
		return data
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

func hookEventArg() string {
	for i := 1; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--hook" {
			return os.Args[i+1]
		}
	}
	return ""
}

// writeLive 原子写 live/<pid>.json(先写 .tmp 再 rename,避免监控器读到半截)。
// Claude Code 的 statusline 推送存在间歇性空字段(某些刷新不推 current_usage/
// session_name 等),这里对空字段保留上次有效值,避免监控器读到 0/空抖动。
func writeLive(pid int, s *statuslineStdin) {
	dir := liveDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	prev, _ := readPrevLive(pid) // 上次记录,用于空字段兜底

	rec := liveRecord{
		Pid:            pid,
		Ts:             time.Now().UnixMilli(),
		SessionID:      s.SessionID,
		SessionName:    s.SessionName,
		Model:          s.Model.DisplayName,
		ModelID:        s.Model.ID,
		Cwd:            s.Cwd,
		TranscriptPath: s.TranscriptPath,
		Version:        s.Version,
		ContextLimit:   s.ContextWindow.ContextWindowSize,
		CostUsd:        s.Cost.TotalCostUsd,
		DurationMs:     s.Cost.TotalDurationMs,
	}
	// 空字段保留上次有效值(statusline 间歇性不推)
	if rec.SessionID == "" {
		rec.SessionID = prev.SessionID
	}
	if rec.SessionName == "" {
		rec.SessionName = prev.SessionName
	}
	if rec.Model == "" {
		rec.Model = prev.Model
	}
	if rec.Cwd == "" {
		rec.Cwd = prev.Cwd
	}
	if rec.TranscriptPath == "" {
		rec.TranscriptPath = prev.TranscriptPath
	}
	if rec.ContextLimit == 0 {
		rec.ContextLimit = prev.ContextLimit
	}
	if u := s.ContextWindow.CurrentUsage; u != nil {
		rec.ContextTokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	}
	if rec.ContextTokens == 0 {
		rec.ContextTokens = prev.ContextTokens // current_usage 间歇空,保留上次
	}
	if s.ContextWindow.UsedPercentage != nil && *s.ContextWindow.UsedPercentage > 0 {
		rec.ContextPercent = *s.ContextWindow.UsedPercentage // 原生百分比(v2.1.6+),最准
	} else if rec.ContextLimit > 0 && rec.ContextTokens > 0 {
		rec.ContextPercent = int(rec.ContextTokens * 100 / rec.ContextLimit)
	}
	if rec.ContextPercent == 0 {
		rec.ContextPercent = prev.ContextPercent
	}
	writeJSON(filepath.Join(dir, strconv.Itoa(pid)+".json"), rec)
}

func writeHookState(pid int, s *hookStdin) {
	dir := hookDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	prev, _ := readPrevHook(pid)
	nowMs := time.Now().UnixMilli()
	rec := hookState{
		Pid:            pid,
		Ts:             nowMs,
		SessionID:      firstNonEmpty(s.SessionID, prev.SessionID),
		TranscriptPath: firstNonEmpty(s.TranscriptPath, prev.TranscriptPath),
		Cwd:            firstNonEmpty(s.Cwd, prev.Cwd),
		LastEvent:      firstNonEmpty(s.HookEventName, prev.LastEvent),
		Phase:          prev.Phase,
		ToolName:       prev.ToolName,
		ToolDepth:      prev.ToolDepth,
		TaskStartedAt:  prev.TaskStartedAt,
	}
	tool := firstNonEmpty(s.ToolName, prev.ToolName)
	switch strings.TrimSpace(s.HookEventName) {
	case "UserPromptSubmit":
		rec.Phase = "busy"
		rec.ToolName = ""
		rec.ToolDepth = 0
		rec.TaskStartedAt = nowMs
	case "PreToolUse":
		rec.Phase = "busy"
		rec.ToolDepth++
		rec.ToolName = tool
		if prev.Phase != "busy" || prev.TaskStartedAt == 0 {
			rec.TaskStartedAt = nowMs
		}
	case "PostToolUse":
		if rec.ToolDepth > 0 {
			rec.ToolDepth--
		}
		rec.Phase = "busy"
		if rec.ToolDepth == 0 {
			rec.ToolName = ""
		} else {
			rec.ToolName = tool
		}
		if rec.TaskStartedAt == 0 {
			rec.TaskStartedAt = nowMs
		}
	case "Stop":
		rec.Phase = "idle"
		rec.ToolDepth = 0
		rec.ToolName = ""
	default:
		if rec.Phase == "" {
			rec.Phase = "busy"
		}
		rec.ToolName = tool
		if rec.Phase == "busy" && rec.TaskStartedAt == 0 {
			rec.TaskStartedAt = nowMs
		}
	}
	if rec.Phase == "" {
		rec.Phase = "busy"
	}
	if rec.Phase == "idle" {
		rec.TaskStartedAt = 0
	}
	writeJSON(filepath.Join(dir, strconv.Itoa(pid)+".json"), rec)
}

// readPrevLive 读取 pid 现有的 live 记录(供 writeLive 空字段兜底)。
func readPrevLive(pid int) (liveRecord, error) {
	var prev liveRecord
	data, err := os.ReadFile(filepath.Join(liveDir(), strconv.Itoa(pid)+".json"))
	if err != nil {
		return prev, err
	}
	_ = json.Unmarshal(data, &prev)
	return prev, nil
}

func readPrevHook(pid int) (hookState, error) {
	var prev hookState
	data, err := os.ReadFile(filepath.Join(hookDir(), strconv.Itoa(pid)+".json"))
	if err != nil {
		return prev, err
	}
	_ = json.Unmarshal(data, &prev)
	return prev, nil
}

func writeJSON(path string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

func firstNonEmpty(v string, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// liveDir 返回 ~/.cc-console/live/。
func liveDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cc-console", "live")
}

func hookDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cc-console", "hook")
}
