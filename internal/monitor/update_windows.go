//go:build windows

package monitor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// DownloadAndReplace 下载新版本 exe，通过批处理脚本实现自替换。
// 下载失败返回 error 供前端提示；成功后通过延时退出确保前端收到响应。
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

	// 传输层超时：连接 20s，响应头 30s，整体 2min
	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 20 * time.Second}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			req.Header.Del("Authorization")
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("创建下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "claude-code-monitor")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	// 验证 Content-Length 合理（至少 5MB）
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
		return fmt.Errorf("下载不完整: 仅收到 %.1f MB", float64(written)/(1024*1024))
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

	// 4. 执行批处理脚本（detached）
	cmd := exec.Command("cmd.exe", "/c", "start", "/b", tmpBat)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		os.Remove(tmpExe)
		os.Remove(tmpBat)
		return fmt.Errorf("启动更新脚本失败: %w", err)
	}

	// 5. 延时退出，让 Wails RPC 有时间把成功响应发回前端
	time.AfterFunc(500*time.Millisecond, func() { os.Exit(0) })
	return nil
}

// isValidPE 检查文件是否为有效的 Windows PE 可执行文件。
func isValidPE(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	if magic[0] != 'M' || magic[1] != 'Z' {
		return false
	}

	if _, err := f.Seek(0x3C, io.SeekStart); err != nil {
		return false
	}
	var peOffset [4]byte
	if _, err := io.ReadFull(f, peOffset[:]); err != nil {
		return false
	}
	offset := int64(peOffset[0]) | int64(peOffset[1])<<8 | int64(peOffset[2])<<16 | int64(peOffset[3])<<24

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return false
	}
	var peSig [4]byte
	if _, err := io.ReadFull(f, peSig[:]); err != nil {
		return false
	}
	return peSig[0] == 'P' && peSig[1] == 'E' && peSig[2] == 0 && peSig[3] == 0
}
