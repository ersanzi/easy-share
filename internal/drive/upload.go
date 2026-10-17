package drive

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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

// fileMD5 整文件顺序读算 MD5（切片 6 的秒传判定键）。
//
// 代价是一次本地读——磁盘顺序读几百 MiB 只要几秒，而命中的回报是
// **整个上传字节路径归零**。算完把文件指针归位，调用方无感。
func fileMD5(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("计算文件 MD5 失败：%w", err)
	}
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("计算文件 MD5 失败：%w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("计算文件 MD5 失败：%w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// UploadFile 把本地文件上传到指定空间的相对路径 objectPath，期间回调进度。
// space 传空按个人空间，onProgress 可为 nil。
func (c *Client) UploadFile(ctx context.Context, token, space, objectPath, localPath string, onProgress ProgressFunc) error {
	return c.UploadFileIfVersion(ctx, token, space, objectPath, localPath, nil, onProgress)
}

// UploadFileIfVersion 与 UploadFile 相同，但带上乐观并发校验（路线图切片 1 的 CAS）
// 与内容哈希秒传判定（切片 6）。
//
// expectedVersion 语义与 PresignPutIfVersion 完全一致：nil 不校验、0 = 「我认为不存在」、
// n>0 = 「我看到的是第 n 版」。**单请求与分片两条路径都受它约束**——
// 分片路径把版本寄存在服务端会话上，在 complete（字节真正落地）时校验，
// 不会因为文件大就绕过 CAS。
//
// 上传前对本地文件整读算一次 MD5 并随签发上报：同空间已有同哈希内容时
// 服务端直接复制既有对象（秒传），本次不传输任何字节。
// 读取失败不影响上传正确性——退回「不带哈希」的上传（服务端不判秒传）。
func (c *Client) UploadFileIfVersion(ctx context.Context, token, space, objectPath, localPath string,
	expectedVersion *int64, onProgress ProgressFunc) error {
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
	// 哈希尽量算（秒传判定键）；本地读失败不拦上传，只放弃秒传
	hash, hashErr := fileMD5(file)
	if hashErr != nil {
		hash = ""
	}
	// 大文件走 Upload Session 分片（断点续传）；小文件单请求直传更省往返。
	// 分流阈值与会话恢复逻辑都封装在包内，调用方无感。
	if c.Sessions != nil && size >= multipartThreshold {
		return c.uploadFileMultipart(ctx, token, space, objectPath, localPath, size, info.ModTime(),
			expectedVersion, hash, onProgress)
	}
	var body io.Reader = file
	if onProgress != nil {
		body = &progressReader{reader: file, total: size, onReport: onProgress}
	}
	_, err = c.upload(ctx, token, space, objectPath, body, size, expectedVersion, hash)
	return err
}
