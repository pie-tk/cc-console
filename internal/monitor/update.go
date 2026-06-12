package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// ReleaseInfo GitHub 最新 release 信息。
type ReleaseInfo struct {
	Version     string `json:"version"`     // 如 "v1.2.0"
	Name        string `json:"name"`        // release 标题
	Body        string `json:"body"`        // release notes (markdown)
	DownloadURL string `json:"downloadUrl"` // 安装包下载地址
	PublishedAt string `json:"publishedAt"`
}

// CheckLatestRelease 调用 GitHub API 获取最新 release。
func CheckLatestRelease(owner, repo, token string) (*ReleaseInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "claude-code-monitor")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("GitHub Token 无效，请检查后重试")
	}
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, fmt.Errorf("GitHub API 限流（未认证 60次/小时），如有开启网络代理或 VPN 可关闭后重试")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API 返回 HTTP %d", resp.StatusCode)
	}

	var apiResp struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	// 优先匹配安装包，兼容旧版裸 exe
	downloadURL := ""
	for _, a := range apiResp.Assets {
		name := strings.ToLower(a.Name)
		if strings.HasPrefix(name, "claude-monitor-setup") && strings.HasSuffix(name, ".exe") {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		for _, a := range apiResp.Assets {
			if strings.HasSuffix(strings.ToLower(a.Name), ".exe") {
				downloadURL = a.BrowserDownloadURL
				break
			}
		}
	}
	if downloadURL == "" {
		downloadURL = fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", owner, repo, apiResp.TagName)
	}

	return &ReleaseInfo{
		Version:     strings.TrimPrefix(apiResp.TagName, "v"),
		Name:        apiResp.Name,
		Body:        apiResp.Body,
		DownloadURL: downloadURL,
		PublishedAt: apiResp.PublishedAt,
	}, nil
}

// IsNewer 比较两个语义化版本，latest > current 时返回 true。
func IsNewer(latest, current string) bool {
	lp := parseSemver(latest)
	cp := parseSemver(current)
	for i := 0; i < 3; i++ {
		if lp[i] > cp[i] {
			return true
		}
		if lp[i] < cp[i] {
			return false
		}
	}
	return false
}

// GitHubToken 优先读 GH_TOKEN，其次 GITHUB_TOKEN。
func GitHubToken() string {
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITHUB_TOKEN")
}

// parseSemver 解析 "1.2.3" 为 [3]int，解析失败返回全 0。
func parseSemver(v string) [3]int {
	var parts [3]int
	n, _ := fmt.Sscanf(strings.TrimSpace(v), "%d.%d.%d", &parts[0], &parts[1], &parts[2])
	if n < 1 {
		return [3]int{}
	}
	return parts
}
