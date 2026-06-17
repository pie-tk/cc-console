package monitor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	version string
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

// Claude Code 在消息文本里嵌入的伪 XML 注解标签。这些标签直接显示在卡片/消息里很突兀，
// 卡片只需"人类可读"的摘要文本，因此用 stripAnnotations 清洗。
var (
	// ansiRe 匹配终端 ANSI 颜色转义（如 \x1b[1m），命令输出里常见，不清洗会显示 [1m 乱码。
	ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")
	// blankLinesRe 压缩多余空行。
	blankLinesRe = regexp.MustCompile(`\n{3,}`)
)

// ccRemoveTags 这些标签连同内容整体移除（系统噪音 / 命令事件 / 思考，非用户真实提问）。
var ccRemoveTags = []string{
	"system-reminder", "env", "user-memory-content",
	"task-notification", "task-reminder", "persisted-output",
	"local-command-caveat", "local-command-stdout", "local-command-stderr",
	"command-name", "command-message", "command-args", "command-body",
	"thinking", "antThinking",
}

// ccUnwrapTags 这些标签保留内文、只去掉外壳（摘录 / Bash 输入输出）。
var ccUnwrapTags = []string{"excerpt", "bash-input", "bash-stdout", "bash-stderr"}

var (
	ccRemoveRe []*regexp.Regexp // 每个 remove 标签一条：开+内容+闭
	ccUnwrapRe []*regexp.Regexp // 每个 unwrap 标签一条：开或闭
	ccLooseRe  *regexp.Regexp   // 兜底：清除残留孤立已知标签
)

func init() {
	for _, t := range ccRemoveTags {
		ccRemoveRe = append(ccRemoveRe, regexp.MustCompile(`(?is)<`+regexp.QuoteMeta(t)+`\b[^>]*>.*?</`+regexp.QuoteMeta(t)+`>`))
	}
	for _, t := range ccUnwrapTags {
		ccUnwrapRe = append(ccUnwrapRe, regexp.MustCompile(`(?i)</?`+regexp.QuoteMeta(t)+`\b[^>]*>`))
	}
	all := append(append([]string{}, ccRemoveTags...), ccUnwrapTags...)
	ccLooseRe = regexp.MustCompile(`(?i)</?(?:` + strings.Join(all, "|") + `)\b[^>]*>`)
}

// stripAnnotations 清洗 Claude Code 注解标签，返回人类可读的纯文本（供卡片的提问/主题/历史摘要使用）。
// 顺序：去 ANSI → 移除噪音块（含内容）→ 剥摘录/Bash 外壳（留内容）→ 清残片 → 压缩空行。
func stripAnnotations(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	for _, re := range ccRemoveRe {
		s = re.ReplaceAllString(s, "")
	}
	for _, re := range ccUnwrapRe {
		s = re.ReplaceAllString(s, "")
	}
	s = ccLooseRe.ReplaceAllString(s, "")
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// transcriptPathFor 返回会话 jsonl 路径:优先用 statusline 提供的官方路径(归属准确),
// 否则回退到 cwd+sessionId 拼接(旧逻辑,无桥接时可能不准)。
func transcriptPathFor(s *SessionInfo) string {
	if s != nil && s.TranscriptPath != "" {
		return s.TranscriptPath
	}
	if s == nil || s.SessionID == "" || s.Cwd == "" {
		return ""
	}
	return filepath.Join(claudeDir(), "projects", encodeProjectPath(s.Cwd), s.SessionID+".jsonl")
}

// loadConversationDetails 从会话对应的 JSONL 中读取模型、token 用量与对话主题。
// 按 mtime 缓存，避免每次刷新都重读大文件。
func loadConversationDetails(s *SessionInfo) convDetails {
	var d convDetails
	path := transcriptPathFor(s)
	if path == "" {
		return d
	}

	info, err := os.Stat(path)
	if err != nil {
		return d // 对话文件不存在 → 新会话
	}
	d.hasFile = true
	mtime := info.ModTime().UnixNano()
	cacheMu.RLock()
	c, ok := convCache[path]
	cacheMu.RUnlock()
	if ok && c.mtime == mtime {
		return c.details
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return d
	}
	parseConversation(data, &d)

	cacheMu.Lock()
	convCache[path] = convCacheEntry{mtime: mtime, details: d}
	cacheMu.Unlock()
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
				Version string `json:"version"`
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
				if cl.Version != "" {
					d.version = cl.Version
				}
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
						txt := stripAnnotations(b.Text)
						d.lastReplySnip = txt
						// 累积到当前轮次的助手回复
						if inTurn {
							if pendingReply == "" {
								pendingReply = txt
							} else {
								pendingReply += "\n" + txt
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
			var uv struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(line, &uv) == nil && uv.Version != "" {
				d.version = uv.Version
			}
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
			return stripAnnotations(s)
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
				return stripAnnotations(b.Text)
			}
		}
	}
	return ""
}

// cleanConvCache 清理文件已不存在的缓存条目，防止长时间运行后缓存无限增长。
// 由 Detect() 每次调用时触发。
func cleanConvCache() {
	cacheMu.Lock()
	for path := range convCache {
		if _, err := os.Stat(path); err != nil {
			delete(convCache, path)
		}
	}
	for path := range chatHistoryCache {
		if _, err := os.Stat(path); err != nil {
			delete(chatHistoryCache, path)
		}
	}
	cacheMu.Unlock()
}

// ---- 聊天面板：完整消息历史解析 ----

type chatHistoryCacheEntry struct {
	mtime  int64
	result ChatHistoryResult
}

var chatHistoryCache = map[string]chatHistoryCacheEntry{}

// GetChatHistory 从 JSONL 文件中提取完整的结构化消息历史（含工具调用/结果）。
// 使用基于 mtime 的独立缓存，避免每次轮询都重读文件。
func GetChatHistory(s *SessionInfo) ChatHistoryResult {
	var result ChatHistoryResult
	path := transcriptPathFor(s)
	if path == "" {
		return result
	}

	info, err := os.Stat(path)
	if err != nil {
		return result
	}
	mtime := info.ModTime().UnixNano()
	cacheMu.RLock()
	c, ok := chatHistoryCache[path]
	cacheMu.RUnlock()
	if ok && c.mtime == mtime {
		return c.result
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	parseChatHistory(data, &result)

	// 缓存写入
	cacheMu.Lock()
	chatHistoryCache[path] = chatHistoryCacheEntry{mtime: mtime, result: result}
	cacheMu.Unlock()
	return result
}

// parseChatHistory 逐行解析 JSONL，提取所有 content block（text / tool_use / tool_result）。
func parseChatHistory(data []byte, r *ChatHistoryResult) {
	turn := 0
	var msgs []ChatMessage

	for _, raw := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}

		switch {
		case bytes.Contains(line, []byte(`"type":"assistant"`)):
			var al struct {
				Message struct {
					Content []struct {
						Type  string          `json:"type"`
						Text  string          `json:"text"`
						Name  string          `json:"name"`
						ID    string          `json:"id"`
						Input json.RawMessage `json:"input"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &al) == nil {
				for _, b := range al.Message.Content {
					switch b.Type {
					case "text":
						if b.Text != "" {
							msgs = append(msgs, ChatMessage{Role: "assistant", Content: b.Text, Turn: turn})
						}
					case "tool_use":
						input := string(b.Input)
						cm := ChatMessage{
							Role:    "tool_use",
							Content: input,
							Tool:    b.Name,
							ToolID:  b.ID,
							Turn:    turn,
						}
						// Edit 工具：读文件定位修改区域起始行号，供前端 diff 显示真实行号
						if b.Name == "Edit" {
							cm.EditStartLine = editStartLine(b.Input)
						}
						msgs = append(msgs, cm)
					}
				}
			}

		case bytes.Contains(line, []byte(`"type":"user"`)):
			var ul struct {
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &ul) != nil {
				continue
			}
			blocks := parseContentBlocks(ul.Message.Content)
			for _, b := range blocks {
				switch {
				case b.text != "":
					turn++
					msgs = append(msgs, ChatMessage{Role: "user", Content: b.text, Turn: turn})
				case b.toolUseID != "":
					msgs = append(msgs, ChatMessage{
						Role:    "tool_result",
						Content: b.content,
						ToolID:  b.toolUseID,
						Turn:    turn,
					})
				}
			}
		}
	}

	// 最多保留 500 条
	if len(msgs) > 500 {
		msgs = msgs[len(msgs)-500:]
	}

	r.Messages = msgs
	for _, m := range msgs {
		r.Hash += len(m.Content)*31 + len(m.Tool)*17 + len(m.ToolID)*13
	}
}

// contentBlock 是 user 消息中的单个 content 块（text 或 tool_result）。
type contentBlock struct {
	text      string
	toolUseID string
	content   string // tool_result 的实际内容
}

// parseContentBlocks 解析 user 消息的 content（可能是字符串或数组）。
func parseContentBlocks(raw json.RawMessage) []contentBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}

	// content 是字符串（纯文本 user 消息）
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return []contentBlock{{text: s}}
		}
		return nil
	}

	// content 是数组
	var items []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}

	var blocks []contentBlock
	for _, item := range items {
		switch item.Type {
		case "text":
			if item.Text != "" {
				blocks = append(blocks, contentBlock{text: item.Text})
			}
		case "tool_result":
			content := extractToolResultContent(item.Content)
			blocks = append(blocks, contentBlock{
				toolUseID: item.ToolUseID,
				content:   content,
			})
		}
	}
	return blocks
}

// extractToolResultContent 从 tool_result 的 content 字段提取文本。
// content 可能是字符串或 text 块数组。
func extractToolResultContent(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		json.Unmarshal(raw, &s)
		return s
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &items) == nil {
		var parts []string
		for _, it := range items {
			if it.Type == "text" && it.Text != "" {
				parts = append(parts, it.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// editStartLine 解析 Edit 工具的 input，在目标文件中定位修改区域起始行号（1-based）。
// Edit 成功后文件已是新版，优先用 new_string 匹配；失败回退 old_string。找不到返回 0。
func editStartLine(input json.RawMessage) int {
	var ei struct {
		FilePath  string `json:"file_path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if json.Unmarshal(input, &ei) != nil {
		return 0
	}
	var needles []string
	if ei.NewString != "" {
		needles = append(needles, ei.NewString)
	}
	if ei.OldString != "" {
		needles = append(needles, ei.OldString)
	}
	return findStartLine(ei.FilePath, needles)
}

// findStartLine 在 filePath 中查找 needles 中首个出现的字符串，返回其起始行号(1-based)。找不到返回 0。
func findStartLine(filePath string, needles []string) int {
	if filePath == "" || len(needles) == 0 {
		return 0
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0
	}
	for _, n := range needles {
		if n == "" {
			continue
		}
		idx := bytes.Index(data, []byte(n))
		if idx >= 0 {
			return 1 + bytes.Count(data[:idx], []byte{'\n'})
		}
	}
	return 0
}
