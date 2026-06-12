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

	// 自定义 redirect 策略：始终不转发 Authorization header
	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			// 清除 Authorization header，避免 S3 签名冲突
			req.Header.Del("Authorization")
			return nil
		},
	}
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败: HTTP %d (URL: %s)", resp.StatusCode, downloadURL)
	}

	// 验证 Content-Length 合理（至少 5MB，我们的 exe 约 14MB）
	minSize := int64(5 * 1024 * 1024)
	if resp.ContentLength > 0 && resp.ContentLength < minSize {
		return fmt.Errorf("文件大小异常 (%d bytes)，下载可能不完整", resp.ContentLength)
	}

	f, err := os.Create(tmpExe)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	written, copyErr := io.Copy(f, resp.Body)
	f.Close()
	if copyErr != nil {
		os.Remove(tmpExe)
		return fmt.Errorf("写入文件失败: %w", copyErr)
	}

	// 二次验证：实际写入大小
	if written < minSize {
		os.Remove(tmpExe)
		return fmt.Errorf("下载不完整: 仅收到 %.1f MB (预期 > 5 MB)", float64(written)/(1024*1024))
	}

	// 验证 PE 文件头
	if !isValidPE(tmpExe) {
		os.Remove(tmpExe)
		return fmt.Errorf("下载文件校验失败: 不是有效的可执行文件")
	}

	// 3. 创建批处理脚本
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

// isValidPE 检查文件是否为有效的 Windows PE 可执行文件。
func isValidPE(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// DOS header: 最开始 2 字节必须是 "MZ"
	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	if magic[0] != 'M' || magic[1] != 'Z' {
		return false
	}

	// 读取 e_lfanew 偏移（位于 DOS header 偏移 0x3C 处，4 字节 LE）
	if _, err := f.Seek(0x3C, io.SeekStart); err != nil {
		return false
	}
	var peOffset [4]byte
	if _, err := io.ReadFull(f, peOffset[:]); err != nil {
		return false
	}
	offset := int64(peOffset[0]) | int64(peOffset[1])<<8 | int64(peOffset[2])<<16 | int64(peOffset[3])<<24

	// PE signature: "PE\0\0"
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return false
	}
	var peSig [4]byte
	if _, err := io.ReadFull(f, peSig[:]); err != nil {
		return false
	}
	return peSig[0] == 'P' && peSig[1] == 'E' && peSig[2] == 0 && peSig[3] == 0
}
