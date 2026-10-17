//go:build windows

package cfapi

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestFetchOnRead 是切片 5 的**决定性验收**：在同步根里建一个占位符，
// 然后真的去读它，断言：
//
//  1. 读取动作**触发了 FETCH_DATA 回调**（"点开才拉"的机制成立）；
//  2. 回调里回填的数据**真的出现在读取结果里**（数据通路正确）；
//  3. 读完之前，磁盘上不占数据块（"列表零下载"没被破坏）。
//
// 与 TestCreatePlaceholder 的区别：那条只证明占位符建得出来（元数据正确），
// 这条证明**内容是按需取回的**——两半合起来才是完整的占位符语义。
//
// 注意：本用例会真的触发系统 I/O 与回调线程，故设了超时保护。
func TestFetchOnRead(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	// 回调里要回填的内容：故意用一个可识别的模式，
	// 这样「读到的内容 == 我们给的」才能证明数据真的过了回调。
	const content = "hello from the cloud! 这是云端内容。"
	cloudBytes := []byte(content)

	var fetchCalls atomic.Int32
	var lastPath atomic.Value

	fetcher := func(req FetchRequest) FetchResult {
		fetchCalls.Add(1)
		lastPath.Store(req.NormalizedPath)
		// 简单实现：整份返回。真实实现要按 RequiredOffset/Length 做范围读取。
		return FetchResult{
			Data:   cloudBytes,
			Offset: 0,
		}
	}

	sr, err := NewSyncRoot(testProvider,
		[]byte("easyshare-syncroot-fetch-test"),
		[]byte("easyshare-fileid-"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterSyncRoot(root, &sr.Registration, &sr.Policies, RegisterFlagNone); err != nil {
		t.Fatalf("RegisterSyncRoot 失败: %v", err)
	}
	defer func() { _ = UnregisterSyncRoot(root) }()

	// 连接同步根并注册 FETCH_DATA 回调
	sess, err := Connect(root, fetcher,
		ConnectFlagRequireProcessInfo|ConnectFlagRequireFullFilePath)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	defer func() { _ = sess.Close() }()

	// 建占位符（内容尚不在本地）
	ph := NewPlaceholders([]PlaceholderSpec{{
		RelativeName: "hello.txt",
		Size:         int64(len(cloudBytes)),
		ModifiedUnix: 1767225600,
		CreatedUnix:  1767225600,
		Identity:     []byte("easyshare-fileid-000042"),
	}})
	if _, err := CreatePlaceholders(root, ph.Infos, CreateFlagNone); err != nil {
		t.Fatalf("CreatePlaceholders 失败: %v", err)
	}
	if r := ph.Infos[0].Result; r != 0 {
		t.Fatalf("占位符 Result = 0x%08x", uint32(r))
	}

	p := filepath.Join(root, "hello.txt")

	// 读之前：不占数据块
	if alloc := allocatedSize(t, p); alloc > 4096 {
		t.Fatalf("读之前磁盘分配 = %d，期望接近 0", alloc)
	}

	// ── 触发读取 ────────────────────────────────────────────────
	// 用一个独立的 goroutine 读，主 goroutine 负责超时保护：
	// 若回调没接上，这个读会挂住——超时能让用例失败而不是永久卡住。
	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		b, err := os.ReadFile(p)
		done <- readResult{b, err}
	}()

	var got readResult
	select {
	case got = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("读取超时（20s）——FETCH_DATA 回调可能未被调用或未回 CfExecute")
	}

	// ── 断言 ────────────────────────────────────────────────────
	if got.err != nil {
		t.Fatalf("读取失败: %v（回调已触发 %d 次）", got.err, fetchCalls.Load())
	}
	if n := fetchCalls.Load(); n == 0 {
		t.Error("读取成功但 FETCH_DATA 回调一次都没触发——数据不是按需取回的")
	}
	if string(got.data) != content {
		t.Errorf("读到的内容不匹配：\n got=%q\nwant=%q", got.data, content)
	}
	t.Logf("回调触发 %d 次；路径=%v；读回 %d 字节", fetchCalls.Load(), lastPath.Load(), len(got.data))
}
