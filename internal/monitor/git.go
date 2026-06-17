package monitor

import (
	"os"
	"path/filepath"
	"strings"
)

// gitBranchEntry 缓存一个仓库解析出的分支。
// key 为 .git 目录（或 worktree 指向的 gitdir）路径；HEAD 文件 mtime 未变即复用，
// 避免每秒、每个实例都重读 HEAD（分支切换会更新 HEAD 的 mtime）。
type gitBranchEntry struct {
	branch string
	mtime  int64 // HEAD 文件 mtime（UnixNano），作失效依据
}

var gitBranchCache = map[string]gitBranchEntry{}

// gitBranchCacheCap 缓存条目上限，超过即整体清空。实际遇到的仓库数远小于此值。
const gitBranchCacheCap = 256

// detectGitBranch 返回 cwd 所属 git 仓库的当前分支名。
// 无仓库返回空字符串；分离头指针(detached HEAD)返回 7 位短 commit 哈希。
// 不依赖 git 可执行文件——直接向上查找 .git 并读 HEAD，零进程开销。
func detectGitBranch(cwd string) string {
	if cwd == "" {
		return ""
	}
	gitDir := findGitDir(cwd)
	if gitDir == "" {
		return ""
	}
	headPath := filepath.Join(gitDir, "HEAD")
	info, err := os.Stat(headPath)
	if err != nil {
		return ""
	}
	mtime := info.ModTime().UnixNano()
	cacheMu.RLock()
	c, ok := gitBranchCache[gitDir]
	cacheMu.RUnlock()
	if ok && c.mtime == mtime {
		return c.branch
	}
	branch := parseGitHead(headPath)
	cacheMu.Lock()
	if len(gitBranchCache) >= gitBranchCacheCap {
		gitBranchCache = map[string]gitBranchEntry{}
	}
	gitBranchCache[gitDir] = gitBranchEntry{branch: branch, mtime: mtime}
	cacheMu.Unlock()
	return branch
}

// findGitDir 从 start 向上查找最近的 .git（目录或 worktree/submodule 文件），最多向上 40 级。
func findGitDir(start string) string {
	dir := start
	for i := 0; i < 40; i++ {
		gd := filepath.Join(dir, ".git")
		if fi, err := os.Stat(gd); err == nil {
			if fi.IsDir() {
				return gd
			}
			// .git 为文件 → worktree/submodule，内含 "gitdir: <path>"
			return resolveGitdirFile(gd)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // 到达盘符根
		}
		dir = parent
	}
	return ""
}

// resolveGitdirFile 解析 worktree/submodule 的 .git 文件，返回真正的 gitdir 路径。
func resolveGitdirFile(gd string) string {
	data, err := os.ReadFile(gd)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	p := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(gd), p)
	}
	return p
}

// parseGitHead 读取 HEAD 并解析当前分支：
//   - "ref: refs/heads/<name>" → <name>
//   - "ref: <其他引用>" → 引用末段
//   - commit 哈希（分离头指针）→ 前 7 位短哈希
func parseGitHead(headPath string) string {
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	const refPrefix = "ref:"
	if strings.HasPrefix(line, refPrefix) {
		ref := strings.TrimSpace(strings.TrimPrefix(line, refPrefix))
		if name := strings.TrimPrefix(ref, "refs/heads/"); name != ref {
			return name
		}
		// 其他引用（如 refs/remotes/origin/main），取末段作展示
		if i := strings.LastIndex(ref, "/"); i >= 0 {
			return ref[i+1:]
		}
		return ref
	}
	// 分离头指针：HEAD 直接指向 commit 哈希
	if len(line) >= 7 {
		return line[:7]
	}
	return ""
}
