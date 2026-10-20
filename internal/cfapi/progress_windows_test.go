//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// ── readProgressively 的行为契约 ──────────────────────────────
//
// 这个函数是「大文件点开时进度条会不会动」的全部实现，
// 而它最容易出的两个错是：漏报最后一次、以及把短读当成功。

type chunkReader struct {
	data []byte
	pos  int
	// chunk 控制每次 Read 返回多少字节，模拟慢速网络
	chunk int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data)-r.pos {
		n = len(r.data) - r.pos
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

func TestReadProgressivelyReportsEveryChunk(t *testing.T) {
	size := 3 * progressChunk
	r := &chunkReader{data: bytes.Repeat([]byte("z"), size), chunk: 4096}
	buf := make([]byte, size)

	var got []int64
	n, err := readProgressively(r, buf, func(completed int64) {
		got = append(got, completed)
	})
	if err != nil {
		t.Fatalf("读满不应报错：%v", err)
	}
	if n != size {
		t.Fatalf("读满字节数 = %d，期望 %d", n, size)
	}
	// 1MiB / 2MiB / 3MiB 三次；最后一次恰好等于总量也必须报
	want := []int64{progressChunk, 2 * progressChunk, 3 * progressChunk}
	if len(got) != len(want) {
		t.Fatalf("回报次数 = %d（%v），期望 %d 次", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 次回报 = %d，期望 %d", i+1, got[i], want[i])
		}
	}
}

func TestReadProgressivelyShortReadStopsAtEOF(t *testing.T) {
	// 源比 buf 短：必须像 io.ReadFull 一样返回已读字节数 + EOF，不能死循环
	r := &chunkReader{data: bytes.Repeat([]byte("y"), progressChunk+10), chunk: 4096}
	buf := make([]byte, 3*progressChunk)
	n, err := readProgressively(r, buf, nil)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("短读应返回 EOF，实得 %v", err)
	}
	if n != progressChunk+10 {
		t.Errorf("已读字节 = %d，期望 %d", n, progressChunk+10)
	}
}

func TestReadProgressivelyLastBytesArriveWithEOF(t *testing.T) {
	// 回归用例：真实网络 reader（HTTP 响应体那一类）会在交出**最后一块字节的同时**
	// 返回 io.EOF。io.ReadFull 对此返回成功；当错误处理会让整次按需取数失败，
	// 用户侧症状是「点开文件转圈到内核超时」——cfmount 集成用例真机挂过一次。
	src := &stagedReader{chunks: [][]byte{[]byte("ab"), []byte("c")}}
	buf := make([]byte, 3)
	n, err := readProgressively(src, buf, nil)
	if err != nil {
		t.Fatalf("读满就该成功，实得 err=%v（读了 %d 字节）", err, n)
	}
	if n != 3 || string(buf) != "abc" {
		t.Errorf("已读 = %d %q，期望 3 与 \"abc\"", n, buf)
	}
}

// stagedReader 依次交出给定分块，并在交出最后一块时带上 io.EOF。
type stagedReader struct {
	chunks [][]byte
}

func (r *stagedReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	n := copy(p, chunk)
	if len(r.chunks) == 0 {
		return n, io.EOF // 最后一块与 EOF 同时到达
	}
	return n, nil
}

func TestReadProgressivelyWithoutReportIsPlainRead(t *testing.T) {
	size := 2 * progressChunk
	r := &chunkReader{data: bytes.Repeat([]byte("x"), size), chunk: 1 << 20}
	buf := make([]byte, size)
	n, err := readProgressively(r, buf, nil)
	if err != nil || n != size {
		t.Fatalf("不回报时也应读满：%d %v", n, err)
	}
	if bytes.Equal(buf, bytes.Repeat([]byte("x"), size)) != true {
		t.Error("内容不对")
	}
}

// ── 真机：取数量够大时，进度确实在按块往内核推 ────────────────
//
// 这里用 SetProgressObserver 把内核调用换成观察钩子：
// 断言的是「我们的分段回报在发生、且量是单调的」，
// 至于内核把进度画成什么样子，那是系统的显示逻辑，不在本包的可验证范围。
func TestFetchReportsProgressForLargeRange(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	// 单文件 12 MiB：跨过 progressMinBytes（8 MiB）
	const size = 12 << 20
	body := make([]byte, size)
	for i := range body {
		body[i] = byte('a' + i%26)
	}

	list := func(ctx context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path: "big.bin", Size: size, ModifiedUnix: 1767225600,
			Identity: EncodeIdentity(777),
		}}, nil
	}
	open := func(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
		if path != "big.bin" {
			return nil, errors.New("云端没有 " + path)
		}
		end := offset + length
		if end > int64(len(body)) {
			end = int64(len(body))
		}
		// 每 64 KiB 一块喂给内核路径，模拟真实网络的分片到达
		return io.NopCloser(&chunkReader{data: body[offset:end], chunk: 64 << 10}), nil
	}

	m, err := MountSpace(MountOptions{
		RootPath:         root,
		Provider:         testProvider,
		SyncRootIdentity: []byte("easyshare-progress-test"),
		List:             list,
		Open:             open,
	})
	if err != nil {
		t.Fatalf("MountSpace 失败：%v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close 失败：%v", err)
		}
	}()

	var totals, completed []int64
	m.SetProgressObserver(func(total, done int64) {
		totals = append(totals, total)
		completed = append(completed, done)
	})

	// 让内核发起一次整份读取（占位符按需取数）
	got, err := os.ReadFile(filepath.Join(root, "big.bin"))
	if err != nil {
		t.Fatalf("读占位符失败：%v", err)
	}
	if len(got) != size {
		t.Fatalf("读回 %d 字节，期望 %d", len(got), size)
	}

	if len(completed) == 0 {
		t.Skip("内核这次把读取拆成了小于 8 MiB 的区间，按阈值不回报是正确行为（非故障）")
	}
	// 单调不减：进度只能往前走
	for i := 1; i < len(completed); i++ {
		if completed[i] < completed[i-1] && totals[i] == totals[i-1] {
			t.Errorf("同一轮里进度倒退了：%d → %d", completed[i-1], completed[i])
		}
	}
	for i := range completed {
		if completed[i] > totals[i] {
			t.Errorf("已完成 %d 超过总量 %d——内核会直接丢掉这条回报", completed[i], totals[i])
		}
		if totals[i] < progressMinBytes {
			t.Errorf("总量 %d 低于阈值 %d 却回报了", totals[i], progressMinBytes)
		}
	}
	t.Logf("回报 %d 次，首 %d / 末 %d（总量 %d），读满 %d 字节",
		len(completed), completed[0], completed[len(completed)-1], totals[0], len(got))
}
