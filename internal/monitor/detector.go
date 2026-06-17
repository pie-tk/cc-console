package monitor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

func claudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeProc 表示枚举到的一个 claude.exe 进程。
type claudeProc struct {
	pid      int
	createMs int64 // 进程创建时间（epoch 毫秒）
}

// 平台特定实现，由 detector_windows.go / detector_darwin.go 在 init 中注册。
var (
	isProcessAlive     func(pid int, startedAt int64) bool  // 单 pid 存活验证（保留供其他场景）
	enumerateClaude    func() []claudeProc                  // 枚举所有 claude.exe 进程
	procCwd            func(pid int) string                 // 读进程工作目录
	enumerateChildren  func(claudePids []int) map[int][]int // 各 claude.exe 的直接子进程 pid（供 busy 判定，非 Win 为 nil）
)

// cacheMu 保护下面所有包级缓存 map（lastInstanceByPid / lastBusyAt / convCache /
// chatHistoryCache / gitBranchCache）。这些 map 同时被多条 goroutine 访问：
// 前端每秒 DetectInstances、托盘 goroutine 每 2 秒 Detect、聊天面板 GetChatHistory。
// 不加锁会触发 Go runtime fatal error: concurrent map read and map write，进程直接被杀
// （表现为程序无声闪退）。每处访问最小化持锁，绝不嵌套同锁，避免死锁。
var cacheMu sync.RWMutex

// lastInstanceByPid 缓存最近一次 Detect 为每个 pid 构造的会话信息，供 GetChatHistory(pid) 复用。
var lastInstanceByPid = map[int]*SessionInfo{}

// lastBusyAt 记录每个 pid 最近一次判定为 busy 的时刻(ms),用于 fusedStatus 滞回。
var lastBusyAt = map[int]int64{}

// GetCachedSession 返回最近一次 Detect 缓存的 pid 对应会话信息（供 GetChatHistory 复用）。
func GetCachedSession(pid int) (*SessionInfo, bool) {
	cacheMu.RLock()
	si, ok := lastInstanceByPid[pid]
	cacheMu.RUnlock()
	return si, ok
}

// sessionMeta 是 jsonl 文件的轻量元信息（仅文件名 + mtime），供 pid↔sessionId 匹配。
type sessionMeta struct {
	sessionID string
	mtimeMs   int64 // 文件 mtime（epoch 毫秒）
}

// busyGraceMs：失去 busy 信号(statusline 停止刷新且无工具子进程)后仍保持 busy 的宽限期。
// 滞回窗口,消除 statusline 零星刷新导致的 busy/idle 抖动。
const busyGraceMs int64 = 6000

// Detect 返回当前存活的 Claude Code 实例。
//
// Claude Code 2.1.177+ 不再写 ~/.claude/sessions/<pid>.json，且整个 .claude 目录不持久化
// 任何 pid。因此实例发现改为以 claude.exe 进程为锚点：枚举进程拿 pid → 读进程工作目录 →
// 关联 projects 下 jsonl 取 model/tokens/history 等展示信息。pid 是唯一可信主键（输入注入
// 等操作按 pid 精确工作）；busy/idle 由 jsonl 文件活跃度推断。
// Detect 返回当前存活的 Claude Code 实例。
//
// Claude Code 2.1.169 引入的 regression(Issue #66486):交互式会话不再实时落盘 jsonl,
// 实时数据改通过 statusline 桥接获取——claude-monitor-sl.exe 把每个会话的实时状态写到
// ~/.claude-monitor/live/<pid>.json。本函数以 claude.exe 进程为锚点枚举 pid → 读对应 live
// 文件精确还原(model/context/busy)。无新鲜 live 文件时回退到旧的 cwd+mtime 猜测(读 jsonl,
// 在 regression 修复或会话结束后生效),前端标注"未接入"。
func Detect() (live []Instance, stale []Instance, err error) {
	now := time.Now().UnixMilli()

	procs := enumerateClaude()
	sessionsByCwd := indexProjectSessions() // 仅 fallback 路径用
	claudePids := make([]int, 0, len(procs))
	for _, p := range procs {
		claudePids = append(claudePids, p.pid)
	}
	children := enumerateChildren(claudePids) // 各 claude.exe 的子进程,供 busy 判定
	usedSession := make(map[string]bool)
	alivePids := make(map[int]bool, len(procs))

	for _, p := range procs {
		alivePids[p.pid] = true

		// 过滤非交互式 claude(doctor/mcp serve/--version 等):它们不渲染 statusline,
		// 无 live 数据,不应作为监控实例。
		if isNonInteractive(p.pid) {
			continue
		}

		rec, mtime, fresh, hasLive := ReadLive(p.pid, now)

		var inst Instance
		switch {
		case hasLive && fresh:
			inst = buildInstanceFromLive(p, rec, mtime, now)
		case hasLive:
			// live 存在但不新鲜(idle 会话):实时 token/cost 已过期,但归属
			// (sessionId/transcriptPath)仍可信,必须用它读 jsonl——否则 matchSession
			// 会把同 cwd 的多个旧会话都错配到最新会话,导致不同实例显示同一会话。
			inst = buildInstanceFromStaleLive(p, rec, mtime, now)
		default:
			inst = buildInstanceFallback(p, sessionsByCwd, usedSession, now)
		}

		// 融合判定 busy/idle:statusline 流式信号 + 进程树工具子进程 + 滞回。
		// Claude Code 在执行子进程类工具期间不调用 statusline(live 文件停止刷新),
		// 单靠 mtime 会把"正在跑工具"误判为 idle——靠子进程信号补救,滞回消除抖动。
		streaming := hasLive && now-mtime < liveBusyMs
		inst.Status = fusedStatus(p.pid, streaming, hasToolChild(p.pid, children), hasLive || inst.HasConversation, now)

		// 缓存 SessionInfo 供 GetChatHistory(pid) 复用(含 transcriptPath,读历史用)
		si := &SessionInfo{
			Pid:            p.pid,
			SessionID:      inst.SessionID,
			Cwd:            inst.Cwd,
			StartedAt:      p.createMs,
			Status:         inst.Status,
			TranscriptPath: inst.TranscriptPath,
		}
		cacheMu.Lock()
		lastInstanceByPid[p.pid] = si
		cacheMu.Unlock()

		live = append(live, inst)
	}

	// 过滤无用实例：没有 live 数据、没有对话、且状态未知的进程。
	// Claude Code 运行时会派生多个子进程（工具沙箱、worker 等），它们也名为 claude.exe，
	// 但没有 session/对话数据，不应出现在监控列表中。
	// 保守策略：只要有 live 数据或对话数据或已匹配到会话（status != unknown）就保留。
	live = filterUseful(live)

	sort.SliceStable(live, func(i, j int) bool {
		if ri, rj := StatusRank(live[i].Status), StatusRank(live[j].Status); ri != rj {
			return ri < rj
		}
		return live[i].Pid < live[j].Pid
	})

	// 清理已退出进程残留的 live 文件
	CleanLiveFiles(alivePids)
	// 清理已退出进程的 busy 滞回残留
	cacheMu.Lock()
	for pid := range lastBusyAt {
		if !alivePids[pid] {
			delete(lastBusyAt, pid)
		}
	}
	cacheMu.Unlock()
	cleanConvCache()
	return live, stale, nil
}

// nonInteractiveCmdKws:命令行含这些子串的 claude 进程视为非交互式,过滤掉。
// 注意不含 --dangerously-skip-permissions(正常交互会话常用)。
var nonInteractiveCmdKws = []string{
	"doctor",
	"mcp serve",
	"mcp ",
	"--version",
	" -v ",
	"version --check",
	" update",
	"config ",
	"setup-token",
}

// isNonInteractive 判断 pid 是否为非交互式 claude 进程。
// 读不到命令行时返回 false(保守,不误杀正常实例)。
func isNonInteractive(pid int) bool {
	cmd := processCmdline(pid)
	if cmd == "" {
		return false
	}
	low := strings.ToLower(cmd)
	for _, kw := range nonInteractiveCmdKws {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

// filterUseful 过滤掉无任何有效数据的实例（Claude Code 子进程/工具沙箱等）。
// 保守策略：只要有一个有效信号（live / 对话 / 会话匹配）就保留。
// 全无的进程通常是 Claude Code 的内部 worker 或未识别的 Claude Desktop 进程。
func filterUseful(insts []Instance) []Instance {
	out := insts[:0]
	for _, inst := range insts {
		if inst.Live || inst.HasConversation || inst.Status != "unknown" {
			out = append(out, inst)
		}
	}
	return out
}

// fusedStatus 综合 statusline 流式信号 + 进程树工具执行信号 + 滞回,判定 busy/idle/unknown。
//
// 背景:Claude Code 在执行子进程类工具期间不调用 statusline,live 文件停止刷新,
// 单靠 mtime 会把"正在跑工具"误判为 idle。引入进程树信号补救,并用滞回消除抖动:
//   streaming    —— live 文件新鲜(statusline 正在刷新 = 模型流式输出/TUI 活跃)
//   hasToolChild —— claude.exe 有非 MCP 的直接子进程(正在执行 Bash 等阻塞型工具)
//   hasSignal    —— 有 live 或对话数据(用于区分 idle 与"无任何数据"的 unknown)
func fusedStatus(pid int, streaming, hasToolChild, hasSignal bool, nowMs int64) string {
	if streaming || hasToolChild {
		cacheMu.Lock()
		lastBusyAt[pid] = nowMs
		cacheMu.Unlock()
		return "busy"
	}
	// 失去 busy 信号后宽限一段时间仍算 busy,平滑 statusline 零星刷新的空窗
	cacheMu.RLock()
	lb := lastBusyAt[pid]
	cacheMu.RUnlock()
	if lb > 0 && nowMs-lb < busyGraceMs {
		return "busy"
	}
	if hasSignal {
		return "idle"
	}
	return "unknown"
}

// hasToolChild 判定 pid 是否有"正在执行工具"的直接子进程。
// 排除常驻进程:MCP server(命令行含 mcp/--stdio)与我们的 statusline hook(bridge.mjs/
// claude-monitor-sl,statusline 刷新时的瞬时 node 子进程)。这些在 idle 实例上也常驻,
// 不排除会永远误判 busy。读不到命令行(权限等)保守跳过,不据此判 busy。
func hasToolChild(pid int, children map[int][]int) bool {
	for _, child := range children[pid] {
		cmd := strings.ToLower(processCmdline(child))
		if cmd == "" || isExcludedChild(cmd) {
			continue
		}
		return true
	}
	return false
}

// isExcludedChild 判定子进程命令行是否属于常驻/自身进程(不应计为工具执行)。
func isExcludedChild(cmd string) bool {
	return strings.Contains(cmd, "mcp") ||
		strings.Contains(cmd, "--stdio") ||
		strings.Contains(cmd, "bridge.mjs") ||
		strings.Contains(cmd, "claude-monitor-sl")
}

// buildInstanceFromLive 用 statusline 桥接的实时数据构建实例(live 文件新鲜)。
// 这是 2.1.177+ 的主路径:数据实时、归属准确(transcriptPath 来自官方)。
func buildInstanceFromLive(p claudeProc, rec LiveRecord, mtimeMs, nowMs int64) Instance {
	si := &SessionInfo{
		Pid:            p.pid,
		SessionID:      rec.SessionID,
		Cwd:            rec.Cwd,
		StartedAt:      p.createMs,
		TranscriptPath: rec.TranscriptPath,
	}
	inst := buildInstance(p.pid, si) // 读 jsonl 历史(用 transcriptPath,归属准确)
	inst.UpdatedAt = mtimeMs
	inst.BridgeConnected = true
	inst.Live = true
	inst.TranscriptPath = rec.TranscriptPath

	// live 实时字段覆盖 jsonl 读取值(jsonl 可能滞后/未落盘)
	if rec.Cwd != "" {
		inst.Cwd = rec.Cwd
	}
	if rec.Model != "" {
		inst.Model = rec.Model
	}
	if rec.SessionName != "" {
		inst.Topic = rec.SessionName // statusline 的 session_name(jsonl 未落盘时的实时主题)
	}
	inst.ContextTokens = rec.ContextTokens
	inst.ContextPercent = rec.ContextPercent
	if rec.ContextLimit > 0 {
		inst.ContextLimit = rec.ContextLimit
	} else {
		inst.ContextLimit = ModelContextLimit(inst.Model)
	}
	if rec.Version != "" {
		inst.Version = rec.Version
	}
	inst.CostUsd = rec.CostUsd
	inst.DurationMs = rec.DurationMs
	// 有 live 实时数据时,即使 jsonl 尚未落盘也视为有会话,让前端显示 model/context
	if rec.ContextTokens > 0 || rec.Model != "" {
		inst.HasConversation = true
	}
	si.Status = inst.Status
	return inst
}

// buildInstanceFromStaleLive 处理 live 文件存在但不新鲜的实例(idle 会话)。
// 实时 token/cost 已过期(前端不展示实时数字),但归属信息(sessionId/transcriptPath)
// 仍然有效——用它读 jsonl 历史,避免 matchSession 把同 cwd 的旧会话错配到最新会话。
func buildInstanceFromStaleLive(p claudeProc, rec LiveRecord, mtimeMs, nowMs int64) Instance {
	si := &SessionInfo{
		Pid:            p.pid,
		SessionID:      rec.SessionID,
		Cwd:            rec.Cwd,
		StartedAt:      p.createMs,
		TranscriptPath: rec.TranscriptPath,
	}
	inst := buildInstance(p.pid, si)
	inst.UpdatedAt = mtimeMs
	inst.BridgeConnected = true // 桥接生效过(有 live),只是当前 idle 不再频繁刷新
	inst.Live = false
	inst.TranscriptPath = rec.TranscriptPath
	if rec.Cwd != "" {
		inst.Cwd = rec.Cwd
	}
	if rec.Model != "" {
		inst.Model = rec.Model
	}
	if rec.Version != "" {
		inst.Version = rec.Version
	}
	si.Status = inst.Status
	return inst
}

// buildInstanceFallback 在无新鲜 live 文件时构建实例(桥接未生效/实例刚启动)。
// 回退到旧的 cwd+mtime 猜测;前端会标注"未接入实时"。
func buildInstanceFallback(p claudeProc, sessionsByCwd map[string][]sessionMeta, used map[string]bool, nowMs int64) Instance {
	cwd := procCwd(p.pid)
	sid, meta := matchSession(cwd, sessionsByCwd, used, nowMs)
	si := &SessionInfo{
		Pid:       p.pid,
		SessionID: sid,
		Cwd:       cwd,
		StartedAt: p.createMs,
	}
	inst := buildInstance(p.pid, si)
	if meta != nil {
		inst.UpdatedAt = meta.mtimeMs
	} else {
		inst.UpdatedAt = p.createMs
	}
	inst.BridgeConnected = false
	inst.Live = false
	si.Status = inst.Status
	return inst
}

// GetStats 返回当前实例的统计摘要。
func GetStats() StatsInfo {
	live, stale, _ := Detect()
	offline := 0
	for _, inst := range live {
		if !inst.Live {
			offline++
		}
	}
	return StatsInfo{
		Online:      len(live),
		Busy:        CountStatus(live, "busy"),
		Idle:        CountStatus(live, "idle"),
		Stale:       len(stale),
		Offline:     offline,
		TotalTokens: TotalTokens(live),
	}
}

// indexProjectSessions 遍历 ~/.claude/projects/*/*.jsonl，按 encoded-cwd 分组返回轻量元信息。
// 只读文件名与 mtime（不解析内容），供 pid↔sessionId 匹配；详细内容在 buildInstance 时按需读取。
func indexProjectSessions() map[string][]sessionMeta {
	m := map[string][]sessionMeta{}
	projectsDir := filepath.Join(claudeDir(), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return m
	}
	for _, enc := range entries {
		if !enc.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projectsDir, enc.Name()))
		if err != nil {
			continue
		}
		var metas []sessionMeta
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			metas = append(metas, sessionMeta{
				sessionID: strings.TrimSuffix(f.Name(), ".jsonl"),
				mtimeMs:   info.ModTime().UnixMilli(),
			})
		}
		if len(metas) > 0 {
			m[enc.Name()] = metas
		}
	}
	return m
}

// matchSession 在进程 cwd 对应的 jsonl 集合中匹配一个 sessionId。
// 策略：优先未被独占且 mtime 最新（最活跃）的；全部被独占时（同 cwd 进程数 > jsonl 数）
// 取 mtime 最新的共享展示，不标记 used。
func matchSession(cwd string, sessionsByCwd map[string][]sessionMeta, used map[string]bool, nowMs int64) (string, *sessionMeta) {
	if cwd == "" {
		return "", nil
	}
	metas, ok := sessionsByCwd[encodeProjectPath(cwd)]
	if !ok || len(metas) == 0 {
		return "", nil
	}
	var best *sessionMeta
	bestAge := int64(1 << 62)
	for i := range metas {
		m := &metas[i]
		if used[m.sessionID] {
			continue
		}
		if age := nowMs - m.mtimeMs; age < bestAge {
			bestAge = age
			best = m
		}
	}
	if best != nil {
		used[best.sessionID] = true
		return best.sessionID, best
	}
	// 候选均已被独占：取 mtime 最新的共享
	latest := &metas[0]
	for i := range metas {
		if metas[i].mtimeMs > latest.mtimeMs {
			latest = &metas[i]
		}
	}
	return latest.sessionID, latest
}

func buildInstance(pid int, s *SessionInfo) Instance {
	inst := Instance{Pid: pid, Status: "unknown"}
	if s != nil {
		inst.Status = s.Status
		if inst.Status == "" {
			inst.Status = "unknown"
		}
		inst.Cwd = s.Cwd
		inst.GitBranch = detectGitBranch(s.Cwd)
		inst.Version = s.Version
		inst.SessionID = s.SessionID
		inst.StartedAt = s.StartedAt
		inst.UpdatedAt = s.UpdatedAt
		inst.HasSession = true

		d := loadConversationDetails(s)
		inst.HasConversation = d.hasFile
		inst.Model = d.model
		if d.version != "" {
			inst.Version = d.version // JSONL 顶层 version 比 session 记录更准
		}
		inst.ContextTokens = d.context
		inst.OutputTokens = d.output
		inst.Topic = d.topic
		inst.TotalInputTokens = d.totalInputTokens
		inst.TotalOutputTokens = d.totalOutputTokens
		inst.TotalCacheTokens = d.totalCacheTokens
		inst.LastUserQuery = d.lastUserQuery
		inst.LastReplySnip = d.lastReplySnip
		inst.Turns = d.turns
		inst.LastTool = d.lastTool
		inst.History = d.history
		inst.HistoryHash = d.historyHash
	}
	// JSONL 还没有模型信息时，fallback 到 settings.json 的 ANTHROPIC_MODEL
	if inst.Model == "" && configModel != "" {
		inst.Model = configModel
	}
	inst.ContextLimit = ModelContextLimit(inst.Model)
	return inst
}
