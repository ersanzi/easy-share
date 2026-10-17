package httprange

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// zeroTime 传给 http.ServeContent；测试不需要 If-Modified-Since 语义。
var zeroTime = time.Time{}

// TestRangeFetcherPartialContent 验证服务端**支持 Range**（204/206）时的行为：
// 只取请求区间，且不会把整份对象读下来。
func TestRangeFetcherPartialContent(t *testing.T) {
	payload := []byte("0123456789abcdefghij") // 20 字节
	var servedBytes int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言客户端确实发了 Range
		rh := r.Header.Get("Range")
		if rh == "" {
			t.Errorf("服务端没收到 Range 头")
			http.ServeContent(w, r, "", zeroTime, bytes.NewReader(payload))
			return
		}
		// 手动实现 206 以精确控制（ServeContent 也支持，但这里想显式断言解析）
		var start, end int
		if _, err := fmt.Sscanf(rh, "bytes=%d-%d", &start, &end); err != nil {
			t.Fatalf("Range 头格式异常: %q", rh)
		}
		if start < 0 || end >= len(payload) || start > end {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		chunk := payload[start : end+1]
		servedBytes += len(chunk)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(chunk)
	}))
	defer srv.Close()

	rf := NewFetcher(func(ctx context.Context) (string, error) { return srv.URL, nil })

	rc, err := rf.Fetch(context.Background(), 5, 4)
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()

	if string(got) != "5678" {
		t.Errorf("内容 = %q，期望 %q", got, "5678")
	}
	if servedBytes != 4 {
		t.Errorf("服务端实际发送 %d 字节，期望 4（说明真的只取了区间）", servedBytes)
	}
}

// TestRangeFetcherFallbackOn200 验证**服务端忽略 Range**（返回 200 + 整份）时，
// 仍然返回正确的区间数据。
//
// 这条是「正确性不依赖服务端支持 Range」的保证——本机无法对真实 RustFS 验证，
// 所以用 httptest 明确覆盖这条回退路径。
func TestRangeFetcherFallbackOn200(t *testing.T) {
	payload := []byte("0123456789abcdefghij")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 刻意忽略 Range，永远返回整份 200
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))
	defer srv.Close()

	rf := NewFetcher(func(ctx context.Context) (string, error) { return srv.URL, nil })

	rc, err := rf.Fetch(context.Background(), 5, 4)
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "5678" {
		t.Errorf("回退路径内容 = %q，期望 %q", got, "5678")
	}
}

// TestRangeFetcherOutOfRange 覆盖区间越界：应当是**可读的错误**，不是 panic。
func TestRangeFetcherOutOfRange(t *testing.T) {
	payload := []byte("short")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", zeroTime, bytes.NewReader(payload))
	}))
	defer srv.Close()

	rf := NewFetcher(func(ctx context.Context) (string, error) { return srv.URL, nil })
	_, err := rf.Fetch(context.Background(), 100, 10)
	if err == nil {
		t.Fatal("越界区间竟然成功了")
	}
	if !strings.Contains(err.Error(), "越界") && !strings.Contains(err.Error(), "416") {
		t.Logf("越界错误信息（可接受）: %v", err)
	}
}

// TestRangeFetcherShortBody 覆盖「206 但 body 短于声称长度」：
// 必须报错，而不是让上层读到静默截断的数据。
func TestRangeFetcherShortBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-9/100")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("only4")) // 声称 10 字节，实际 5
	}))
	defer srv.Close()

	rf := NewFetcher(func(ctx context.Context) (string, error) { return srv.URL, nil })
	rc, err := rf.Fetch(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("Fetch 本身不应失败: %v", err)
	}
	b, rerr := io.ReadAll(rc)
	rc.Close()
	if rerr == nil {
		t.Fatalf("短 body 竟然读到 EOF 无错（拿到 %d 字节）——会变成静默截断", len(b))
	}
	t.Logf("短 body 被如实报错: %v（已读 %d 字节）", rerr, len(b))
}

// TestParseContentRangeTotal 覆盖 Content-Range 解析（纯函数）。
func TestParseContentRangeTotal(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
		ok   bool
	}{
		{"bytes 0-1023/2048", 2048, true},
		{"bytes 5-9/10", 10, true},
		{"bytes 0-0/*", 0, false},
		{"", 0, false},
		{"garbage", 0, false},
	} {
		got, ok := ParseContentRangeTotal(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseContentRangeTotal(%q) = (%d,%v)，期望 (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
