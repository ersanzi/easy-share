package spacedav

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"easyshare/internal/drive"
)

// fileUploadAwareClient 实现可选能力 fileUploader（生产 *drive.Client 满足），
// 用来验证「有该能力时优先走它」——即大文件走 Upload Session 分片而不是单请求。
type fileUploadAwareClient struct {
	*fakeClient
	uploadedPaths []string
	uploadErr     error
}

func (c *fileUploadAwareClient) UploadFile(ctx context.Context, token, space, objectPath, localPath string,
	onProgress drive.ProgressFunc) error {
	// 生产代码现在总是走带 CAS 的入口；测试桩把两个入口归一到同一实现，
	// 这样既验证“仍走本地路径分片通道”，也验证“CAS 接线没有改坏这条通道”。
	return c.UploadFileIfVersion(ctx, token, space, objectPath, localPath, nil, onProgress)
}

func (c *fileUploadAwareClient) UploadFileIfVersion(_ context.Context, _, _, objectPath, localPath string,
	_ *int64, _ drive.ProgressFunc) error {
	if c.uploadErr != nil {
		return c.uploadErr
	}
	// 记录「确实按本地路径上传了」，并核对路径上的字节数与文件大小一致
	fi, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("上传路径不可读: %w", err)
	}
	c.uploadedPaths = append(c.uploadedPaths, fmt.Sprintf("%s=%d", objectPath, fi.Size()))
	return nil
}

// TestUploadSpoolsToDiskNotMemory 钉住「拖大文件进资源管理器不能吃掉等量内存」。
//
// 背景：WebDAV 的写路径原先把整份内容攒在 bytes.Buffer 里，Close 时才发出。
// 这么做有它的理由（配额要在签发时知道确切字节数），代价却是
// **拖一个 5 GB 文件进来，Core 就要分配 5 GB 内存**——内存不足时直接 OOM 崩掉，
// 而不是给出可读错误。桌面端是与用户其他程序共享内存的进程，这个代价不可接受。
//
// 断言口径：写 64 MiB 后，进程堆增长必须远小于写入量。
// 用「远小于」而不是精确值，是为了容忍运行时噪声，同时**足以判定**
// 「整份留在内存里」这一实现（那会让堆至少增长 64 MiB）。
func TestUploadSpoolsToDiskNotMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("大块内存断言在 -short 下跳过")
	}
	client := newFake()
	fs := newFS(client, drive.SpacePersonal, false)
	fs.spoolDir = t.TempDir()

	const payloadSize = 64 << 20 // 64 MiB
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	file, err := fs.OpenFile(context.Background(), "/big.bin", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile 出错：%v", err)
	}
	// 分块喂入，模拟 WebDAV 的 io.Copy；单块 1 MiB，避免测试自己占用大内存
	chunk := make([]byte, 1<<20)
	written := 0
	for written < payloadSize {
		n, err := file.Write(chunk)
		if err != nil {
			t.Fatalf("Write 出错：%v", err)
		}
		written += n
	}
	runtime.GC()
	runtime.ReadMemStats(&after)

	heapGrowth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if heapGrowth > payloadSize/2 {
		t.Errorf("写入 %d 字节后堆增长 %d 字节——整份内容留在内存里了（应落盘暂存）",
			payloadSize, heapGrowth)
	}

	if err := file.Close(); err != nil {
		t.Fatalf("Close 出错：%v", err)
	}
	if got := client.uploads["big.bin"]; got != payloadSize {
		t.Fatalf("上传申报大小 = %d，应为 %d", got, payloadSize)
	}
}

// TestUploadUsesSpoolPathForMultipart 钉住「落盘后把本地路径交给 UploadFile」。
//
// 为什么重要：控制面早在 2026-09-06 就有了 Upload Session（分片 + 断点续传），
// drive.Client 也已按 32 MiB 阈值分流，但**这条能力从没接到 WebDAV 上**——
// 资源管理器拖进来的文件走的是单请求 PUT，大文件既不能续传也不能分片。
// 落盘暂存恰好把「本地路径」这个 UploadFile 需要的输入准备好了。
func TestUploadUsesSpoolPathForMultipart(t *testing.T) {
	base := newFake()
	client := &fileUploadAwareClient{fakeClient: base}
	fs := newFS(client, drive.SpacePersonal, false)
	fs.spoolDir = t.TempDir()

	file, err := fs.OpenFile(context.Background(), "/big.bin", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile 出错：%v", err)
	}
	if _, err := file.Write([]byte("0123456789")); err != nil {
		t.Fatalf("Write 出错：%v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close 出错：%v", err)
	}

	if len(client.uploadedPaths) != 1 {
		t.Fatalf("应恰好走一次 UploadFile（分片通道），实得 %v", client.uploadedPaths)
	}
	if want := "big.bin=10"; client.uploadedPaths[0] != want {
		t.Errorf("UploadFile 入参 = %q，期望 %q", client.uploadedPaths[0], want)
	}
	// 负断言：走分片通道时不该再走单请求 Upload（否则等于传了两遍）
	if len(base.uploads) != 0 {
		t.Errorf("走 UploadFile 后不得再走单请求 Upload：%v", base.uploads)
	}
}

// TestUploadSpoolIsCleanedUp 钉住暂存文件不残留。
//
// 暂存目录里每残留一个文件就多占一份磁盘；上传频繁时会把数据盘填满，
// 而用户看不到任何提示（文件明明已经传完了）。
func TestUploadSpoolIsCleanedUp(t *testing.T) {
	dir := t.TempDir()
	client := newFake()
	fs := newFS(client, drive.SpacePersonal, false)
	fs.spoolDir = dir

	file, err := fs.OpenFile(context.Background(), "/a.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile 出错：%v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("Write 出错：%v", err)
	}
	// 写入后、Close 前：暂存文件应当存在（证明内容确实在磁盘上）
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读暂存目录出错：%v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("写入后暂存目录应有 1 个文件，实得 %d", len(entries))
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close 出错：%v", err)
	}
	// Close 后必须清干净
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读暂存目录出错：%v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("Close 后应清空暂存目录，残留 %v", names)
	}
}

// TestUploadSpoolCleanedUpOnFailure 钉住上传失败也要清暂存。
//
// 失败路径更容易漏清理：正常路径每天走一万次都干净，失败路径一周才走一次，
// 没人会注意到磁盘在慢慢涨。
func TestUploadSpoolCleanedUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	client := newFake()
	client.uploadErr = fmt.Errorf("配额不足")
	fs := newFS(client, drive.SpacePersonal, false)
	fs.spoolDir = dir

	file, err := fs.OpenFile(context.Background(), "/a.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile 出错：%v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("Write 出错：%v", err)
	}
	// 先证明「确实落盘了」——否则下面的「目录为空」会平凡成立：
	// 一个根本没落盘的实现（比如攒内存的旧版本）同样留下空目录，
	// 于是这条用例会静默变成永远通过。这正是它第一版的问题。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读暂存目录出错：%v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("写入后暂存目录应有 1 个文件（否则根本没落盘），实得 %d", len(entries))
	}

	if err := file.Close(); err == nil {
		t.Fatal("上传失败时 Close 应报错")
	}

	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读暂存目录出错：%v", err)
	}
	if len(entries) != 0 {
		t.Errorf("上传失败后也应清空暂存目录，残留 %d 个", len(entries))
	}
}

// 编译期断言：生产类型必须满足 fileUploader，否则会静默退回单请求 PUT
// （大文件既不能分片也不能续传）。
var _ fileUploader = (*drive.Client)(nil)

var _ = io.Discard
var _ = filepath.Join
