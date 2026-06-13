package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SessionInfo 对应 ~/.claude/sessions/<pid>.json 的结构（多余字段忽略）。
type SessionInfo struct {
	Pid        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	StartedAt  int64  `json:"startedAt"`  // epoch 毫秒
	ProcStart  string `json:"procStart"`
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Entrypoint     string `json:"entrypoint"`
	Status         string `json:"status"`         // busy / idle / ...
	UpdatedAt      int64  `json:"updatedAt"`      // epoch 毫秒
	TranscriptPath string `json:"transcriptPath"` // 当前会话 jsonl 官方路径（来自 statusline，优先于 cwd+sessionId 拼接）
}

// loadSessions 读取 ~/.claude/sessions/*.json（旧版 Claude Code 的实例文件）。
// 自 Claude Code 2.1.177 起该文件不再生成；Detect() 已改为进程枚举驱动，不再调用本函数。
// 保留仅供旧版兼容参考。
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
