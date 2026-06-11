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
}

// StatsInfo 统计信息，供前端使用。
type StatsInfo struct {
	Online     int   `json:"online"`
	Busy       int   `json:"busy"`
	Idle       int   `json:"idle"`
	Stale      int   `json:"stale"`
	TotalTokens int64 `json:"totalTokens"` // 所有实例累计 tokens
}
