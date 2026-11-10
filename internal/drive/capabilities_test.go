package drive

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// 能力只能由专用接口显式声明，不能从对象列表或空响应推断。
func TestCapabilitiesUnwrapsEnvelope(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("请求方法错误：%s", r.Method)
		}
		if r.URL.RequestURI() != "/easyshare/drive/capabilities" {
			t.Errorf("能力接口路径错误：%s", r.URL.RequestURI())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer capability-token" {
			t.Errorf("鉴权头错误：%q", got)
		}
		if got := r.Header.Get("clientid"); got != pcClientID {
			t.Errorf("clientid 头错误：%q", got)
		}
		w.Write([]byte(`{"code":200,"msg":"ok","data":{"persistentDirectories":true,"protocolVersion":1}}`))
	}))
	defer closeFn()

	got, err := client.Capabilities(context.Background(), "capability-token")
	if err != nil {
		t.Fatalf("Capabilities 出错：%v", err)
	}
	if want := (Capabilities{PersistentDirectories: true, ProtocolVersion: 1}); got != want {
		t.Fatalf("能力解析错误：got %+v, want %+v", got, want)
	}
}

func TestCapabilitiesErrorsDoNotAdvertiseSupport(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"旧服务端404", http.StatusNotFound, "404 page not found", "404"},
		{"旧服务端JSON404", http.StatusNotFound, `{"code":404,"msg":"接口不存在"}`, "接口不存在"},
		{"业务错误", http.StatusOK, `{"code":500,"msg":"能力查询失败","data":null}`, "能力查询失败"},
		{"未登录", http.StatusOK, `{"code":401,"msg":"请重新登录","data":null}`, "请重新登录"},
		{"服务端错误", http.StatusInternalServerError, "internal server error", "500"},
		{"空对象列表", http.StatusOK, `{"code":200,"data":[]}`, "解析云盘数据失败"},
		{"字段类型错误", http.StatusOK, `{"code":200,"data":{"persistentDirectories":"true"}}`, "解析云盘数据失败"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer closeFn()

			got, err := client.Capabilities(context.Background(), "tok")
			if err == nil {
				t.Fatal("无有效能力声明时应报错")
			}
			if !strings.Contains(err.Error(), "读取云盘协议能力失败") || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("错误缺少上下文或原始原因：%v", err)
			}
			if got.PersistentDirectories {
				t.Errorf("失败响应不能声明持久化目录支持：%+v", got)
			}
		})
	}
}

// 空对象、空数据与显式关闭能力均不能被当作持久化目录支持。
func TestCapabilitiesEmptyDataDoesNotAdvertiseSupport(t *testing.T) {
	for _, body := range []string{
		`{"code":200,"data":{}}`,
		`{"code":200,"data":null}`,
		`{"code":200}`,
		`{"code":200,"data":{"persistentDirectories":false,"protocolVersion":1}}`,
	} {
		t.Run(body, func(t *testing.T) {
			client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			}))
			defer closeFn()

			got, err := client.Capabilities(context.Background(), "tok")
			if err != nil {
				t.Fatalf("空能力或关闭能力应解析为不支持：%v", err)
			}
			if got.PersistentDirectories {
				t.Errorf("不能从空数据或显式 false 推断目录支持：%+v", got)
			}
		})
	}
}

func TestCapabilitiesPreservesContextCancellation(t *testing.T) {
	client := New("http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := client.Capabilities(ctx, "tok")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消原因应通过错误包装保留：%v", err)
	}
	if got != (Capabilities{}) {
		t.Errorf("取消请求不应返回能力：%+v", got)
	}
}
