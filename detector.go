package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

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

	// 清理已失效的对话缓存
	cleanConvCache()

	return live, stale, nil
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
