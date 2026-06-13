//go:build !windows

package monitor

func init() {
	// 非 Windows:无命令行读取能力,过滤逻辑跳过。
	processCmdline = func(pid int) string { return "" }
}
