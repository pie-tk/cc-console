package monitor

// Instance 表示一个被发现的 Claude Code 实例。
type Instance struct {
	Pid             int    `json:"pid"`
	Status          string `json:"status"`
	Cwd             string `json:"cwd"`
	Version         string `json:"version"`
	SessionID       string `json:"sessionId"`
	StartedAt       int64  `json:"startedAt"`  // epoch 毫秒
	UpdatedAt       int64  `json:"updatedAt"`  // epoch 毫秒
	HasSession      bool   `json:"hasSession"`  // 是否找到了对应的 session 文件
	HasConversation bool   `json:"hasConversation"` // 是否存在 .jsonl 对话文件
	Model           string `json:"model"`
	ContextTokens   int64  `json:"contextTokens"` // input + cache_creation + cache_read
	OutputTokens    int64  `json:"outputTokens"`
	Topic           string `json:"topic"`
	ContextLimit    int64  `json:"contextLimit"` // 模型上下文上限（0 表示未知）
	// 累计 token（整个会话所有 assistant 消息求和）
	TotalInputTokens  int64 `json:"totalInputTokens"`
	TotalOutputTokens int64 `json:"totalOutputTokens"`
	TotalCacheTokens  int64 `json:"totalCacheTokens"`
	// 会话动态信息（主题行右侧）
	LastUserQuery string `json:"lastUserQuery"` // 最近一条真实用户提问
	LastReplySnip string `json:"lastReplySnip"` // 最近一条助手回复片段
	Turns         int    `json:"turns"`         // 消息轮数
	LastTool      string `json:"lastTool"`      // 最近使用的工具名
	// 对话历史（所有 Q&A 轮次，最多 30 轮）
	History     []HistoryTurn `json:"history,omitempty"`
	HistoryHash int           `json:"historyHash"` // 历史内容哈希，前端用于检测更新
}

// HistoryTurn 表示一轮 Q&A 对话（用户提问 + 助手回复片段）。
type HistoryTurn struct {
	UserQuery string `json:"q"` // 用户提问，截断至 80 runes
	ReplySnip string `json:"r"` // 助手回复片段，截断至 120 runes
}

// StatsInfo 统计信息，供前端使用。
type StatsInfo struct {
	Online     int   `json:"online"`
	Busy       int   `json:"busy"`
	Idle       int   `json:"idle"`
	Stale      int   `json:"stale"`
	TotalTokens int64 `json:"totalTokens"` // 所有实例累计 tokens
}
