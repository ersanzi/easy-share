package drive

import (
	"context"
	"fmt"
	"io"
	"os"
)

// ProgressFunc 汇报已发送字节与总字节数。
type ProgressFunc func(sent, total int64)

// progressReader 在读取时汇报进度。预签名 PUT 是单请求上传，
// 进度只能从「已读出多少字节」推断——读到即已交给传输层。
type progressReader struct {
	reader   io.Reader
	total    int64
	sent     int64
	onReport ProgressFunc
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)
	if n > 0 {
		p.sent += int64(n)
		if p.onReport != nil {
			p.onReport(p.sent, p.total)
		}
	}
	return n, err
}

// UploadFile 把本地文件上传到指定空间的相对路径 objectPath，期间回调进度。
// space 传空按个人空间，onProgress 可为 nil。
func (c *Client) UploadFile(ctx context.Context, token, space, objectPath, localPath string, onProgress ProgressFunc) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("读取文件信息失败：%w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("不能上传目录：%s", localPath)
	}
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("打开文件失败：%w", err)
	}
	defer file.Close()

	size := info.Size()
	var body io.Reader = file
	if onProgress != nil {
		body = &progressReader{reader: file, total: size, onReport: onProgress}
	}
	return c.Upload(ctx, token, space, objectPath, body, size)
}
