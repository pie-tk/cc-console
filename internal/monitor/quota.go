package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 账号用量（account usage）：按当前 Claude Code 后端类型分发——
//   GLM (z.ai / bigmodel.cn)     → 配额（5h token 窗口 + 月度）
//   DeepSeek (deepseek.com)      → 余额
//   其他后端                     → 不展示
// 后端类型由 ~/.claude/settings.json 的 env（ANTHROPIC_BASE_URL + 凭证）决定，
// 缓存感知 settings.json 的 mtime，换后端/换 key 后下一轮轮询即重查。

// Limit 表示 GLM 配额的一个限制项（5 小时 token 窗口或月度用量）。
type Limit struct {
	Type          string                   `json:"type"`                    // TOKENS_LIMIT | TIME_LIMIT
	Percentage    int                      `json:"percentage"`              // 已用百分比
	NextResetTime int64                    `json:"nextResetTime,omitempty"` // 下次重置时刻（epoch 毫秒）
	CurrentValue  int64                    `json:"currentValue,omitempty"`  // 月度：当前用量
	Usage         int64                    `json:"usage,omitempty"`         // 月度：总额
	Remaining     int64                    `json:"remaining,omitempty"`     // 月度：剩余
	UsageDetails  []map[string]interface{} `json:"usageDetails,omitempty"`  // 月度：按模型/工具明细（透传，前端拼 tooltip 备用）
}

// Balance 表示 DeepSeek 账户余额。
type Balance struct {
	Currency string `json:"currency"`        // "CNY" / "USD" ...
	Total    string `json:"total"`           // 总余额，如 "10.00"
	Granted  string `json:"granted,omitempty"`  // 赠送余额
	ToppedUp string `json:"toppedUp,omitempty"` // 充值余额
}

// AccountUsage 是当前后端的账号用量信息（账号级，非实例级）。
type AccountUsage struct {
	Provider  string   `json:"provider"`        // "glm" | "deepseek" | ""
	Available bool     `json:"available"`       // 是否有可用用量数据
	Reason    string   `json:"reason,omitempty"` // 不可用原因：unsupported | no-token | fetch-failed
	FetchedAt int64    `json:"fetchedAt"`       // 抓取时刻（epoch 毫秒）
	Error     string   `json:"error,omitempty"`  // 抓取失败的错误信息（前端 tooltip）
	Level     string   `json:"level,omitempty"`  // GLM 账号等级
	Tokens    *Limit   `json:"tokens,omitempty"` // GLM 5h token 窗口
	Monthly   *Limit   `json:"monthly,omitempty"`
	Balance   *Balance `json:"balance,omitempty"` // DeepSeek 余额
}

// 各后端的查询端点（挂在 base domain 下）。
const (
	glmQuotaEndpoint       = "/api/monitor/usage/quota/limit"
	deepseekBalanceEndpoint = "/user/balance"
)

// readProviderAndToken 从 ~/.claude/settings.json 读取后端类型、base host 与凭证。
// 凭证取 ANTHROPIC_AUTH_TOKEN 优先、ANTHROPIC_API_KEY 兜底（GLM 用前者、DeepSeek 教程用后者）。
// 返回 (baseHost, token, provider, reason)：provider 为 "" 时 reason 说明原因（unsupported/no-token）。
// 读取模式与 models.go loadSettingsContextLimits 一致。
func readProviderAndToken() (baseHost, token, provider, reason string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", "", "no-token"
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return "", "", "", "no-token"
	}
	var cfg struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "", "", "", "no-token"
	}
	token = cfg.Env["ANTHROPIC_AUTH_TOKEN"]
	if token == "" {
		token = cfg.Env["ANTHROPIC_API_KEY"]
	}
	if token == "" {
		return "", "", "", "no-token"
	}
	baseURL := cfg.Env["ANTHROPIC_BASE_URL"]
	if baseURL == "" {
		return "", "", "", "unsupported"
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", "", "unsupported"
	}
	host := strings.ToLower(u.Host)
	switch {
	case strings.Contains(host, "z.ai"), strings.Contains(host, "bigmodel.cn"):
		provider = "glm"
	case strings.Contains(host, "deepseek.com"):
		provider = "deepseek"
	default:
		return "", "", "", "unsupported"
	}
	return u.Scheme + "://" + u.Host, token, provider, ""
}

// settingsMtime 返回 ~/.claude/settings.json 的修改时刻（epoch 毫秒），用于缓存感知切换。
// 文件不存在或无法读取返回 0（视为「变了」，触发重查）。
func settingsMtime() int64 {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return 0
	}
	fi, err := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return 0
	}
	return fi.ModTime().UnixMilli()
}

// FetchAccountUsage 按当前后端查询账号用量，始终返回非 nil 的 AccountUsage。
// 不支持的后端 / 无 token 直接返回带 Reason 的结构（不发请求）。
func FetchAccountUsage() *AccountUsage {
	now := time.Now().UnixMilli()
	baseHost, token, provider, reason := readProviderAndToken()
	switch provider {
	case "glm":
		return fetchGlm(baseHost, token, now)
	case "deepseek":
		return fetchDeepseek(baseHost, token, now)
	}
	return &AccountUsage{Provider: provider, Available: false, Reason: reason, FetchedAt: now}
}

// fetchGlm 查询 GLM 配额（裸 token 鉴权）。
func fetchGlm(baseHost, token string, now int64) *AccountUsage {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", baseHost+glmQuotaEndpoint, nil)
	if err != nil {
		return &AccountUsage{Provider: "glm", Available: false, Reason: "fetch-failed", Error: err.Error(), FetchedAt: now}
	}
	req.Header.Set("Authorization", token) // GLM 用裸 token，不加 Bearer
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en-US,en")
	req.Header.Set("User-Agent", "cc-console")

	resp, err := client.Do(req)
	if err != nil {
		return &AccountUsage{Provider: "glm", Available: false, Reason: "fetch-failed", Error: err.Error(), FetchedAt: now}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &AccountUsage{Provider: "glm", Available: false, Reason: "fetch-failed", Error: fmt.Sprintf("HTTP %d", resp.StatusCode), FetchedAt: now}
	}
	var raw struct {
		Code int    `json:"code"`
		Msg  string `json:"msg,omitempty"`
		Data struct {
			Level  string  `json:"level"`
			Limits []Limit `json:"limits"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return &AccountUsage{Provider: "glm", Available: false, Reason: "fetch-failed", Error: err.Error(), FetchedAt: now}
	}
	info := &AccountUsage{Provider: "glm", Available: true, Level: raw.Data.Level, FetchedAt: now}
	for i := range raw.Data.Limits {
		l := raw.Data.Limits[i]
		switch l.Type {
		case "TOKENS_LIMIT":
			info.Tokens = &l
		case "TIME_LIMIT":
			info.Monthly = &l
		}
	}
	return info
}

// fetchDeepseek 查询 DeepSeek 账户余额（Bearer 鉴权）。
func fetchDeepseek(baseHost, token string, now int64) *AccountUsage {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", baseHost+deepseekBalanceEndpoint, nil)
	if err != nil {
		return &AccountUsage{Provider: "deepseek", Available: false, Reason: "fetch-failed", Error: err.Error(), FetchedAt: now}
	}
	req.Header.Set("Authorization", "Bearer "+token) // DeepSeek 用 Bearer 前缀
	req.Header.Set("User-Agent", "cc-console")

	resp, err := client.Do(req)
	if err != nil {
		return &AccountUsage{Provider: "deepseek", Available: false, Reason: "fetch-failed", Error: err.Error(), FetchedAt: now}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &AccountUsage{Provider: "deepseek", Available: false, Reason: "fetch-failed", Error: fmt.Sprintf("HTTP %d", resp.StatusCode), FetchedAt: now}
	}
	var raw struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return &AccountUsage{Provider: "deepseek", Available: false, Reason: "fetch-failed", Error: err.Error(), FetchedAt: now}
	}
	info := &AccountUsage{Provider: "deepseek", FetchedAt: now}
	if len(raw.BalanceInfos) > 0 {
		b := raw.BalanceInfos[0]
		info.Available = true
		info.Balance = &Balance{
			Currency: b.Currency,
			Total:    b.TotalBalance,
			Granted:  b.GrantedBalance,
			ToppedUp: b.ToppedUpBalance,
		}
	} else {
		// 无 balance_infos：按 is_available 判定，通常意味着账户不可用
		info.Available = false
		info.Reason = "fetch-failed"
		info.Error = "no balance info"
	}
	return info
}

// ---- 账号用量缓存（账号级全局，120s TTL + settings.json mtime 感知）----

var (
	usageMu            sync.Mutex
	usageCache         *AccountUsage
	usageFetchedAt     int64
	usageSettingsMtime int64
)

// usageTTLms 缓存有效期。账号用量是网络请求，不能像本地检测那样每秒拉；
// 配额数值随 token 消耗变化，60s 刷新一次既及时又避免轰炸后端。
// 配合前端 15s 轮询 + mtime 感知：切换 settings.json 后 ≤15s 反映，数值 ≤60s 刷新。
const usageTTLms = 60 * 1000

// GetAccountUsage 返回带缓存的账号用量。
// 缓存有效 = settings.json mtime 未变 AND 未过 120s TTL；任一不满足则重查。
// 这样换后端/换 key（改 settings.json → mtime 变）会在下一轮轮询立即重查并反映，
// 而同后端的配额数值变化靠 TTL 周期刷新。采用「锁内检查 → 锁外 fetch → 锁内提交」。
func GetAccountUsage() *AccountUsage {
	curMtime := settingsMtime()
	now := time.Now().UnixMilli()
	usageMu.Lock()
	if usageCache != nil && usageSettingsMtime == curMtime && now-usageFetchedAt < usageTTLms {
		c := usageCache
		usageMu.Unlock()
		return c
	}
	usageMu.Unlock()

	fresh := FetchAccountUsage() // 慢操作，锁外执行
	usageMu.Lock()
	usageCache = fresh
	usageFetchedAt = fresh.FetchedAt
	usageSettingsMtime = curMtime
	usageMu.Unlock()
	return fresh
}
