package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ---- 模型上下文上限（查表 + 配置覆盖） ----

var configLimits = map[string]int64{}

type modelLimitEntry struct {
	prefix string
	limit  int64
}

// 模型上下文窗口上限表（前缀匹配，第一条命中即返回）。
// 数据来源：各厂商官方文档（2025-2026），具体值可能随后端/版本变化，
// 可在 ~/.claude-monitor.json 的 modelLimits 字段覆盖。
var builtinModelLimits = []modelLimitEntry{
	// ---- Anthropic Claude ----
	// 注意：4.5+ 扩展到 1M；基础 4.x 为 200K
	{"claude-opus-4-8", 1000000},
	{"claude-opus-4-7", 1000000},
	{"claude-opus-4-6", 1000000},
	{"claude-opus-4", 200000},
	{"claude-sonnet-4-6", 1000000},
	{"claude-sonnet-4-5", 1000000},
	{"claude-sonnet-4", 200000},
	{"claude-haiku-4", 200000},
	{"claude-3-5-sonnet", 200000},
	{"claude-3-5-haiku", 200000},
	{"claude-3-opus", 200000},
	{"claude-3-sonnet", 200000},
	{"claude-3-haiku", 200000},

	// ---- OpenAI ----
	{"o4-mini", 200000},
	{"o3-mini", 200000},
	{"o3", 200000},
	{"o1-mini", 128000},
	{"o1", 200000},
	{"gpt-4.1-mini", 1048576},
	{"gpt-4.1-nano", 1048576},
	{"gpt-4.1", 1048576},
	{"gpt-4.5", 128000},
	{"gpt-4o-mini", 128000},
	{"gpt-4o", 128000},

	// ---- Google Gemini ----
	{"gemini-2.5-flash-lite", 1048576},
	{"gemini-2.5-flash", 1048576},
	{"gemini-2.5-pro", 1048576},
	{"gemini-2.0-flash-lite", 1048576},
	{"gemini-2.0-flash", 1048576},
	{"gemini-1.5-flash", 1048576},
	{"gemini-1.5-pro", 2097152},

	// ---- DeepSeek ----
	{"deepseek-v4", 1048576},
	{"deepseek_v4", 1048576},
	{"deepseek-r1", 131072},
	{"deepseek-v3", 131072},
	{"deepseek_v3", 131072},
	{"deepseek-v2", 131072},
	{"deepseek_v2", 131072},
	{"deepseek", 131072},

	// ---- 通义千问 Qwen ----
	{"qwen-long", 10000000},
	{"qwen-turbo", 1048576},
	{"qwen-plus", 1048576},
	{"qwen-max", 131072},
	{"qwen3-coder", 262144},
	{"qwen3-next", 262144},
	{"qwen3-max", 131072},
	{"qwen3-", 131072}, // qwen3-8b/14b/32b/72b via YaRN
	{"qwen2.5-", 131072},
	{"qwen", 131072},

	// ---- 智谱 GLM ----
	{"glm-5", 200000},
	{"glm-4.5-air", 131072},
	{"glm-4.5", 200000},
	{"glm-4-", 131072},
	{"glm-4", 131072},

	// ---- 月之暗面 Kimi ----
	{"kimi-k2-5", 262144},
	{"kimi-k2", 131072},
	{"moonshot-v1", 131072},

	// ---- 百度文心 ----
	{"ernie-5", 131072},
	{"ernie-4-5", 131072},
	{"ernie-4.5", 131072},

	// ---- 字节豆包 Doubao ----
	{"doubao-seed-1-6", 262144},
	{"doubao", 131072},

	// ---- Meta Llama ----
	{"llama-4-scout", 10485760},
	{"llama-4-maverick", 1048576},
	{"llama-4-", 1048576},
	{"llama-3-", 131072},
	{"llama-3", 8192},

	// ---- Mistral ----
	{"mistral-large", 262144},
	{"mistral-medium", 131072},
	{"mistral-small", 131072},
	{"mistral-nemo", 131072},
	{"codestral", 262144},
	{"pixtral", 131072},

	// ---- Cohere ----
	{"command-a", 262144},
	{"command-r-plus", 131072},
	{"command-r", 131072},
}

// modelContextLimit 解析模型字符串的上下文上限。
//
// 优先顺序：
//  1. 模型字符串里显式带的上限信息（格式：<model>[<limit>]，如 "deepseek-v4-pro[1M]" / "glm-5[256k]"）
//  2. ~/.claude-monitor.json 里的精确模型映射
//  3. 内置表的前缀匹配
//  4. 默认 200000
func modelContextLimit(model string) int64 {
	if model == "" {
		return 0
	}
	ml := strings.ToLower(model)

	// 1) 显式 [xxx] 后缀：支持 K/M/G 缩写（200k = 200*1000，2m = 2*1000*1000）
	base, explicit, hasExplicit := splitModelAndLimit(ml)
	if hasExplicit {
		if v, ok := parseLimitToken(explicit); ok {
			return v
		}
		// 解析失败就忽略括号，继续走下面的匹配
	}
	// 解析失败或没括号时，用原串继续
	if base != "" {
		ml = base
	}

	// 2) 配置精确覆盖
	if v, ok := configLimits[ml]; ok {
		return v
	}
	// 3) 内置表前缀匹配
	for _, e := range builtinModelLimits {
		if strings.HasPrefix(ml, e.prefix) {
			return e.limit
		}
	}
	// 4) 默认 200K
	return 200000
}

// splitModelAndLimit 拆 "model[limit]"；返回 (base, limit, true) 或 (原串, "", false)
func splitModelAndLimit(s string) (string, string, bool) {
	i := strings.LastIndex(s, "[")
	j := strings.LastIndex(s, "]")
	if i >= 0 && j > i {
		return s[:i], strings.ToLower(s[i+1 : j]), true
	}
	return s, "", false
}

// parseLimitToken 解析 "200k" / "2m" / "1g" / 纯数字 → tokens
func parseLimitToken(t string) (int64, bool) {
	t = strings.TrimSpace(t)
	if t == "" {
		return 0, false
	}
	mult := int64(1)
	switch t[len(t)-1] {
	case 'k':
		mult = 1000
		t = t[:len(t)-1]
	case 'm':
		mult = 1000000
		t = t[:len(t)-1]
	case 'g':
		mult = 1000000000
		t = t[:len(t)-1]
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n * mult, true
}

// loadConfig 读取上下文上限配置，优先级：
//  1. ~/.claude-monitor.json 的 modelLimits（精确覆盖）
//  2. ~/.claude/settings.json 里模型环境变量中的 [xxx] 标注
//     如 ANTHROPIC_MODEL="deepseek-v4-pro[1M]" → 自动识别为 1,000,000
func loadConfig() {
	configLimits = map[string]int64{}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}

	// ~/.claude-monitor.json 手动覆盖
	data, err := os.ReadFile(filepath.Join(home, ".claude-monitor.json"))
	if err == nil {
		var cfg struct {
			ModelLimits map[string]int64 `json:"modelLimits"`
		}
		if json.Unmarshal(data, &cfg) == nil {
			for k, v := range cfg.ModelLimits {
				configLimits[strings.ToLower(k)] = v
			}
		}
	}

	// ~/.claude/settings.json 里的模型环境变量
	loadSettingsContextLimits(home)
}

// loadSettingsContextLimits 从 Claude Code 的 settings.json 中读取模型环境变量，
// 解析其中的 [xxx] 上下文标注，注入到 configLimits。
func loadSettingsContextLimits(home string) {
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return
	}
	var cfg struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &cfg) != nil || len(cfg.Env) == 0 {
		return
	}
	// 检查这些模型相关的环境变量
	modelKeys := []string{
		"ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME",
	}
	for _, key := range modelKeys {
		val := cfg.Env[key]
		if val == "" {
			continue
		}
		base, explicit, hasExplicit := splitModelAndLimit(strings.ToLower(val))
		if !hasExplicit {
			continue
		}
		if v, ok := parseLimitToken(explicit); ok {
			// "deepseek-v4-pro[1M]" → configLimits["deepseek-v4-pro"] = 1000000
			configLimits[base] = v
		}
	}
}
