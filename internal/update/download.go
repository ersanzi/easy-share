package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Download 流式下载 url 到 destPath（先写同目录 .part 临时文件，校验通过后改名落位），
// 边下边算 SHA256 并回调进度。expectSHA256 非空时强校验；expectSize > 0 时校验大小。
// progress 可为 nil；回调频率与网络分片一致，调用方自行节流。
// httpClient 由调用方提供（下载耗时长，不适用控制面客户端的 15 秒超时）。
func Download(ctx context.Context, httpClient *http.Client, url, destPath, expectSHA256 string, expectSize int64, progress func(received, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("构造下载请求: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发起下载: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("创建下载目录: %w", err)
	}
	tempPath := destPath + ".part"
	file, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}()

	hash := sha256.New()
	counting := &countingReader{inner: resp.Body, progress: progress, total: resp.ContentLength}
	if _, err := io.Copy(io.MultiWriter(file, hash), counting); err != nil {
		return fmt.Errorf("下载中断: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("落盘: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}

	if expectSize > 0 && counting.received != expectSize {
		return fmt.Errorf("大小校验失败：期望 %d 字节，实际 %d 字节", expectSize, counting.received)
	}
	if expectSHA256 != "" {
		actual := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(actual, expectSHA256) {
			return fmt.Errorf("SHA256 校验失败：期望 %s，实际 %s", strings.ToLower(expectSHA256), actual)
		}
	}
	if err := os.Rename(tempPath, destPath); err != nil {
		return fmt.Errorf("落位安装包: %w", err)
	}
	return nil
}

// countingReader 包装下载流，统计已读字节并回调进度。
type countingReader struct {
	inner    io.Reader
	progress func(received, total int64)
	received int64
	total    int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.received += int64(n)
	if r.progress != nil {
		r.progress(r.received, r.total)
	}
	return n, err
}

// 清理过期的升级安装包：删除 updates 目录下除 keep 以外的所有文件。
// keep 为空串时全部删除（应用成功后旧包即无用）。
func CleanStaleDownloads(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if keep != "" && entry.Name() == filepath.Base(keep) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
