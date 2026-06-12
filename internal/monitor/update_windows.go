//go:build windows

package monitor

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// DownloadAndReplace 下载新版本 exe，通过批处理脚本实现自替换。
// 执行成功后本进程将退出，由批处理脚本完成替换并重启。
func DownloadAndReplace(downloadURL string) error {
	// 1. 确定当前 exe 路径
	curExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前程序路径失败: %w", err)
	}
	curExe, err = filepath.EvalSymlinks(curExe)
	if err != nil {
		return fmt.Errorf("解析程序路径失败: %w", err)
	}

	// 2. 下载新版本到临时目录
	tmpDir := os.TempDir()
	tmpExe := filepath.Join(tmpDir, "claude-monitor-update.exe")
	tmpBat := filepath.Join(tmpDir, "claude-monitor-update.bat")

	// 先清理上次残留
	os.Remove(tmpExe)
	os.Remove(tmpBat)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(tmpExe)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpExe)
		return fmt.Errorf("写入文件失败: %w", err)
	}
	f.Close()

	// 3. 创建批处理脚本
	// 使用 cmd /c start /b 实现完全 detached
	batContent := fmt.Sprintf(`@echo off
timeout /t 2 /nobreak >nul
move /Y "%s" "%s"
if %%errorlevel%% equ 0 (
    start "" "%s"
)
del "%%~f0"
`, tmpExe, curExe, curExe)

	if err := os.WriteFile(tmpBat, []byte(batContent), 0644); err != nil {
		os.Remove(tmpExe)
		return fmt.Errorf("创建更新脚本失败: %w", err)
	}

	// 4. 执行批处理脚本（detached），然后退出
	cmd := exec.Command("cmd.exe", "/c", "start", "/b", tmpBat)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		os.Remove(tmpExe)
		os.Remove(tmpBat)
		return fmt.Errorf("启动更新脚本失败: %w", err)
	}

	// 退出本进程
	os.Exit(0)
	return nil
}
