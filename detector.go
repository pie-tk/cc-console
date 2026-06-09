package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SessionInfo 对应 ~/.claude/sessions/<pid>.json 的结构（多余字段忽略）。
type SessionInfo struct {
	Pid        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	StartedAt  int64  `json:"startedAt"` // epoch 毫秒
	ProcStart  string `json:"procStart"`
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
	Status     string `json:"status"`  // busy / idle / ...
	UpdatedAt  int64  `json:"updatedAt"` // epoch 毫秒
}

// Instance 表示一个被发现的 Claude Code 实例。
type Instance struct {
	Pid        int
	Status     string
	Cwd        string
	Version    string
	SessionID  string
	StartedAt  int64 // epoch 毫秒
	UpdatedAt  int64 // epoch 毫秒
	HasSession bool  // 是否找到了对应的 session 文件
	// 来自对话 JSONL：
	HasConversation bool   // 是否存在 .jsonl 对话文件（新会话为 false）
	Model           string // 最后一条 assistant 的模型
	ContextTokens   int64  // input + cache_creation + cache_read
	OutputTokens    int64  // 本轮输出
	Topic           string // 对话主题（ai-title，回退首条 user 消息）
	ContextLimit    int64  // 模型上下文上限（查表/配置，0 表示未知）
}

// procInfo 是一个 claude.exe 进程的基本信息。
type procInfo struct {
	pid          int
	exePath      string
	createTimeMs int64 // 进程创建时间（epoch 毫秒），0 表示读取失败
}

func claudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// Detect 返回当前存活的 Claude Code 实例，以及残留（进程已退出但 session 文件仍在）的会话。
func Detect() (live []Instance, stale []Instance, err error) {
	procs, err := listClaudeProcesses()
	if err != nil {
		return nil, nil, err
	}
	sessionsDir := filepath.Join(claudeDir(), "sessions")
	sessions := loadSessions(sessionsDir) // map[pid]*SessionInfo

	liveSet := make(map[int]bool)
	for _, p := range procs {
		if !isClaudeCode(p, sessions) {
			continue
		}
		live = append(live, buildInstance(p.pid, sessions))
		liveSet[p.pid] = true
	}

	for pid, s := range sessions {
		if liveSet[pid] {
			continue
		}
		stale = append(stale, instanceFromSession(pid, s))
	}

	sort.SliceStable(live, func(i, j int) bool {
		if ri, rj := statusRank(live[i].Status), statusRank(live[j].Status); ri != rj {
			return ri < rj
		}
		return live[i].Pid < live[j].Pid
	})
	sort.SliceStable(stale, func(i, j int) bool {
		return stale[i].Pid < stale[j].Pid
	})
	return live, stale, nil
}

// isClaudeCode 判断一个 claude.exe 进程是否为一个真正的 Claude Code 交互实例。
//
// 判定标准：必须有对应的 session 文件（~/.claude/sessions/<pid>.json），且进程
// 启动时间与该会话的 startedAt 一致（容差 15s，排除 PID 复用）。
//
// 为什么不再用「可执行路径含 claude-code」直接放行：Claude Code 启动瞬间会派生
// 若干短命辅助子进程（版本/更新探测等），它们与主进程共用同一个 claude.exe 二进制、
// 同一条路径，按路径放行会把它们全部计入，表现为「启动时冒出好几行、1~2 秒后消失、
// 只剩一个」。这些辅助进程都不写 session 文件——而真正的交互实例几乎立即就会写
// session 文件。因此「有 session 文件」才是「活着且是个实例」的可靠标志。
// （Claude 桌面版同样不写 session 文件，顺带被排除。）
func isClaudeCode(p procInfo, sessions map[int]*SessionInfo) bool {
	s, ok := sessions[p.pid]
	if !ok {
		return false
	}
	if p.createTimeMs == 0 {
		return false
	}
	return abs(p.createTimeMs-s.StartedAt) <= 15000
}

func buildInstance(pid int, sessions map[int]*SessionInfo) Instance {
	inst := Instance{Pid: pid, Status: "unknown"}
	s, ok := sessions[pid]
	if !ok {
		return inst
	}
	inst.Status = s.Status
	if inst.Status == "" {
		inst.Status = "unknown"
	}
	inst.Cwd = s.Cwd
	inst.Version = s.Version
	inst.SessionID = s.SessionID
	inst.StartedAt = s.StartedAt
	inst.UpdatedAt = s.UpdatedAt
	inst.HasSession = true

	d := loadConversationDetails(s)
	inst.HasConversation = d.hasFile
	inst.Model = d.model
	inst.ContextTokens = d.context
	inst.OutputTokens = d.output
	inst.Topic = d.topic
	inst.ContextLimit = modelContextLimit(d.model)
	return inst
}

func instanceFromSession(pid int, s *SessionInfo) Instance {
	if s == nil {
		return Instance{Pid: pid, Status: "unknown"}
	}
	return buildInstance(pid, map[int]*SessionInfo{pid: s})
}

// loadSessions 读取 ~/.claude/sessions/*.json，返回按 PID 索引的会话映射。
func loadSessions(sessionsDir string) map[int]*SessionInfo {
	m := map[int]*SessionInfo{}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return m
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		pid, perr := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if perr != nil {
			continue
		}
		data, derr := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
		if derr != nil {
			continue
		}
		var s SessionInfo
		if json.Unmarshal(data, &s) == nil {
			m[pid] = &s
		}
	}
	return m
}

// ---- 对话 JSONL 解析：model / context / output tokens / topic ----

type usageInfo struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

type convDetails struct {
	hasFile bool
	model   string
	context int64
	output  int64
	topic   string
}

type convCacheEntry struct {
	mtime   int64
	details convDetails
}

var convCache = map[string]convCacheEntry{}

// encodeProjectPath 把工作目录编码成 ~/.claude/projects 下的目录名（与 Claude Code 一致：: / \ 都换成 -）。
func encodeProjectPath(cwd string) string {
	r := strings.NewReplacer(":", "-", "\\", "-", "/", "-")
	return r.Replace(cwd)
}

// loadConversationDetails 从会话对应的 JSONL 中读取模型、token 用量与对话主题。
// 按 mtime 缓存，避免每次刷新都重读大文件。
func loadConversationDetails(s *SessionInfo) convDetails {
	var d convDetails
	if s == nil || s.SessionID == "" || s.Cwd == "" {
		return d
	}
	base := claudeDir()
	if base == "" {
		return d
	}
	path := filepath.Join(base, "projects", encodeProjectPath(s.Cwd), s.SessionID+".jsonl")

	info, err := os.Stat(path)
	if err != nil {
		return d // 对话文件不存在 → 新会话
	}
	d.hasFile = true
	mtime := info.ModTime().UnixNano()
	if c, ok := convCache[path]; ok && c.mtime == mtime {
		return c.details
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return d
	}
	parseConversation(data, &d)

	defer func() { // 用具名返回/闭包写入缓存
		convCache[path] = convCacheEntry{mtime: mtime, details: d}
	}()
	return d
}

// parseConversation 单次遍历对话文件，取：最后一条 assistant 的模型/用量、最后一条 ai-title 主题、首条 user 文本（主题回退）。
func parseConversation(data []byte, d *convDetails) {
	firstUserSet := false
	var firstUser string

	for _, raw := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		switch {
		case bytes.Contains(line, []byte(`"type":"assistant"`)):
			var cl struct {
				Message struct {
					Model string     `json:"model"`
					Usage *usageInfo `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &cl) == nil && cl.Message.Usage != nil {
				u := cl.Message.Usage
				d.model = cl.Message.Model
				d.context = int64(u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens)
				d.output = int64(u.OutputTokens)
			}
		case bytes.Contains(line, []byte(`"type":"ai-title"`)):
			var at struct {
				AiTitle string `json:"aiTitle"`
			}
			if json.Unmarshal(line, &at) == nil && at.AiTitle != "" {
				d.topic = at.AiTitle // 不断覆盖，保留最后一条
			}
		case !firstUserSet && bytes.Contains(line, []byte(`"type":"user"`)):
			if t := extractUserText(line); t != "" {
				firstUser = t
			}
			firstUserSet = true
		}
	}

	if d.topic == "" && firstUser != "" {
		d.topic = truncateRunes(firstUser, 60)
	}
}

// extractUserText 从一条 user 消息行中提取纯文本（content 为字符串或 text 块数组）。
func extractUserText(line []byte) string {
	var ul struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &ul) != nil {
		return ""
	}
	c := bytes.TrimSpace(ul.Message.Content)
	if len(c) == 0 {
		return ""
	}
	if c[0] == '"' {
		var s string
		if json.Unmarshal(c, &s) == nil {
			return s
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(c, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---- 模型上下文上限（查表 + 配置覆盖） ----

var configLimits = map[string]int64{}

type modelLimitEntry struct {
	prefix string
	limit  int64
}

// 模型上下文窗口上限表（前缀匹配，第一条命中即返回）。
// 数据来源：各厂商官方文档（2025-2026），具体值可能随后端/版本变化，
// 可在 ~/.claude-monitor.json 的 modelLimits 字段覆盖。
var builtinModelLimits = []modelLimitEntry{
	// ---- Anthropic Claude ----
	// 注意：4.5+ 扩展到 1M；基础 4.x 为 200K
	{"claude-opus-4-8", 1000000},
	{"claude-opus-4-7", 1000000},
	{"claude-opus-4-6", 1000000},
	{"claude-opus-4", 200000},
	{"claude-sonnet-4-6", 1000000},
	{"claude-sonnet-4-5", 1000000},
	{"claude-sonnet-4", 200000},
	{"claude-haiku-4", 200000},
	{"claude-3-5-sonnet", 200000},
	{"claude-3-5-haiku", 200000},
	{"claude-3-opus", 200000},
	{"claude-3-sonnet", 200000},
	{"claude-3-haiku", 200000},

	// ---- OpenAI ----
	{"o4-mini", 200000},
	{"o3-mini", 200000},
	{"o3", 200000},
	{"o1-mini", 128000},
	{"o1", 200000},
	{"gpt-4.1-mini", 1048576},
	{"gpt-4.1-nano", 1048576},
	{"gpt-4.1", 1048576},
	{"gpt-4.5", 128000},
	{"gpt-4o-mini", 128000},
	{"gpt-4o", 128000},

	// ---- Google Gemini ----
	{"gemini-2.5-flash-lite", 1048576},
	{"gemini-2.5-flash", 1048576},
	{"gemini-2.5-pro", 1048576},
	{"gemini-2.0-flash-lite", 1048576},
	{"gemini-2.0-flash", 1048576},
	{"gemini-1.5-flash", 1048576},
	{"gemini-1.5-pro", 2097152},

	// ---- DeepSeek ----
	{"deepseek-v4", 1048576},
	{"deepseek_v4", 1048576},
	{"deepseek-r1", 131072},
	{"deepseek-v3", 131072},
	{"deepseek_v3", 131072},
	{"deepseek-v2", 131072},
	{"deepseek_v2", 131072},
	{"deepseek", 131072},

	// ---- 通义千问 Qwen ----
	{"qwen-long", 10000000},
	{"qwen-turbo", 1048576},
	{"qwen-plus", 1048576},
	{"qwen-max", 131072},
	{"qwen3-coder", 262144},
	{"qwen3-next", 262144},
	{"qwen3-max", 131072},
	{"qwen3-", 131072}, // qwen3-8b/14b/32b/72b via YaRN
	{"qwen2.5-", 131072},
	{"qwen", 131072},

	// ---- 智谱 GLM ----
	{"glm-5", 200000},
	{"glm-4.5-air", 131072},
	{"glm-4.5", 200000},
	{"glm-4-", 131072},
	{"glm-4", 131072},

	// ---- 月之暗面 Kimi ----
	{"kimi-k2-5", 262144},
	{"kimi-k2", 131072},
	{"moonshot-v1", 131072},

	// ---- 百度文心 ----
	{"ernie-5", 131072},
	{"ernie-4-5", 131072},
	{"ernie-4.5", 131072},

	// ---- 字节豆包 Doubao ----
	{"doubao-seed-1-6", 262144},
	{"doubao", 131072},

	// ---- Meta Llama ----
	{"llama-4-scout", 10485760},
	{"llama-4-maverick", 1048576},
	{"llama-4-", 1048576},
	{"llama-3-", 131072},
	{"llama-3", 8192},

	// ---- Mistral ----
	{"mistral-large", 262144},
	{"mistral-medium", 131072},
	{"mistral-small", 131072},
	{"mistral-nemo", 131072},
	{"codestral", 262144},
	{"pixtral", 131072},

	// ---- Cohere ----
	{"command-a", 262144},
	{"command-r-plus", 131072},
	{"command-r", 131072},
}

// modelContextLimit 解析模型字符串的上下文上限。
//
// 优先顺序：
//  1. 模型字符串里显式带的上限信息（格式：<model>[<limit>]，如 "deepseek-v4-pro[1M]" / "glm-5[256k]"）
//  2. ~/.claude-monitor.json 里的精确模型映射
//  3. 内置表的前缀匹配
//  4. 默认 200000
func modelContextLimit(model string) int64 {
	if model == "" {
		return 0
	}
	ml := strings.ToLower(model)

	// 1) 显式 [xxx] 后缀：支持 K/M/G 缩写（200k = 200*1000，2m = 2*1000*1000）
	base, explicit, hasExplicit := splitModelAndLimit(ml)
	if hasExplicit {
		if v, ok := parseLimitToken(explicit); ok {
			return v
		}
		// 解析失败就忽略括号，继续走下面的匹配
	}
	// 解析失败或没括号时，用原串继续
	if base != "" {
		ml = base
	}

	// 2) 配置精确覆盖
	if v, ok := configLimits[ml]; ok {
		return v
	}
	// 3) 内置表前缀匹配
	for _, e := range builtinModelLimits {
		if strings.HasPrefix(ml, e.prefix) {
			return e.limit
		}
	}
	// 4) 默认 200K
	return 200000
}

// splitModelAndLimit 拆 "model[limit]"；返回 (base, limit, true) 或 (原串, "", false)
func splitModelAndLimit(s string) (string, string, bool) {
	i := strings.LastIndex(s, "[")
	j := strings.LastIndex(s, "]")
	if i >= 0 && j > i {
		return s[:i], strings.ToLower(s[i+1 : j]), true
	}
	return s, "", false
}

// parseLimitToken 解析 "200k" / "2m" / "1g" / 纯数字 → tokens
func parseLimitToken(t string) (int64, bool) {
	t = strings.TrimSpace(t)
	if t == "" {
		return 0, false
	}
	mult := int64(1)
	switch t[len(t)-1] {
	case 'k':
		mult = 1000
		t = t[:len(t)-1]
	case 'm':
		mult = 1000000
		t = t[:len(t)-1]
	case 'g':
		mult = 1000000000
		t = t[:len(t)-1]
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n * mult, true
}

// loadConfig 读取 ~/.claude-monitor.json，支持 {"modelLimits":{"glm-5.1":200000}} 覆盖默认上限。
func loadConfig() {
	configLimits = map[string]int64{}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude-monitor.json"))
	if err != nil {
		return
	}
	var cfg struct {
		ModelLimits map[string]int64 `json:"modelLimits"`
	}
	if json.Unmarshal(data, &cfg) == nil {
		for k, v := range cfg.ModelLimits {
			configLimits[strings.ToLower(k)] = v
		}
	}
}

// ---- 进程枚举 ----

func listClaudeProcesses() ([]procInfo, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	var out []procInfo
	// Toolhelp32 快照在进程增删瞬间偶尔会把同一个进程列出两次，
	// 导致同一 PID 在界面里重复成两行。按 PID 去重，保证每个 PID 最多一条。
	seen := make(map[int]bool)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snapshot, &pe); err != nil {
		return nil, err
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		name = strings.TrimSuffix(name, ".exe")
		if name == "claude" {
			pid := int(pe.ProcessID)
			if !seen[pid] {
				seen[pid] = true
				out = append(out, procInfo{
					pid:          pid,
					exePath:      processExePath(pe.ProcessID),
					createTimeMs: processCreateTimeMs(pe.ProcessID),
				})
			}
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}
	return out, nil
}

func processExePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var buf [windows.MAX_PATH + 1]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

func processCreateTimeMs(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0
	}
	return filetimeToEpochMs(c)
}

func filetimeToEpochMs(ft windows.Filetime) int64 {
	const epochDiff100ns = 116444736000000000
	n := uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
	if n < epochDiff100ns {
		return 0
	}
	return int64((n - epochDiff100ns) / 10000)
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// ---- 展示辅助 ----

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

// contextDisplay: 新会话 → "（新）"；无用量 → "—"；有上限 → "74%  148k/200k"；否则裸用量。
func contextDisplay(it Instance) string {
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

func compactK(n int64) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%dM", n/1000000)
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

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
