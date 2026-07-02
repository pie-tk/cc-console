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
	isProcessAlive    func(pid int, startedAt int64) bool  // 单 pid 存活验证（保留供其他场景）
	enumerateClaude   func() []claudeProc                  // 枚举所有 claude.exe 进程
	procCwd           func(pid int) string                 // 读进程工作目录
	enumerateChildren func(claudePids []int) map[int][]int // 各 claude.exe 的直接子进程 pid（供 busy 判定，非 Win 为 nil）
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

// liveRead 缓存一次 ReadLive 的结果，供 Detect 两遍扫描复用，避免对同一 live 文件重复读+解析。
type liveRead struct {
	rec   LiveRecord
	mtime int64
	fresh bool
	ok    bool
}

type hookRead struct {
	state HookState
	mtime int64
	ok    bool
}

// busyGraceMs：无 lifecycle hook 时，失去 busy 信号(statusline 停止刷新且无工具子进程)后仍保持 busy 的宽限期。
// hook 驱动会话不再依赖这段长滞回，只给 legacy fallback 用。
const busyGraceMs int64 = 6000

// hookFreshMs：hook 状态文件在该窗口内视为当前有效；超时则回退到旧 heuristic。
const hookFreshMs int64 = 120000

// Detect 返回当前存活的 Claude Code 实例。
//
// Claude Code 2.1.177+ 不再写 ~/.claude/sessions/<pid>.json，且整个 .claude 目录不持久化
// 任何 pid。因此实例发现改为以 claude.exe 进程为锚点：枚举进程拿 pid → 读进程工作目录 →
// 关联 projects 下 jsonl 取 model/tokens/history 等展示信息。pid 是唯一可信主键（输入注入
// 等操作按 pid 精确工作）；busy/idle 由 jsonl 文件活跃度推断。
// Detect 返回当前存活的 Claude Code 实例。
//
// Claude Code 2.1.169 引入的 regression(Issue #66486):交互式会话不再实时落盘 jsonl,
// 实时数据改通过 statusline 桥接获取——cc-console-sl.exe 把每个会话的实时状态写到
// ~/.cc-console/live/<pid>.json。本函数以 claude.exe 进程为锚点枚举 pid → 读对应 live
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

	// Pass 1：预读所有实例的 live 文件，收集「已被 live 实例精确占用的 sessionId」。
	// 走 live 路径的实例用官方 transcriptPath 读自己的会话，不调 matchSession、原先不标记 used——
	// 导致同 cwd 的无 live 实例（新建实例启动窗口期 / 未接入桥接）在 fallback 的 matchSession 里
	// 抢到 mtime 最新的 jsonl，显示别人的对话记录。这里把 live 占用的 sessionId 收集起来，
	// 供 Pass 2 的 fallback 从候选池排除。
	liveOwned := make(map[string]bool)
	liveReads := make(map[int]liveRead, len(procs))
	hookReads := make(map[int]hookRead, len(procs))
	for _, p := range procs {
		rec, mtime, fresh, hasLive := ReadLive(p.pid, now)
		liveReads[p.pid] = liveRead{rec: rec, mtime: mtime, fresh: fresh, ok: hasLive}
		if hasLive && rec.TranscriptPath != "" {
			sid := strings.TrimSuffix(filepath.Base(rec.TranscriptPath), ".jsonl")
			if sid != "" {
				liveOwned[sid] = true
			}
		}
		if hs, hm, ok := ReadHookState(p.pid); ok {
			hookReads[p.pid] = hookRead{state: hs, mtime: hm, ok: true}
		}
	}

	for _, p := range procs {
		alivePids[p.pid] = true

		// 过滤非交互式 claude(doctor/mcp serve/--version 等):它们不渲染 statusline,
		// 无 live 数据,不应作为监控实例。
		if isNonInteractive(p.pid) {
			continue
		}

		lr := liveReads[p.pid]
		rec, mtime, fresh, hasLive := lr.rec, lr.mtime, lr.fresh, lr.ok

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
			inst = buildInstanceFallback(p, sessionsByCwd, usedSession, liveOwned, now)
		}

		// 生命周期 hook 状态优先，statusline/子进程信号退化为兜底。
		streaming := hasLive && now-mtime < liveBusyMs
		hr := hookReads[p.pid]
		inst.Status = fusedStatus(p.pid, hr.state, hr.ok, hr.mtime, streaming, hasToolChild(p.pid, children), hasLive || inst.HasConversation, now)

		// 缓存 SessionInfo 供 GetChatHistory(pid) 复用(含 transcriptPath,读历史用)
		si := &SessionInfo{
			Pid:            p.pid,
			SessionID:      inst.SessionID,
			Cwd:            inst.Cwd,
			StartedAt:      p.createMs,
			Status:         inst.Status,
			UpdatedAt:      inst.UpdatedAt,
			TranscriptPath: inst.TranscriptPath,
		}
		inst.WaitingKind = detectInstanceWaitingKind(si, inst.WaitingKind)
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

	// 清理已退出进程残留的 live / hook / ask 文件
	CleanLiveFiles(alivePids)
	CleanHookFiles(alivePids)
	CleanAskFiles(alivePids)
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

// fusedStatus 优先采用 lifecycle hook 的权威状态；缺失/过期时回退到旧 heuristic。
func fusedStatus(pid int, hook HookState, hasHook bool, hookMtimeMs int64, streaming, hasToolChild, hasSignal bool, nowMs int64) string {
	if hasHook && nowMs-hookMtimeMs < hookFreshMs {
		switch hook.Phase {
		case "busy":
			return "busy"
		case "idle":
			if hasSignal || hook.LastEvent == "Stop" {
				return "idle"
			}
		}
	}
	if streaming || hasToolChild {
		cacheMu.Lock()
		lastBusyAt[pid] = nowMs
		cacheMu.Unlock()
		return "busy"
	}
	// 失去 busy 信号后宽限一段时间仍算 busy，仅用于无 hook 的 legacy 路径平滑空窗。
	cacheMu.RLock()
	lb := lastBusyAt[pid]
	cacheMu.RUnlock()
	if lb > 0 && nowMs-lb < busyGraceMs {
		return "busy"
	}
	if hasSignal || hasHook {
		return "idle"
	}
	return "unknown"
}

// hasToolChild 判定 pid 是否有"正在执行工具"的直接子进程。
// 排除常驻进程:MCP server(命令行含 mcp/--stdio)与我们的 statusline hook(bridge.mjs/
// cc-console-sl,statusline 刷新时的瞬时 node 子进程)。这些在 idle 实例上也常驻,
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
		strings.Contains(cmd, "cc-console-sl")
}

// liveModelName 返回 live 记录里可用于展示的模型名：优先显示名，缺失时退回 modelId。
// 某些 statusline 刷新会间歇性丢 display_name；此时至少保留可识别的真实模型 ID。
func liveModelName(rec LiveRecord) string {
	if rec.Model != "" {
		return rec.Model
	}
	return rec.ModelID
}

// liveModelLimit 返回 live 记录对应的上下文上限：优先使用 statusline 原生值，
// 缺失时按真实 modelId 推断，最后才退回 display name。
// 这样可避免 "Sonnet 4.6" 这类展示名查表失败后误回退到默认 200K。
func liveModelLimit(rec LiveRecord) int64 {
	if rec.ContextLimit > 0 {
		return rec.ContextLimit
	}
	if rec.ModelID != "" {
		return ModelContextLimit(rec.ModelID)
	}
	return ModelContextLimit(rec.Model)
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
	if liveModelName(rec) != "" {
		inst.Model = liveModelName(rec)
	}
	if rec.SessionName != "" {
		inst.Topic = rec.SessionName // statusline 的 session_name(jsonl 未落盘时的实时主题)
	}
	inst.ContextTokens = rec.ContextTokens
	inst.ContextPercent = rec.ContextPercent
	inst.ContextLimit = liveModelLimit(rec)
	if rec.Version != "" {
		inst.Version = rec.Version
	}
	inst.CostUsd = rec.CostUsd
	inst.DurationMs = rec.DurationMs
	if hs, _, ok := ReadHookState(p.pid); ok {
		inst.TaskStartedAt = hs.TaskStartedAt
	}
	// 有 live 实时数据时,即使 jsonl 尚未落盘也视为有会话,让前端显示 model/context
	if rec.ContextTokens > 0 || rec.Model != "" {
		inst.HasConversation = true
	}
	si.Status = inst.Status
	return inst
}

// buildInstanceFromStaleLive 处理 live 文件存在但不新鲜的实例(idle 会话)。
// 归属信息(sessionId/transcriptPath)仍然有效——用它读 jsonl 历史,避免 matchSession
// 把同 cwd 的旧会话错配到最新会话。context 用 JSONL 优先；若活跃会话不落盘导致
// JSONL 解析不到 usage,则保留 statusline 最后一次上报的 context,避免 idle 后显示为 "—"。
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
	if liveModelName(rec) != "" {
		inst.Model = liveModelName(rec)
		inst.ContextLimit = liveModelLimit(rec)
	}
	if rec.Version != "" {
		inst.Version = rec.Version
	}
	// 活跃 Claude Code 会话在 idle 后可能不会把最后一轮 usage 及时写回 JSONL。
	// 这类会话通过 stale live 仍能拿到 statusline 最后一次有效 context；仅在
	// JSONL 没有 context 时兜底,避免把已知用量覆盖成 0/"—"。
	if inst.ContextTokens <= 0 && rec.ContextTokens > 0 {
		inst.ContextTokens = rec.ContextTokens
		inst.ContextPercent = rec.ContextPercent
		if rec.ContextLimit > 0 {
			inst.ContextLimit = rec.ContextLimit
		}
		inst.CostUsd = rec.CostUsd
		inst.DurationMs = rec.DurationMs
		inst.HasConversation = true
	}
	si.Status = inst.Status
	return inst
}

// buildInstanceFallback 在无新鲜 live 文件时构建实例(桥接未生效/实例刚启动)。
// 回退到旧的 cwd+mtime 猜测;前端会标注"未接入实时"。
func buildInstanceFallback(p claudeProc, sessionsByCwd map[string][]sessionMeta, used map[string]bool, liveOwned map[string]bool, nowMs int64) Instance {
	cwd := procCwd(p.pid)
	sid, meta := matchSession(cwd, sessionsByCwd, used, liveOwned, nowMs)
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

func detectInstanceWaitingKind(si *SessionInfo, fallback string) string {
	if si == nil || si.Pid <= 0 {
		return fallback
	}
	if _, ok := ReadAsk(si.Pid); ok {
		return "ask"
	}
	// buildInstance 初读 JSONL 时 status 还未经过 fusedStatus，permission 等待需用最终状态重算。
	if si.Status == "idle" || fallback == "ask" || fallback == "plan" {
		result := GetChatHistory(si)
		if kind := DetectPendingInteraction(result.Messages, si.Status); kind != "" {
			return kind
		}
	}
	return ""
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
//
//	used       —— 已被本轮其他 fallback 实例独占的 sessionId（同 cwd 多实例互斥）
//	liveOwned  —— 已被 live 实例（statusline 桥接）精确占用的 sessionId
//
// liveOwned 的候选完全排除在外：它们有明确归属，被无 live 的新建实例抢走会导致
// 「新建实例显示了别人的会话记录」。策略：在「未被 live 占用且未被独占」的候选里取
// mtime 最新；全被独占则取（非 live 占用的）最新共享；若该 cwd 的 jsonl 全被 live 实例
// 占用，说明本实例确无自己的会话（刚启动、尚未落盘），返回空，让前端显示启动中状态。
func matchSession(cwd string, sessionsByCwd map[string][]sessionMeta, used map[string]bool, liveOwned map[string]bool, nowMs int64) (string, *sessionMeta) {
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
		if liveOwned[m.sessionID] {
			continue
		}
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
	// 候选均已被 fallback 实例独占：取（未被 live 占用的）mtime 最新共享
	var latest *sessionMeta
	for i := range metas {
		if liveOwned[metas[i].sessionID] {
			continue
		}
		if latest == nil || metas[i].mtimeMs > latest.mtimeMs {
			latest = &metas[i]
		}
	}
	if latest != nil {
		return latest.sessionID, latest
	}
	// 该 cwd 的 jsonl 全被 live 实例占用：本实例还没有自己的会话（刚启动、尚未落盘）
	return "", nil
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
		inst.WaitingKind = d.waitingKind
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
