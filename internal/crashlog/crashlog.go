// Package crashlog 捕获崩溃信息到磁盘。
//
// 背景：本程序是 Windows GUI 子系统（-H windowsgui），没有控制台，进程的 stderr 是空句柄。
// 因此任何 panic / Go runtime fatal error（如 concurrent map、out of memory）的堆栈都无处可去，
// 程序会无声闪退，事后无法排查——这正是此前"运行中突然自己退了、没有任何线索"的根因之一。
//
// 本包在启动时把 stdout/stderr 重定向到日志文件，并在 Windows 下用 SetStdHandle 重定向进程
// 标准句柄。已用实验验证：这样能完整捕获 Go runtime fatal error 的堆栈（含 goroutine 栈帧与
// 文件:行号），不再依赖 recover——recover 本身抓不到 fatal error。
package crashlog

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

// maxLogSize 日志文件大小上限，超过则在下一次启动时轮转，避免无限增长。
const maxLogSize = 2 << 20 // 2MB

var (
	mu      sync.Mutex
	logFile *os.File
	path    string
)

// Setup 初始化崩溃日志：打开（必要时轮转）日志文件，重定向 stdout/stderr 与 OS 标准句柄。
// logDir 为日志目录（不存在则创建）。返回日志文件绝对路径。
func Setup(logDir string) (string, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return "", err
	}
	p := filepath.Join(logDir, "monitor.log")

	// 轮转：文件过大则归档为 .old.log，重新开始
	if info, err := os.Stat(p); err == nil && info.Size() > maxLogSize {
		_ = os.Rename(p, filepath.Join(logDir, "monitor.old.log"))
	}

	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return "", err
	}

	mu.Lock()
	logFile = f
	path = p
	mu.Unlock()

	// 写启动分隔，便于区分多次运行
	fmt.Fprintf(f, "\n==== %s 启动 (pid %d) ====\n", time.Now().Format("2006-01-02 15:04:05"), os.Getpid())

	// 重定向 Go 层标准输出/错误（fmt.Println、log 包等）
	os.Stdout = f
	os.Stderr = f
	// 重定向 OS 层标准句柄（捕获 Go runtime 的 fatal error / panic 堆栈）
	redirectStdHandles(f)

	return p, nil
}

// LogPath 返回当前日志文件路径（未初始化返回空串）。
func LogPath() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

// Writef 向日志文件追加内容（不走 os.Stderr，便于在崩溃路径上直接写）。
func Writef(format string, args ...any) {
	mu.Lock()
	f := logFile
	mu.Unlock()
	if f == nil {
		return
	}
	fmt.Fprintf(f, format, args...)
}

// Recover 在 defer 中调用：捕获当前 goroutine 的 panic，把消息 + 堆栈写入日志。
// 注意：Go runtime 的 fatal error（concurrent map 等）无法被 recover 捕获，但它们的堆栈
// 已通过 Setup 重定向的 stderr 落盘；Recover 主要用于捕获普通 panic 并让进程存活。
func Recover() {
	if r := recover(); r != nil {
		Writef("panic recovered: %v\n%s\n", r, debug.Stack())
	}
}
