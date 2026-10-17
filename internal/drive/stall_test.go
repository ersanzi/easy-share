package drive

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// TestStallReaderKeepsSlowButMovingTransfer 钉住「慢 ≠ 挂死」。
//
// 这条是修复过程中的一次自我更正：第一版给每个分片设了**总时长**上限（1 分钟）。
// 那在慢链路上是错的——8 MiB 分片在 1 Mbps 链路上要 60 多秒，
// 会被反复误杀，而重试是**从头再传这一片**，于是越慢越永远完不成。
//
// 判据必须落在「有没有进展」而不是「花了多久」：
// 只要还有字节在流动就一直等，彻底不动才判死。
func TestStallReaderKeepsSlowButMovingTransfer(t *testing.T) {
	// 模拟慢链路：总共 200ms 才读完，但期间**一直在动**（远超过停滞窗口的比例）
	// 算术要成立：每 5ms 读 4 字节，120 字节 = 30 次 × 5ms = 150ms，
	// 远超 20ms 的停滞窗口——这样「没被误杀」才是真的证明。
	reader := &trickleReader{total: 120, delay: 5 * time.Millisecond}
	killed := false
	guard := newStallReader(reader, 20*time.Millisecond, func() { killed = true })
	defer guard.stop()

	start := time.Now()
	n, err := io.Copy(io.Discard, guard)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("慢但在动的读取不该报错: %v", err)
	}
	if killed {
		t.Fatalf("慢但在动的传输被误判为挂死（耗时 %v）——"+
			"慢链路会被反复重传，永远传不完", elapsed)
	}
	if n != 120 {
		t.Fatalf("读到的字节数 %d，期望 120", n)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("用例前提不成立：本该耗时 >150ms 的慢读取只用了 %v", elapsed)
	}
	t.Logf("✅ 慢但在动的传输（%v）未被误杀", elapsed.Round(time.Millisecond))
}

// TestStallReaderFiresWhenNoProgress 反面：彻底不动时必须触发（否则挂死无解）。
func TestStallReaderFiresWhenNoProgress(t *testing.T) {
	// 一个永远阻塞的 reader：Read 永不返回
	reader := &blockingReader{release: make(chan struct{})}
	killed := make(chan struct{})
	guard := newStallReader(reader, 20*time.Millisecond, func() { close(killed) })
	defer guard.stop()

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, guard)
		close(done)
	}()

	select {
	case <-killed:
		t.Log("✅ 停滞被检出（这就是把「永远挂住」变成「可重试错误」的那一步）")
	case <-time.After(2 * time.Second):
		t.Fatal("彻底不动却没有触发停滞判定——上传会无限期挂住")
	}
	close(reader.release)
	<-done
}

// trickleReader 每次只给少量字节、中间睡一下：模拟「慢但在动」。
type trickleReader struct {
	total int
	read  int
	delay time.Duration
}

func (r *trickleReader) Read(p []byte) (int, error) {
	if r.read >= r.total {
		return 0, io.EOF
	}
	if len(p) > 4 {
		p = p[:4]
	}
	time.Sleep(r.delay)
	data := bytes.Repeat([]byte{'x'}, min(4, r.total-r.read))
	n := copy(p, data)
	r.read += n
	return n, nil
}

// blockingReader 永远阻塞，直到 release 被关闭。
type blockingReader struct{ release chan struct{} }

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.release
	return 0, io.EOF
}
