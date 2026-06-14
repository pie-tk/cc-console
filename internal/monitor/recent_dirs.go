package monitor

// maxRecentDirs 最近工作目录列表的最大长度。
const maxRecentDirs = 8

// GetRecentDirs 返回最近工作目录（最多 maxRecentDirs 个，最近在前）。
// 返回拷贝，不影响内部状态；无记录时返回空切片（非 nil）。
func GetRecentDirs() []string {
	dirs := currentSettings.RecentDirs
	out := make([]string, 0, len(dirs))
	return append(out, dirs...)
}

// AddRecentDir 把 dir 提到列表最前（去重、限长 maxRecentDirs）并落盘。
// 返回更新后的列表。dir 为空时直接返回当前列表，不写入。
func AddRecentDir(dir string) ([]string, error) {
	if dir == "" {
		return GetRecentDirs(), nil
	}
	cfg := currentSettings // 值拷贝，保留其它字段（ModelLimits/CloseQuits 等）
	// 插到最前，跳过重复项
	filtered := make([]string, 0, len(cfg.RecentDirs)+1)
	filtered = append(filtered, dir)
	for _, d := range cfg.RecentDirs {
		if d != dir {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) > maxRecentDirs {
		filtered = filtered[:maxRecentDirs]
	}
	cfg.RecentDirs = filtered
	return filtered, SaveSettings(cfg)
}
