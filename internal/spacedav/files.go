package spacedav

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"easyshare/internal/drive"
	"easyshare/internal/httprange"
)

// readFile 是一个只读的远端对象。
//
// 内容来自控制面签发的预签名 GET URL，边读边流，不整体落盘。
type readFile struct {
	// fs/key 用于「回到开头」时重开流（见 Seek）；为 nil 时该能力不可用。
	fs   *FS
	key  string
	name string
	body io.ReadCloser
	size int64
	read int64
}

func newReadFile(fs *FS, key string, body io.ReadCloser, size int64) *readFile {
	return &readFile{fs: fs, key: key, name: path.Base(key), body: body, size: size}
}

func (r *readFile) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.read += int64(n)
	return n, err
}

func (r *readFile) Close() error { return r.body.Close() }

// Seek 支持 `http.ServeContent` 实际需要的那几种调用。
//
// ⚠️ **这不是可选的实现细节，而是「文件能不能下载」的前提**（2026-10-16 实测）。
// `webdav.Handler` 的 GET 分支把文件交给 `http.ServeContent`，而后者会
//
//  1. Read(512)                  ← 探测 Content-Type，**先消费数据**
//  2. Seek(0, io.SeekStart)      ← "rewind to output whole file"，**回退重放**
//  3. 整份 io.Copy 到响应
//
// 顺序与直觉相反：**先读、后回退**。旧实现只在 `read == 0` 时接受 `(0, SeekStart)`，
// 于是第 2 步必失败 → ServeContent 直接
//
//	http.Error(w, "seeker can't seek", http.StatusInternalServerError)
//
// 结果：**从「此电脑」双击任何文件都是 HTTP 500**（不只是拖进度条）。
// 这是端到端测试（走真实 webdav.Handler）才暴露出来的——单测 Seek 全绿也发现不了。
//
// 实现要点：远端流不能倒带，`(0, SeekStart)` 只能**重开一条从 0 起的流**。
// 这需要客户端支持预签名（openRangeer）；不支持时如实报错，
// 延续「不假装成功」的取向——假装成功会让上层拿到错位数据。
func (r *readFile) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart {
		if r.read == 0 {
			return 0, nil // 已在开头，无需重开
		}
		return r.reopen()
	}
	if offset == 0 && whence == io.SeekCurrent {
		return r.read, nil
	}
	if whence == io.SeekEnd && offset == 0 {
		return r.size, nil
	}
	return 0, os.ErrInvalid
}

// openRangeer 是可选能力：能签发下载用预签名 URL 的客户端。
//
// *drive.Client 满足（PresignGet 是导出方法）。有它才能「回到开头」——
// 即重开一条从 0 起的流（见 reopen）。
type openRangeer interface {
	PresignGet(ctx context.Context, token, space, path string) (string, error)
}

// reopen 重开一条从 0 到对象末尾的流，供 Seek(0, SeekStart) 使用。
//
// 不支持预签名的客户端返回 ErrInvalid——保持旧行为，让它明确失败而不是给错数据。
func (r *readFile) reopen() (int64, error) {
	pr, ok := r.fs.client.(openRangeer)
	if r.fs == nil || !ok {
		return 0, os.ErrInvalid
	}
	token, err := r.fs.tokenOrErr()
	if err != nil {
		return 0, err
	}
	url, err := pr.PresignGet(context.Background(), token, r.fs.space, r.key)
	if err != nil {
		return 0, err
	}
	rc, err := httprange.NewFetcher(func(context.Context) (string, error) { return url, nil }).
		Fetch(context.Background(), 0, r.size)
	if err != nil {
		return 0, err
	}
	_ = r.body.Close()
	r.body = rc
	r.read = 0
	return 0, nil
}

func (r *readFile) Write([]byte) (int, error) { return 0, os.ErrPermission }

func (r *readFile) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

func (r *readFile) Stat() (os.FileInfo, error) {
	return &fileInfo{name: r.name, size: r.size, modTime: time.Now()}, nil
}

// fileUploader 是可选能力：能按**本地文件路径**上传（走 Upload Session 分片）的客户端。
//
// *drive.Client 满足（UploadFile 是导出方法，内部按 32 MiB 阈值分流到分片 + 断点续传）。
// 有它时暂存到磁盘的内容可以**直接把路径交出去**，字节由 client 自己按分片读，
// 不经过本包内存；没有它时退回「读回内存再单请求 PUT」。
type fileUploader interface {
	UploadFile(ctx context.Context, token, space, objectPath, localPath string,
		onProgress drive.ProgressFunc) error
	UploadFileIfVersion(ctx context.Context, token, space, objectPath, localPath string,
		expectedVersion *int64, onProgress drive.ProgressFunc) error
}

// writeFile 把内容**暂存到磁盘**，Close 时交给控制面上传。
//
// 为什么必须先落到某处：配额是在签发预签名 URL 时判定的，而判定需要**确切的字节数**。
// 流式上传拿不到总长，只能上报 0，配额就等于失效。落盘才能既拿到真实大小、
// 又不吃掉等量内存。
//
// **为什么不是内存**（2026-10-16 改）：原实现用 bytes.Buffer 攒整份内容。
// 资源管理器里拖入大文件时，Core 会分配与文件等量的内存——拖一个 5 GB 文件
// 就是 5 GB 分配。桌面端是与用户其他程序共享内存的进程，内存不足时直接 OOM 崩掉，
// 连可读错误都留不下。注释里当时写的理由是「需要控制面先支持 multipart 签发」，
// 而那条前提**早在 2026-09-06 就成立了**（`/upload-session/*` 四端点 + 客户端
// 32 MiB 分流），只是这份代码没跟上。
//
// 落盘还顺带解锁了分片：有了本地路径，就能交给 {@link fileUploader} 走
// Upload Session（大文件分片 + 断点续传），而不是一次次单请求 PUT。
type writeFile struct {
	fs  *FS
	key string
	// spool 是磁盘暂存。Close 无论成败都要删掉它。
	spool     *os.File
	spoolPath string
	size      int64
	closed    bool
}

// newWriteFile 建一个落盘暂存的写句柄。
//
// 失败直接返回错误（而不是退化成内存缓冲）：暂存建不出来通常意味着数据盘满或
// 目录不可写，那正是用户需要知道的事；悄悄改成吃内存会把「磁盘满」变成一个
// 更难诊断的 OOM。
func newWriteFile(fs *FS, key string) (*writeFile, error) {
	dir := fs.spoolDir
	if dir == "" {
		dir = os.TempDir()
	}
	file, err := os.CreateTemp(dir, "easyshare-upload-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("创建上传暂存文件失败（请检查数据盘空间）：%w", err)
	}
	return &writeFile{fs: fs, key: key, spool: file, spoolPath: file.Name()}, nil
}

func (w *writeFile) Write(p []byte) (int, error) {
	if w.closed {
		return 0, os.ErrClosed
	}
	n, err := w.spool.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *writeFile) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	// 无论成败都清掉暂存：残留的每一个文件都白占一份磁盘，
	// 而用户看不到任何提示（文件明明已经传完了）。
	defer func() {
		_ = w.spool.Close()
		_ = os.Remove(w.spoolPath)
	}()

	token, err := w.fs.tokenOrErr()
	if err != nil {
		return err
	}
	// 用后台 context：资源管理器可能在 Close 前就断开请求，那不该中断已经收全的上传
	ctx, cancel := w.fs.uploadContext()
	defer cancel()

	// 必须先把落盘内容刷出去再交给别人按路径读，否则读到的是不完整文件
	if err := w.spool.Sync(); err != nil {
		return fmt.Errorf("上传暂存落盘失败：%w", err)
	}

	uploadErr := w.upload(ctx, token)
	// 版本冲突：资源管理器 PUT 不弹窗，自动保存为冲突副本（双方内容都保留）。
	// 只有版本冲突才重试改名；权限/配额/存储错误原样返回，避免掩盖真实原因。
	if isVersionConflict(uploadErr) {
		if u, ok := w.fs.client.(fileUploader); ok {
			if err := w.uploadConflictCopy(ctx, token, u); err != nil {
				return err
			}
		} else {
			return uploadErr
		}
	} else if uploadErr != nil {
		return uploadErr
	}
	// 文件落了地，其所在目录不再需要「空目录」记录
	w.fs.clearPendingDir(path.Dir(w.key))
	w.fs.invalidate()
	return nil
}

// upload 优先按本地路径交给支持分片的客户端，否则退回单请求 PUT。
//
// ⚠️ **必须带 CAS**：本文件是资源管理器 PUT 的最终落点，不带版本就是静默覆盖。
// 版本来自 OpenFile 时的缓存快照（见 WriteFile.new）；如果快照失效/未命中，
// 就传 0 表示“我认为不存在”，控制面发现已存在会拒绝，从而把冲突交回这里处理。
func (w *writeFile) upload(ctx context.Context, token string) error {
	if u, ok := w.fs.client.(fileUploader); ok {
		return u.UploadFileIfVersion(ctx, token, w.fs.space, w.key, w.spoolPath,
			w.expectedVersion(), nil)
	}
	// 退化路径：读回内存再单请求 PUT。假客户端与旧控制面走这里。
	if _, err := w.spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return w.fs.client.Upload(ctx, token, w.fs.space, w.key, w.spool, w.size)
}

// isVersionConflict 判断是否是 CAS 拒绝（与 app.go 保持同一语义）。
func isVersionConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "已被其他人修改") ||
		strings.Contains(msg, "已被删除") ||
		strings.Contains(msg, "版本") && strings.Contains(msg, "已过期")
}

// expectedVersion 返回上传时带回控制面的版本指针。
func (w *writeFile) expectedVersion() *int64 {
	if v, ok := w.fs.versionOf(w.key); ok {
		return &v
	}
	zero := int64(0)
	return &zero
}

// uploadConflictCopy 冲突时生成副本（不覆盖对方内容）。
func (w *writeFile) uploadConflictCopy(ctx context.Context, token string, u fileUploader) error {
	// 重新列举以拿到最新路径集合；旧缓存可能不含刚被对方创建的文件。
	objects, err := w.fs.client.Objects(ctx, token, w.fs.space)
	if err != nil {
		return fmt.Errorf("上传冲突副本前刷新列表失败：%w", err)
	}
	key := w.key
	ext := path.Ext(key)
	base := strings.TrimSuffix(key, ext)
	if !existsPath(objects, base+" (冲突副本)"+ext) {
		key = base + " (冲突副本)" + ext
	} else {
		for i := 2; ; i++ {
			candidate := base + " (冲突副本" + strconv.Itoa(i) + ")" + ext
			if !existsPath(objects, candidate) {
				key = candidate
				break
			}
		}
	}
	zero := int64(0)
	if err := u.UploadFileIfVersion(ctx, token, w.fs.space, key, w.spoolPath, &zero, nil); err != nil {
		return fmt.Errorf("上传冲突副本 %s 失败：%w", key, err)
	}
	w.key = key
	return nil
}

func existsPath(objects []drive.Object, key string) bool {
	for _, obj := range objects {
		if obj.Path == key {
			return true
		}
	}
	return false
}

func (w *writeFile) Read([]byte) (int, error) { return 0, os.ErrPermission }

func (w *writeFile) Seek(int64, int) (int64, error) { return 0, os.ErrInvalid }

func (w *writeFile) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

func (w *writeFile) Stat() (os.FileInfo, error) {
	return &fileInfo{name: path.Base(w.key), size: w.size, modTime: time.Now()}, nil
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
