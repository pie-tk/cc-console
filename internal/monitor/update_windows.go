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
	"time"
)

// DownloadAndReplace 下载新版本安装包，静默运行完成自替换。
// 不再自行 rename/copy，由 Inno Setup 安装程序负责替换文件并重启应用。
func DownloadAndReplace(downloadURL string, onProgress func(downloaded, total int64)) error {
	curExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前程序路径失败: %w", err)
	}
	curExe, err = filepath.EvalSymlinks(curExe)
	if err != nil {
		return fmt.Errorf("解析程序路径失败: %w", err)
	}
	installDir := filepath.Dir(curExe)

	tmpDir := os.TempDir()
	tmpSetup := filepath.Join(tmpDir, "claude-monitor-setup.exe")
	os.Remove(tmpSetup)

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

	f, err := os.Create(tmpSetup)
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
				os.Remove(tmpSetup)
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
			os.Remove(tmpSetup)
			return fmt.Errorf("读取下载流失败: %w", readErr)
		}
	}
	f.Close()

	if written < minSize {
		os.Remove(tmpSetup)
		return fmt.Errorf("下载不完整: 仅收到 %.1f MB", float64(written)/(1024*1024))
	}

	if onProgress != nil {
		onProgress(written, written)
	}

	if !isValidPE(tmpSetup) {
		os.Remove(tmpSetup)
		return fmt.Errorf("下载文件校验失败: 不是有效的可执行文件")
	}

	// 静默运行安装包，指向当前安装目录
	cmd := exec.Command(tmpSetup,
		"/VERYSILENT",
		"/SUPPRESSMSGBOXES",
		"/NORESTART",
		"/DIR="+installDir,
	)
	if err := cmd.Start(); err != nil {
		os.Remove(tmpSetup)
		return fmt.Errorf("启动安装程序失败: %w", err)
	}

	// 退出当前进程，让安装程序接管
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
