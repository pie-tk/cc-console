package monitor

// Instance 表示一个被发现的 Claude Code 实例。
type Instance struct {
	Pid             int     `json:"pid"`
	Status          string  `json:"status"`
	Cwd             string  `json:"cwd"`
	Version         string  `json:"version"`
	SessionID       string  `json:"sessionId"`
	StartedAt       int64   `json:"startedAt"`       // epoch 毫秒
	UpdatedAt       int64   `json:"updatedAt"`       // epoch 毫秒
	HasSession      bool    `json:"hasSession"`      // 是否找到了对应的 session 文件
	HasConversation bool    `json:"hasConversation"` // 是否存在 .jsonl 对话文件
	Model           string  `json:"model"`
	ContextTokens   int64   `json:"contextTokens"` // input + cache_creation + cache_read
	OutputTokens    int64   `json:"outputTokens"`
	Topic           string  `json:"topic"`
	ContextLimit    int64   `json:"contextLimit"`          // 模型上下文上限（0 表示未知）
	ContextPercent  int     `json:"contextPercent"`        // 上下文占用百分比（statusline 原生 used_percentage）
	CostUsd         float64 `json:"costUsd"`               // 会话累计费用 USD（statusline cost）
	DurationMs      int64   `json:"durationMs"`            // 会话时长 ms（statusline cost）
	TaskStartedAt   int64   `json:"taskStartedAt"`         // 当前任务开始时刻（首次进入 busy 起，直到下次 UserPromptSubmit 才重置；中途 Stop 不清零）
	WaitingKind     string  `json:"waitingKind,omitempty"` // 等待用户处理的交互类型：ask / plan / permission
	BridgeConnected bool    `json:"bridgeConnected"`       // statusline 桥接是否对该实例生效
	Live            bool    `json:"live"`                  // 是否有新鲜的 live 数据（实时反映）
	GitBranch       string  `json:"gitBranch"`             // 当前项目 git 分支（无仓库为空）
	TranscriptPath  string  `json:"-"`                     // 当前会话 jsonl 官方路径（来自 statusline，内部用）
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

// ChatMessage 表示会话历史中的一条完整消息（用于聊天面板）。
// 与 HistoryTurn 不同，ChatMessage 按时间顺序包含每个 content block。
type ChatMessage struct {
	Role             string `json:"role"`                       // "user" | "assistant" | "tool_use" | "tool_result" | "command"
	Content          string `json:"content"`                    // 显示文本（tool_use 时为 JSON input）
	Tool             string `json:"tool,omitempty"`             // 工具名（tool_use / tool_result 时）
	ToolID           string `json:"toolId,omitempty"`           // tool_use_id（用于配对）
	IsError          bool   `json:"isError,omitempty"`          // tool_result 是否出错（JSONL 缺失 is_error 时为 false）
	Turn             int    `json:"turn"`                       // 轮次号（1-based），tool_result 与前一 user 同轮次
	EditStartLine    int    `json:"editStartLine,omitempty"`    // Edit 工具修改区域起始行号（1-based，0 表示未知）
	Ts               int64  `json:"ts,omitempty"`               // 该消息落盘时刻（epoch 毫秒，取自 JSONL 顶层 timestamp）
	BlockType        string `json:"blockType,omitempty"`        // 原始 content block 类型：text/tool_use/server_tool_use/tool_result/command
	RawContent       string `json:"rawContent,omitempty"`       // 原始 block JSON/结果 JSON（展示层需要结构化兜底时使用）
	ToolUseResult    string `json:"toolUseResult,omitempty"`    // JSONL 顶层 toolUseResult（比文本结果更结构化）
	AttributionSkill string `json:"attributionSkill,omitempty"` // skill 执行链归因（Claude Code 顶层字段）
	LastPrompt       string `json:"lastPrompt,omitempty"`       // last-prompt 记录中的原始 slash command
}

// ChatHistoryResult 是 GetChatHistory 的返回结构。
type ChatHistoryResult struct {
	Messages   []ChatMessage `json:"messages"`
	Hash       int           `json:"hash"`                 // 前端用于增量更新判断
	PendingAsk *AskRecord    `json:"pendingAsk,omitempty"` // 活跃会话挂起的 AskUserQuestion（JSONL 未落盘时的实时旁路，由 service 层填）
}

// StatsInfo 统计信息，供前端使用。
type StatsInfo struct {
	Online      int   `json:"online"`
	Busy        int   `json:"busy"`
	Idle        int   `json:"idle"`
	Stale       int   `json:"stale"`
	Offline     int   `json:"offline"`     // 未接入 statusline 桥接的实例数
	TotalTokens int64 `json:"totalTokens"` // 所有实例累计 tokens
}
