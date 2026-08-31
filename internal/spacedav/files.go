package spacedav

import (
	"bytes"
	"io"
	"os"
	"path"
	"time"
)

// readFile 是一个只读的远端对象。
//
// 内容来自控制面签发的预签名 GET URL，边读边流，不整体落盘。
type readFile struct {
	name string
	body io.ReadCloser
	size int64
	read int64
}

func newReadFile(key string, body io.ReadCloser, size int64) *readFile {
	return &readFile{name: path.Base(key), body: body, size: size}
}

func (r *readFile) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.read += int64(n)
	return n, err
}

func (r *readFile) Close() error { return r.body.Close() }

// Seek 只支持「原地查询当前位置」与「回到开头前的 0 偏移」这两种退化用法。
//
// 远端是一次性的流，真正的随机读需要 Range 请求，而控制面的预签名 URL 没有暴露该能力。
// 资源管理器复制文件时是顺序读，不依赖 Seek；返回错误比假装成功更安全——假装成功会让
// 上层拿到错位的数据。
func (r *readFile) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekCurrent {
		return r.read, nil
	}
	if whence == io.SeekEnd && offset == 0 {
		return r.size, nil
	}
	return 0, os.ErrInvalid
}

func (r *readFile) Write([]byte) (int, error) { return 0, os.ErrPermission }

func (r *readFile) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

func (r *readFile) Stat() (os.FileInfo, error) {
	return &fileInfo{name: r.name, size: r.size, modTime: time.Now()}, nil
}

// writeFile 在内存里攒好内容，Close 时一次性经控制面上传。
//
// 为什么要攒：配额是在签发预签名 URL 时判定的，而判定需要**确切的字节数**。
// 流式上传拿不到总长，只能上报 0，配额就等于失效。攒到 Close 才知道真实大小，
// 这是让配额真正生效的代价。
//
// 因此大文件会占用等量内存。资源管理器里拖入超大文件是已知的内存代价——真正的解法是
// 分块上传 + 分块配额核算，那需要控制面先支持 multipart 签发。
type writeFile struct {
	fs     *FS
	key    string
	buffer bytes.Buffer
	closed bool
}

func newWriteFile(fs *FS, key string) *writeFile {
	return &writeFile{fs: fs, key: key}
}

func (w *writeFile) Write(p []byte) (int, error) {
	if w.closed {
		return 0, os.ErrClosed
	}
	return w.buffer.Write(p)
}

func (w *writeFile) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	token, err := w.fs.tokenOrErr()
	if err != nil {
		return err
	}
	size := int64(w.buffer.Len())
	// 用后台 context：资源管理器可能在 Close 前就断开请求，那不该中断已经收全的上传
	ctx, cancel := w.fs.uploadContext()
	defer cancel()
	if err := w.fs.client.Upload(ctx, token, w.fs.space, w.key, &w.buffer, size); err != nil {
		return err
	}
	// 文件落了地，其所在目录不再需要「空目录」记录
	w.fs.clearPendingDir(path.Dir(w.key))
	w.fs.invalidate()
	return nil
}

func (w *writeFile) Read([]byte) (int, error) { return 0, os.ErrPermission }

func (w *writeFile) Seek(int64, int) (int64, error) { return 0, os.ErrInvalid }

func (w *writeFile) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

func (w *writeFile) Stat() (os.FileInfo, error) {
	return &fileInfo{name: path.Base(w.key), size: int64(w.buffer.Len()), modTime: time.Now()}, nil
}

// dirFile 是一次目录列举的结果。
type dirFile struct {
	info    *fileInfo
	entries []os.FileInfo
	offset  int
}

func (d *dirFile) Read([]byte) (int, error) { return 0, os.ErrInvalid }

func (d *dirFile) Write([]byte) (int, error) { return 0, os.ErrPermission }

func (d *dirFile) Close() error { return nil }

func (d *dirFile) Seek(offset int64, whence int) (int64, error) {
	// webdav 库在重读目录前会 Seek(0, 0)，需要支持，否则第二次列举会空
	if offset == 0 && whence == io.SeekStart {
		d.offset = 0
		return 0, nil
	}
	return 0, os.ErrInvalid
}

func (d *dirFile) Readdir(count int) ([]os.FileInfo, error) {
	if d.offset >= len(d.entries) {
		if count <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if count <= 0 {
		rest := d.entries[d.offset:]
		d.offset = len(d.entries)
		return rest, nil
	}
	end := d.offset + count
	if end > len(d.entries) {
		end = len(d.entries)
	}
	batch := d.entries[d.offset:end]
	d.offset = end
	return batch, nil
}

func (d *dirFile) Stat() (os.FileInfo, error) { return d.info, nil }

// fileInfo 是 os.FileInfo 的最小实现。
type fileInfo struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
}

func (fi *fileInfo) Name() string { return fi.name }

func (fi *fileInfo) Size() int64 { return fi.size }

func (fi *fileInfo) Mode() os.FileMode {
	if fi.isDir {
		return os.ModeDir | 0o755
	}
	return 0o644
}

func (fi *fileInfo) ModTime() time.Time { return fi.modTime }

func (fi *fileInfo) IsDir() bool { return fi.isDir }

func (fi *fileInfo) Sys() any { return nil }
