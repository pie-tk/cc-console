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
	// 会话动态信息（主题行右侧）
	lastUserQuery string // 最近一条真实用户提问（排除 tool_result 回显）
	lastReplySnip string // 最近一条 assistant text 块片段
	turns         int    // 消息轮数（含 text 块的 user 消息计数）
	lastTool      string         // 最近使用的工具名（最后一个 tool_use 的 name）
	history       []HistoryTurn  // 所有 Q&A 轮次（最多 30 轮）
	historyHash   int            // 对话历史内容哈希，前端用于判断是否需要重建 DOM
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

// parseConversation 单次遍历对话文件，累积 Q&A 轮次、取模型/用量/主题。
func parseConversation(data []byte, d *convDetails) {
	firstUserSet := false
	var firstUser string
	var pendingUser string   // 当前轮次的用户提问
	var pendingReply string  // 当前轮次累积的助手回复
	var inTurn bool          // 是否有待完成的轮次

	finalizeTurn := func() {
		if inTurn && pendingUser != "" {
			d.history = append(d.history, HistoryTurn{
				UserQuery: TruncateRunes(pendingUser, 80),
				ReplySnip: TruncateRunes(pendingReply, 120),
			})
		}
		inTurn = false
		pendingUser = ""
		pendingReply = ""
	}

	for _, raw := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		switch {
		case bytes.Contains(line, []byte(`"type":"assistant"`)):
			var cl struct {
				Message struct {
					Model   string     `json:"model"`
					Usage   *usageInfo `json:"usage"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
						Name string `json:"name"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &cl) == nil {
				if cl.Message.Usage != nil {
					u := cl.Message.Usage
					d.model = cl.Message.Model
					d.context = int64(u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens)
					d.output = int64(u.OutputTokens)
					d.totalInputTokens += int64(u.InputTokens)
					d.totalOutputTokens += int64(u.OutputTokens)
					d.totalCacheTokens += int64(u.CacheCreationInputTokens + u.CacheReadInputTokens)
				}
				for _, b := range cl.Message.Content {
					if b.Type == "text" && b.Text != "" {
						d.lastReplySnip = b.Text
						// 累积到当前轮次的助手回复
						if inTurn {
							if pendingReply == "" {
								pendingReply = b.Text
							} else {
								pendingReply += "\n" + b.Text
							}
						}
					}
					if b.Type == "tool_use" && b.Name != "" {
						d.lastTool = b.Name
					}
				}
			}
		case bytes.Contains(line, []byte(`"type":"ai-title"`)):
			var at struct {
				AiTitle string `json:"aiTitle"`
			}
			if json.Unmarshal(line, &at) == nil && at.AiTitle != "" {
				d.topic = at.AiTitle
			}
		case bytes.Contains(line, []byte(`"type":"user"`)):
			t := extractUserText(line)
			if !firstUserSet {
				if t != "" {
					firstUser = t
				}
				firstUserSet = true
			}
			if t != "" {
				// 新轮次开始前，先完成上一轮
				finalizeTurn()
				pendingUser = t
				inTurn = true
				d.turns++
				d.lastUserQuery = t
			}
		}
	}

	// 完成最后一轮
	finalizeTurn()

	// 限制最多 30 轮
	if len(d.history) > 30 {
		d.history = d.history[len(d.history)-30:]
	}

	// 回填兼容字段（从 history 最后一项）
	// 注意：当最后一轮只有提问尚无回复时，不覆盖 lastReplySnip——它可能已由
	// assistant 消息解析设好，覆盖会导致回复在下一轮提问前丢失。
	if n := len(d.history); n > 0 {
		d.lastUserQuery = d.history[n-1].UserQuery
		if d.history[n-1].ReplySnip != "" {
			d.lastReplySnip = d.history[n-1].ReplySnip
		}
	}
	// 计算历史内容哈希——前端用此判断是否需要重建 DOM，因为 assistant 回复
	// 追加到已有轮次时 turns 计数不变，单纯比较 turns 会漏掉更新。
	for _, h := range d.history {
		d.historyHash += len(h.UserQuery)*31 + len(h.ReplySnip)*17
	}

	if d.topic == "" && firstUser != "" {
		d.topic = TruncateRunes(firstUser, 60)
	}
	d.lastUserQuery = TruncateRunes(d.lastUserQuery, 80)
	d.lastReplySnip = TruncateRunes(d.lastReplySnip, 120)
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
