package monitor

import (
	"os"
	"path/filepath"
	"sort"
)

func claudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// Detect 返回当前存活的 Claude Code 实例，以及残留（进程已退出但 session 文件仍在）的会话。
// session 文件由 Claude Code 启动时写入，pid 天然可信；逐个验证存活 + 启动时间
// （±15s 防 PID 复用），不依赖进程名，覆盖 claude.exe / node.exe 等所有安装方式。
func Detect() (live []Instance, stale []Instance, err error) {
	sessionsDir := filepath.Join(claudeDir(), "sessions")
	sessions := loadSessions(sessionsDir) // map[pid]*SessionInfo

	liveSet := make(map[int]bool)
	for pid, s := range sessions {
		if !isProcessAlive(pid, s.StartedAt) {
			continue
		}
		live = append(live, buildInstance(pid, sessions))
		liveSet[pid] = true
	}

	for pid, s := range sessions {
		if liveSet[pid] {
			continue
		}
		stale = append(stale, instanceFromSession(pid, s))
	}

	sort.SliceStable(live, func(i, j int) bool {
		if ri, rj := StatusRank(live[i].Status), StatusRank(live[j].Status); ri != rj {
			return ri < rj
		}
		return live[i].Pid < live[j].Pid
	})
	sort.SliceStable(stale, func(i, j int) bool {
		return stale[i].Pid < stale[j].Pid
	})

	// 清理已失效的对话缓存
	cleanConvCache()

	return live, stale, nil
}

// GetStats 返回当前实例的统计摘要。
func GetStats() StatsInfo {
	live, stale, _ := Detect()
	return StatsInfo{
		Online:      len(live),
		Busy:        CountStatus(live, "busy"),
		Idle:        CountStatus(live, "idle"),
		Stale:       len(stale),
		TotalTokens: TotalTokens(live),
	}
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
	inst.TotalInputTokens = d.totalInputTokens
	inst.TotalOutputTokens = d.totalOutputTokens
	inst.TotalCacheTokens = d.totalCacheTokens
	inst.LastUserQuery = d.lastUserQuery
	inst.LastReplySnip = d.lastReplySnip
	inst.Turns = d.turns
	inst.LastTool = d.lastTool
	inst.History = d.history
	inst.HistoryHash = d.historyHash
	// JSONL 还没有模型信息时，fallback 到 settings.json 的 ANTHROPIC_MODEL
	if inst.Model == "" && configModel != "" {
		inst.Model = configModel
	}
	inst.ContextLimit = ModelContextLimit(inst.Model)
	return inst
}

func instanceFromSession(pid int, s *SessionInfo) Instance {
	if s == nil {
		return Instance{Pid: pid, Status: "unknown"}
	}
	return buildInstance(pid, map[int]*SessionInfo{pid: s})
}

// isProcessAlive 由平台特定文件实现：验证 pid 存活且创建时间与 startedAt 匹配（±15s 防 PID 复用）。
var isProcessAlive func(pid int, startedAt int64) bool
