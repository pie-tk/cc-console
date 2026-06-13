package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// statusline 桥接:利用 Claude Code 的 statusLine 通道获取活跃会话的实时数据。
//
// 背景:Claude Code 2.1.177+ 活跃会话不落盘 jsonl,实时 token/上下文只能通过
// statusline 通道获取。claude-monitor-sl.exe 作为 statusLine 命令,把每次刷新推送的
// 状态落盘到 ~/.claude-monitor/live/<pid>.json,本包负责读取这些文件并在 Detect 中
// 按 pid 精确还原每个实例的实时状态。
//
// 本文件含跨平台逻辑(live 文件读写 + settings.json 的 statusLine 字段操作)。
// 平台特定的 processCmdline 在 bridge_windows.go / bridge_other.go。

// LiveRecord 对应 slhook 写入的 live/<pid>.json(两端 JSON tag 必须一致)。
type LiveRecord struct {
	Pid            int    `json:"pid"`
	Ts             int64  `json:"ts"` // slhook 写入时刻(epoch ms)
	SessionID      string `json:"sessionId"`
	SessionName    string `json:"sessionName"`
	Model          string `json:"model"`
	ModelID        string `json:"modelId"`
	Cwd            string `json:"cwd"`
	TranscriptPath string `json:"transcriptPath"`
	ContextTokens  int64  `json:"contextTokens"`
	ContextPercent int    `json:"contextPercent"`
	ContextLimit   int64   `json:"contextLimit"`
	Version        string  `json:"version"`
	CostUsd        float64 `json:"costUsd"`
	DurationMs     int64   `json:"durationMs"`
}

const (
	liveFreshMs int64 = 60000 // live 文件 mtime 距 now < 60s 视为新鲜(idle 时 statusline 刷新频率低,放宽容忍)
	liveBusyMs  int64 = 3000  // live 文件 mtime 距 now < 3s 视为 busy(statusline 正频繁刷新)
)

const (
	slhookExeName = "claude-monitor-sl.exe"
	bridgeMjsName = "bridge.mjs"
	slhookMarker  = "bridge.mjs" // statusLine.command 含此串即表示已由我们接管(node 入口)
)

// processCmdline 返回 pid 的命令行(小写),失败返回 ""。平台特定。
var processCmdline func(pid int) string

// claudeMonitorDir 返回 ~/.claude-monitor/。
func claudeMonitorDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude-monitor")
}

// LiveDir 返回 live 文件目录。
func LiveDir() string {
	d := claudeMonitorDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "live")
}

// LivePath 返回 pid 对应的 live 文件路径。
func LivePath(pid int) string {
	d := LiveDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, strconv.Itoa(pid)+".json")
}

// ReadLive 读取并解析 pid 的 live 文件。
// 返回:记录、文件 mtime(epoch ms)、是否新鲜、是否成功解析。
func ReadLive(pid int, nowMs int64) (rec LiveRecord, mtimeMs int64, fresh bool, ok bool) {
	path := LivePath(pid)
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mtimeMs = info.ModTime().UnixMilli()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if json.Unmarshal(data, &rec) != nil {
		return
	}
	ok = true
	fresh = nowMs-mtimeMs < liveFreshMs
	return
}

// CleanLiveFiles 删除不在 alivePids 中的 live 文件(对应进程已退出)。
func CleanLiveFiles(alivePids map[int]bool) {
	dir := LiveDir()
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		if !alivePids[pid] {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// ---- settings.json 的 statusLine 操作 ----

func claudeSettingsPath() string {
	d := claudeDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "settings.json")
}

func origStatuslinePath() string {
	d := claudeMonitorDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "orig-statusline.json")
}

// slhookExePath 返回监控器 exe 同目录下的 claude-monitor-sl.exe 绝对路径(不存在则空)。
func slhookExePath() string {
	dir := monitorExeDir()
	if dir == "" {
		return ""
	}
	p := filepath.Join(dir, slhookExeName)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// bridgeMjsPath 返回监控器 exe 同目录下的 bridge.mjs 绝对路径(不存在则空)。
func bridgeMjsPath() string {
	dir := monitorExeDir()
	if dir == "" {
		return ""
	}
	p := filepath.Join(dir, bridgeMjsName)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// monitorExeDir 返回监控器 exe 所在目录。
func monitorExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// readSettingsJSON 读取并解析 settings.json,容忍 UTF-8 BOM(PowerShell 等工具可能写入)。
func readSettingsJSON(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil // 不存在 → 空,EnsureBridge 会创建
		}
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // 去 UTF-8 BOM
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}

// getStatuslineCommand 从解析后的 settings.json map 中取 statusLine.command。
func getStatuslineCommand(cfg map[string]any) string {
	sl, ok := cfg["statusLine"].(map[string]any)
	if !ok {
		return ""
	}
	cmd, _ := sl["command"].(string)
	return cmd
}

func saveOrigStatusline(cmd string) {
	p := origStatuslinePath()
	if p == "" || cmd == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	data, _ := json.MarshalIndent(struct {
		Command string `json:"command"`
	}{Command: cmd}, "", "  ")
	_ = os.WriteFile(p, data, 0o644)
}

func readOrigStatusline() string {
	data, err := os.ReadFile(origStatuslinePath())
	if err != nil {
		return ""
	}
	var o struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(data, &o) != nil {
		return ""
	}
	return o.Command
}

// EnsureBridge 把 ~/.claude/settings.json 的 statusLine 指向 slhook,并备份原值。
// 幂等:已指向自己则不动作。返回 (是否做了修改, error)。
func EnsureBridge() (bool, error) {
	bridgePath := bridgeMjsPath()
	if bridgePath == "" {
		return false, fmt.Errorf("未找到 %s(请确认与监控器在同一目录)", bridgeMjsName)
	}
	if slhookExePath() == "" {
		return false, fmt.Errorf("未找到 %s", slhookExeName)
	}
	path := claudeSettingsPath()
	if path == "" {
		return false, fmt.Errorf("无法定位 ~/.claude")
	}

	cfg, err := readSettingsJSON(path)
	if err != nil {
		return false, fmt.Errorf("读取 settings.json 失败: %w", err)
	}

	bridgeFwd := strings.ReplaceAll(bridgePath, `\`, `/`)
	cur := getStatuslineCommand(cfg)
	// 已指向本监控器的 bridge(完整路径匹配)才跳过;不同路径的 bridge.mjs(如开发版残留)会被更新到自己的
	if cur != "" && strings.Contains(strings.ToLower(cur), strings.ToLower(bridgeFwd)) {
		return false, nil
	}

	// 备份原值(仅首次,避免覆盖用户后续手动改动)
	if cur != "" {
		if _, err := os.Stat(origStatuslinePath()); os.IsNotExist(err) {
			saveOrigStatusline(cur)
		}
	}

	// Claude Code 2.1.x 只执行 `node "mjs"` 形式的 statusLine(实测 exe 形式不被调用),
	// 故入口为 bridge.mjs;它再 spawn slhook.exe(写 live)+ 链式原 statusLine。路径用正斜杠。
	cfg["statusLine"] = map[string]any{
		"type":    "command",
		"command": `node "` + bridgeFwd + `"`,
	}
	return writeSettings(path, cfg)
}

// DisableBridge 从 orig-statusline.json 还原 statusLine(无备份则删除该字段)。
func DisableBridge() error {
	path := claudeSettingsPath()
	cfg, err := readSettingsJSON(path)
	if err != nil {
		return err
	}
	cur := getStatuslineCommand(cfg)
	if !strings.Contains(strings.ToLower(cur), slhookMarker) {
		return nil // 已非我们
	}
	if orig := readOrigStatusline(); orig != "" {
		cfg["statusLine"] = map[string]any{"type": "command", "command": orig}
	} else {
		delete(cfg, "statusLine")
	}
	_, err = writeSettings(path, cfg)
	return err
}

// writeSettings 原子写回 settings.json(保留所有字段 + 2 空格缩进,与 Claude Code 一致)。
func writeSettings(path string, cfg map[string]any) (bool, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, err
	}
	return true, nil
}

// BridgeStatus 是前端查询桥接状态的返回结构。
type BridgeStatus struct {
	Enabled   bool   `json:"enabled"`   // 用户设置是否启用桥接
	Installed bool   `json:"installed"` // slhook exe 存在
	Hooked    bool   `json:"hooked"`    // settings.json 的 statusLine 已指向 slhook
	OrigCmd   string `json:"origCmd"`   // 备份的原命令(前端展示/确认用)
}

// GetBridgeStatus 返回当前桥接状态(不依赖 live 文件)。
func GetBridgeStatus() BridgeStatus {
	st := BridgeStatus{
		Enabled:   GetSettings().BridgeEnabled,
		Installed: bridgeMjsPath() != "" && slhookExePath() != "",
	}
	if raw, err := os.ReadFile(claudeSettingsPath()); err == nil {
		var cfg map[string]any
		if json.Unmarshal(raw, &cfg) == nil {
			cmd := getStatuslineCommand(cfg)
			st.Hooked = strings.Contains(strings.ToLower(cmd), slhookMarker)
		}
	}
	st.OrigCmd = readOrigStatusline()
	return st
}
