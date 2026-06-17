package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CommandSuggestion 是斜杠命令/技能自动补全列表中的一项。
type CommandSuggestion struct {
	Name        string `json:"name"`        // 如 clear / git-commit（不含前导 /）
	Type        string `json:"type"`        // builtin | command | skill
	Description string `json:"description"` // 单行说明
	Source      string `json:"source"`      // 内置 / 项目 / 用户 / 插件
}

// builtinCommands 是 Claude Code 内置斜杠命令清单（与终端 / 菜单一致）。
// 这些命令由 CLI 自身实现，不在磁盘上；放在最前，确保补全列表稳定可见。
var builtinCommands = []CommandSuggestion{
	{Name: "clear", Type: "builtin", Description: "清空当前对话上下文", Source: "内置"},
	{Name: "compact", Type: "builtin", Description: "压缩对话历史，保留摘要", Source: "内置"},
	{Name: "resume", Type: "builtin", Description: "恢复之前的会话", Source: "内置"},
	{Name: "agents", Type: "builtin", Description: "管理子代理", Source: "内置"},
	{Name: "model", Type: "builtin", Description: "切换模型", Source: "内置"},
	{Name: "config", Type: "builtin", Description: "打开配置", Source: "内置"},
	{Name: "cost", Type: "builtin", Description: "查看本次会话用量与花费", Source: "内置"},
	{Name: "memory", Type: "builtin", Description: "编辑 CLAUDE.md 记忆文件", Source: "内置"},
	{Name: "mcp", Type: "builtin", Description: "管理 MCP 服务器", Source: "内置"},
	{Name: "permissions", Type: "builtin", Description: "查看与管理权限", Source: "内置"},
	{Name: "init", Type: "builtin", Description: "初始化项目的 CLAUDE.md", Source: "内置"},
	{Name: "status", Type: "builtin", Description: "查看账户与会话状态", Source: "内置"},
	{Name: "review", Type: "builtin", Description: "审查代码 / PR", Source: "内置"},
	{Name: "vim", Type: "builtin", Description: "切换 vim 模式", Source: "内置"},
	{Name: "login", Type: "builtin", Description: "登录账号", Source: "内置"},
	{Name: "logout", Type: "builtin", Description: "登出账号", Source: "内置"},
	{Name: "bug", Type: "builtin", Description: "报告问题", Source: "内置"},
	{Name: "doctor", Type: "builtin", Description: "诊断环境", Source: "内置"},
	{Name: "usage", Type: "builtin", Description: "查看 CLI 用量", Source: "内置"},
	{Name: "export", Type: "builtin", Description: "导出当前会话", Source: "内置"},
	{Name: "terminal-setup", Type: "builtin", Description: "安装 Shift+Enter 快捷键", Source: "内置"},
	{Name: "feedback", Type: "builtin", Description: "提交反馈", Source: "内置"},
	{Name: "release-notes", Type: "builtin", Description: "查看更新日志", Source: "内置"},
}

// GetCommandSuggestions 汇总该 cwd 下可用的斜杠命令/技能，用于消息框自动补全。
// 优先级：内置 > 项目(cwd/.claude) > 用户(~/.claude) > 插件(installed_plugins)。
// 每个来源出错（目录/文件缺失）静默跳过，不影响其它来源。按名称去重保留优先级高者。
func GetCommandSuggestions(cwd string) []CommandSuggestion {
	seen := map[string]bool{}
	var out []CommandSuggestion
	add := func(items []CommandSuggestion) {
		for _, it := range items {
			name := strings.ToLower(strings.TrimSpace(it.Name))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			it.Name = name
			// 描述过长截断为单行，避免下拉项撑得过高
			if d := strings.TrimSpace(it.Description); d != "" {
				if i := strings.IndexAny(d, "\r\n"); i >= 0 {
					d = d[:i]
				}
				if len(d) > 80 {
					d = d[:80] + "…"
				}
				it.Description = d
			}
			out = append(out, it)
		}
	}

	add(builtinCommands)

	home, _ := os.UserHomeDir()
	userClaude := ""
	if home != "" {
		userClaude = filepath.Join(home, ".claude")
	}

	// 项目层（当前实例 cwd）
	if cwd != "" {
		add(scanCommands(filepath.Join(cwd, ".claude", "commands"), "项目"))
		add(scanSkills(filepath.Join(cwd, ".claude", "skills"), "项目"))
	}
	// 用户层（~/.claude）
	if userClaude != "" {
		add(scanCommands(filepath.Join(userClaude, "commands"), "用户"))
		add(scanSkills(filepath.Join(userClaude, "skills"), "用户"))
		// 插件层：installed_plugins.json → 各 installPath
		add(scanPlugins(filepath.Join(userClaude, "plugins", "installed_plugins.json")))
	}
	return out
}

// scanCommands 扫描 dir 下的 *.md 作为自定义斜杠命令：name=文件名去后缀，desc 取 frontmatter。
func scanCommands(dir, source string) []CommandSuggestion {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []CommandSuggestion
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == "" {
			continue
		}
		_, desc := parseFrontmatter(filepath.Join(dir, e.Name()))
		out = append(out, CommandSuggestion{Name: name, Type: "command", Description: desc, Source: source})
	}
	return out
}

// scanSkills 扫描 dir 下的 */SKILL.md 作为技能：name 取 frontmatter name（无则用目录名），desc 取 frontmatter。
func scanSkills(dir, source string) []CommandSuggestion {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []CommandSuggestion
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillMd := filepath.Join(dir, e.Name(), "SKILL.md")
		name, desc := parseFrontmatter(skillMd)
		if name == "" {
			name = e.Name() // 无 frontmatter name 时回退到目录名
		}
		out = append(out, CommandSuggestion{Name: name, Type: "skill", Description: desc, Source: source})
	}
	return out
}

// scanPlugins 解析 installed_plugins.json，对每个 installPath 扫描其 commands/ 与 skills/。
func scanPlugins(jsonPath string) []CommandSuggestion {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil
	}
	// 结构宽松：只取 installPath 字段，忽略版本/作用域等。
	var doc struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	var out []CommandSuggestion
	for _, installs := range doc.Plugins {
		for _, ins := range installs {
			p := strings.TrimSpace(ins.InstallPath)
			if p == "" {
				continue
			}
			out = append(out, scanCommands(filepath.Join(p, "commands"), "插件")...)
			out = append(out, scanSkills(filepath.Join(p, "skills"), "插件")...)
		}
	}
	return out
}

// parseFrontmatter 读取 markdown 文件 YAML 头部的 name / description（极简行解析，不引入 YAML 依赖）。
// 文件首行须为 "---"，读到下一个 "---" 之间按 "key: value" 提取目标字段；读不到返回空串。
func parseFrontmatter(path string) (name, desc string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" || line == "" {
			if line == "---" {
				break
			}
			continue
		}
		if v := frontmatterValue(line, "name:"); v != "" && name == "" {
			name = v
		}
		if v := frontmatterValue(line, "description:"); v != "" && desc == "" {
			desc = v
		}
	}
	return name, desc
}

// frontmatterValue 若 line 以 "key:" 开头，返回去引号、trim 后的值，否则空串。
func frontmatterValue(line, key string) string {
	if !strings.HasPrefix(line, key) {
		return ""
	}
	v := strings.TrimSpace(strings.TrimPrefix(line, key))
	v = strings.Trim(v, "\"'")
	return v
}
