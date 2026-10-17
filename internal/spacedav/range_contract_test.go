package spacedav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"easyshare/internal/drive"

	"golang.org/x/net/webdav"
)

// rangeAwareClient 是支持预签名的假客户端：Open/PresignGet 都指向一个 httptest 服务器，
// 从而让「区间读」这条路径可以被真实执行（而不是只 mock 掉）。
type rangeAwareClient struct {
	payload []byte
	// objects 可覆写列举结果；为 nil 时用默认的单文件。
	objects []drive.Object
	srv     *httptest.Server
	// rangeHits 记录服务端收到的 Range 头，用来审计是否退化成整份请求
	rangeHits []string
	// uploads 记录经控制面上传的 path→大小（PUT 路径的验证点）
	uploads map[string]int64
}

func newRangeAware(payload []byte) *rangeAwareClient {
	c := &rangeAwareClient{payload: payload}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rh := r.Header.Get("Range"); rh != "" {
			c.rangeHits = append(c.rangeHits, rh)
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(c.payload))
	}))
	return c
}

func (c *rangeAwareClient) Close() { c.srv.Close() }

func (c *rangeAwareClient) Objects(context.Context, string, string) ([]drive.Object, error) {
	if c.objects != nil {
		return c.objects, nil
	}
	return []drive.Object{{Path: "big.bin", Size: int64(len(c.payload))}}, nil
}

func (c *rangeAwareClient) Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error) {
	resp, err := http.Get(c.srv.URL)
	if err != nil {
		return nil, 0, err
	}
	return resp.Body, int64(len(c.payload)), nil
}

func (c *rangeAwareClient) PresignGet(ctx context.Context, token, space, path string) (string, error) {
	return c.srv.URL, nil
}

func (c *rangeAwareClient) Upload(_ context.Context, _, _, path string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if c.uploads == nil {
		c.uploads = map[string]int64{}
	}
	c.uploads[path] = int64(len(data))
	if size >= 0 && int64(len(data)) != size {
		return fmt.Errorf("申报大小 %d 与实际 %d 不符", size, len(data))
	}
	return nil
}
func (c *rangeAwareClient) Delete(context.Context, string, string, string) error {
	return fmt.Errorf("只读测试不该删除")
}

// TestWebDAVRangeBehaviorContract 记录并守卫**当前**的 WebDAV 区间读行为。
//
// 背景（2026-10-16 实测）：`internal/spacedav` 的 `readFile.Seek` 只支持
// 「原地查询位置」与「SeekEnd(0)」，因此 `http.ServeContent` 在响应 HTTP Range
// 请求时会失败——**基线对任何 Range 请求都返回 500**。
//
// 本用例**不要求** Range 必须可用（那是未来改进的目标），它守的是两件当前必须成立的事：
//
//  1. **不静默丢数据**：无论走 500 还是 206，都不能出现「状态码 2xx 但内容残缺」——
//     后者是比 500 危险得多的失败模式（用户拿到半个文件却以为成功）。
//  2. **行为可被观测**：把当前实际状态码记录下来，作为将来改进的基线。
//
// 一旦有人实现了真正的区间读，本用例的断言 ① 仍然成立（206 + 精确区间），
// 而下面的"当前行为"记录需要更新——那正是它的作用：**逼后来者显式面对这次变更**。
func TestWebDAVRangeBehaviorContract(t *testing.T) {
	payload := make([]byte, 64<<10) // 64 KiB：足够触发真实的分块读取路径
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	fc := newRangeAware(payload)
	defer fc.Close()

	fs := New(Options{Client: fc, Space: drive.SpacePersonal,
		Token: func() string { return "tok" }, Label: "test"})
	h := &webdav.Handler{Prefix: "/", FileSystem: fs, LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// ① 无 Range：整份读必须**完整**（这是最要紧的守卫——它防的是静默截断）
	t.Run("整份读完整", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/big.bin")
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
		}
		if len(got) != len(payload) {
			t.Fatalf("读到 %d 字节，期望 %d —— **静默丢数据**，比报错严重得多", len(got), len(payload))
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("整份读内容不符")
		}
	})

	// ② Range 请求：**只守"不得静默丢数据"**，不要求必须支持
	t.Run("区间读不静默丢数据", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/big.bin", nil)
		req.Header.Set("Range", "bytes=0-9")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)

		switch resp.StatusCode {
		case http.StatusPartialContent:
			// 若将来实现了区间读，内容必须是请求的那一段
			if len(got) != 10 {
				t.Errorf("206 但长度为 %d，期望 10 —— 静默错数据", len(got))
			}
			if !bytes.Equal(got, payload[:10]) {
				t.Error("206 但内容不是请求区间")
			}
			t.Logf("已支持区间读；对象存储请求=%v", fc.rangeHits)
		case http.StatusInternalServerError, http.StatusRequestedRangeNotSatisfiable:
			// 当前基线的实际行为：不支持随机读，干净地报错（可接受）
			t.Logf("当前不支持区间读，状态码 %d（干净失败，未返回残缺数据）", resp.StatusCode)
		default:
			// 2xx 却没给对内容 = 静默错数据，绝不允许
			t.Fatalf("状态码 %d，长度 %d —— 非预期且可能静默错数据", resp.StatusCode, len(got))
		}
	})
}
