package spacedav

import (
	"bytes"
	"context"
	"io"
	"testing"

	"easyshare/internal/drive"
)

// TestSeekContractForServeContent 钉住 `readFile.Seek` 与 `http.ServeContent` 的契约。
//
// 为什么单独测它：`internal/spacedav` 的下载路径是
//
//	webdav.Handler → http.ServeContent → readFile.Seek/Read
//
// 而 ServeContent 的调用序列**与直觉相反**：
//
//	Read(512)              ← 探测 Content-Type，先消费数据
//	Seek(0, SeekStart)     ← "rewind to output whole file"，要求回退
//	Seek(0, SeekEnd)       ← 取大小
//	Seek(ra.start, ...)    ← Range 时定位
//
// 旧实现只接受 `(0, SeekCurrent)` 与 `(0, SeekEnd)`，**漏掉了 `(0, SeekStart)`**，
// 于是 ServeContent 直接以 `seeker can't seek` 回 HTTP 500——
// **从「此电脑」双击任何云盘文件都打不开**。该缺陷靠单元测试发现不了，
// 是走真实 `webdav.Handler` 的端到端用例（range_contract_test.go）暴露的。
//
// 本用例把这条契约钉死：这些调用**必须**被接受。
func TestSeekContractForServeContent(t *testing.T) {
	payload := []byte("0123456789abcdefghij")
	fc := newRangeAware(payload)
	defer fc.Close()

	fs := New(Options{Client: fc, Space: drive.SpacePersonal,
		Token: func() string { return "tok" }, Label: "test"})
	f, err := fs.OpenFile(context.Background(), "big.bin", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// ① 先读一段（复刻 ServeContent 的探测读）
	head := make([]byte, 5)
	if _, err := io.ReadFull(f, head); err != nil {
		t.Fatalf("探测读失败: %v", err)
	}
	if !bytes.Equal(head, payload[:5]) {
		t.Fatalf("探测读内容 = %q，期望 %q", head, payload[:5])
	}

	// ② 回到开头 —— **这一步就是旧实现 500 的根因**
	pos, err := f.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek(0, SeekStart) 失败：%v —— ServeContent 会因此回 500，整份下载不可用", err)
	}
	if pos != 0 {
		t.Fatalf("Seek(0, SeekStart) 返回 %d，期望 0", pos)
	}

	// ③ 回退后必须能重新读到开头的数据（否则是"假装成功"）
	again := make([]byte, 5)
	if _, err := io.ReadFull(f, again); err != nil {
		t.Fatalf("回退后重读失败: %v", err)
	}
	if !bytes.Equal(again, payload[:5]) {
		t.Fatalf("回退后读到 %q，期望 %q（回退没生效 = 静默错数据）", again, payload[:5])
	}

	// ④ ServeContent 还会问大小
	if pos, err := f.Seek(0, io.SeekEnd); err != nil || pos != int64(len(payload)) {
		t.Fatalf("Seek(0, SeekEnd) = (%d,%v)，期望 (%d,nil)", pos, err, len(payload))
	}

	// ⑤ 查询当前位置
	if pos, err := f.Seek(0, io.SeekCurrent); err != nil && pos != int64(len(payload)) {
		t.Fatalf("Seek(0, SeekCurrent) 失败: %v", err)
	}
}

// TestSeekUnsupportedClientFailsLoudly 钉住「不假装成功」的取向：
// 客户端不支持预签名时，回退必须**如实报错**，而不是返回错位数据。
func TestSeekUnsupportedClientFailsLoudly(t *testing.T) {
	// fakeClient（spacedav_test.go）不实现 PresignGet
	fc := newFake(drive.Object{Path: "a.txt", Size: 10})
	fs := New(Options{Client: fc, Space: drive.SpacePersonal,
		Token: func() string { return "tok" }, Label: "test"})

	f, err := fs.OpenFile(context.Background(), "a.txt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, err := io.ReadFull(f, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	// 已读过数据，回退需要重开流 → 无预签名能力 → 必须报错
	if _, err := f.Seek(0, io.SeekStart); err == nil {
		t.Error("无预签名能力时回退竟然成功了——可能返回了错位数据")
	}
}
