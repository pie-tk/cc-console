package monitor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

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
	// 累计 token（所有 assistant 消息求和）
	totalInputTokens  int64
	totalOutputTokens int64
	totalCacheTokens  int64 // cache_creation + cache_read
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
				// 累加所有 assistant 消息的 token
				d.totalInputTokens += int64(u.InputTokens)
				d.totalOutputTokens += int64(u.OutputTokens)
				d.totalCacheTokens += int64(u.CacheCreationInputTokens + u.CacheReadInputTokens)
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
		d.topic = TruncateRunes(firstUser, 60)
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

// cleanConvCache 清理文件已不存在的缓存条目，防止长时间运行后缓存无限增长。
// 由 Detect() 每次调用时触发。
func cleanConvCache() {
	for path := range convCache {
		if _, err := os.Stat(path); err != nil {
			delete(convCache, path)
		}
	}
}
