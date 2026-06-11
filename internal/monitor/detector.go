package monitor

import (
	"os"
	"path/filepath"
	"sort"
)

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
		Online:  len(live),
		Busy:    CountStatus(live, "busy"),
		Idle:    CountStatus(live, "idle"),
		Stale:   len(stale),
		Context: TotalContext(live),
	}
}

// isClaudeCode 判断一个 claude.exe 进程是否为一个真正的 Claude Code 交互实例。
//
// 判定标准：必须有对应的 session 文件（~/.claude/sessions/<pid>.json），且进程
// 启动时间与该会话的 startedAt 一致（容差 15s，排除 PID 复用）。
func isClaudeCode(p procInfo, sessions map[int]*SessionInfo) bool {
	s, ok := sessions[p.pid]
	if !ok {
		return false
	}
	if p.createTimeMs == 0 {
		return false
	}
	return abs64(p.createTimeMs-s.StartedAt) <= 15000
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

// listClaudeProcesses 由平台特定文件实现。
var listClaudeProcesses func() ([]procInfo, error)
