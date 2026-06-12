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

// DownloadAndReplace 下载新版本 exe 并完成自替换。
// 策略：先 rename 当前 exe（NTFS 允许 rename 运行中的文件），
// 再复制新文件到原路径，然后启动新进程并退出。
// 如果 rename 失败，回退到批处理脚本方式。
func DownloadAndReplace(downloadURL string, onProgress func(downloaded, total int64)) error {
	curExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前程序路径失败: %w", err)
	}
	curExe, err = filepath.EvalSymlinks(curExe)
	if err != nil {
		return fmt.Errorf("解析程序路径失败: %w", err)
	}

	tmpDir := os.TempDir()
	tmpExe := filepath.Join(tmpDir, "claude-monitor-update.exe")

	os.Remove(tmpExe)

	// 传输层超时
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 20 * time.Second}).DialContext,
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

	minSize := int64(5 * 1024 * 1024)
	if resp.ContentLength > 0 && resp.ContentLength < minSize {
		return fmt.Errorf("文件大小异常 (%d bytes)，下载可能不完整", resp.ContentLength)
	}

	f, err := os.Create(tmpExe)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}

	var written int64
	buf := make([]byte, 32*1024)
	lastPercent := -1
	total := resp.ContentLength
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			nw, writeErr := f.Write(buf[:n])
			if writeErr != nil {
				f.Close()
				os.Remove(tmpExe)
				return fmt.Errorf("写入文件失败: %w", writeErr)
			}
			written += int64(nw)
			if onProgress != nil && total > 0 {
				pct := int(written * 100 / total)
				if pct != lastPercent {
					lastPercent = pct
					onProgress(written, total)
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			f.Close()
			os.Remove(tmpExe)
			return fmt.Errorf("读取下载流失败: %w", readErr)
		}
	}
	f.Close()

	if written < minSize {
		os.Remove(tmpExe)
		return fmt.Errorf("下载不完整: 仅收到 %.1f MB", float64(written)/(1024*1024))
	}

	if onProgress != nil {
		onProgress(written, written)
	}

	if !isValidPE(tmpExe) {
		os.Remove(tmpExe)
		return fmt.Errorf("下载文件校验失败: 不是有效的可执行文件")
	}

	// 尝试直接替换：先 rename 旧 exe（NTFS 允许 rename 运行中的文件），
	// 再复制新文件到原路径，然后启动新进程。
	oldExe := curExe + ".old"
	os.Remove(oldExe) // 清理上次残留

	if err := os.Rename(curExe, oldExe); err == nil {
		// rename 成功，复制新文件到原路径
		if err := copyFile(tmpExe, curExe); err == nil {
			os.Remove(tmpExe)
			os.Remove(oldExe) // 非致命，旧文件可以下次清理
			exec.Command(curExe).Start()
			time.AfterFunc(500*time.Millisecond, func() { os.Exit(0) })
			return nil
		}
		// 复制失败，恢复旧文件
		os.Rename(oldExe, curExe)
		os.Remove(tmpExe)
		return fmt.Errorf("无法写入新版本到目标路径")
	}

	// rename 失败，回退到批处理脚本
	if onProgress != nil {
		onProgress(written, written)
	}

	tmpBat := filepath.Join(tmpDir, "claude-monitor-update.bat")
	os.Remove(tmpBat)

	// 批处理使用 ping 做延迟（timeout 在无控制台环境可能失效），
	// 最多重试 30 次，每次等 1 秒（共 30 秒窗口）。
	batContent := fmt.Sprintf(`@echo off
set count=0
:retry
ping -n 2 127.0.0.1 >nul
copy /Y "%s" "%s" >nul 2>&1
if not errorlevel 1 (
    start "" "%s"
    del "%s"
    del "%%~f0" & exit
)
set /a count+=1
if %%count%% lss 30 goto retry
del "%%~f0"
`, tmpExe, curExe, curExe, tmpExe)

	if err := os.WriteFile(tmpBat, []byte(batContent), 0644); err != nil {
		os.Remove(tmpExe)
		return fmt.Errorf("创建更新脚本失败: %w", err)
	}

	cmd := exec.Command("cmd.exe", "/c", tmpBat)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		os.Remove(tmpExe)
		os.Remove(tmpBat)
		return fmt.Errorf("启动更新脚本失败: %w", err)
	}

	time.AfterFunc(1*time.Second, func() { os.Exit(0) })
	return nil
}

// copyFile 复制文件内容并保留基本权限。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
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
