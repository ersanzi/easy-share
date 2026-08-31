package drive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient 建一个指向测试服务器的客户端。
func newTestClient(handler http.Handler) (*Client, func()) {
	server := httptest.NewServer(handler)
	client := New(server.URL)
	return client, server.Close
}

func TestObjectsUnwrapsEnvelope(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/drive/objects" {
			t.Errorf("路径错误：%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("鉴权头错误：%q", got)
		}
		if got := r.Header.Get("clientid"); got != pcClientID {
			t.Errorf("clientid 头错误：%q", got)
		}
		w.Write([]byte(`{"code":200,"msg":"ok","data":[
			{"path":"a.txt","size":3,"lastModified":"2026-08-29T11:00:00Z"},
			{"path":"photos/b.jpg","size":9,"lastModified":"2026-08-29T12:00:00Z"}]}`))
	}))
	defer closeFn()

	objects, err := client.Objects(context.Background(), "tok", SpacePersonal)
	if err != nil {
		t.Fatalf("Objects 出错：%v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("期望 2 个对象，实际 %d", len(objects))
	}
	if objects[1].Path != "photos/b.jpg" || objects[1].Size != 9 {
		t.Errorf("对象解析错误：%+v", objects[1])
	}
	if objects[0].LastModified.IsZero() {
		t.Error("时间未解析")
	}
}

func TestPresignReturnsURL(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"path":"dir/a.txt"`) {
			t.Errorf("请求体未含路径：%s", body)
		}
		// 配额判定依赖申报的 size：漏传等于把校验废掉，故一并钉住
		if !strings.Contains(string(body), `"size":9`) {
			t.Errorf("请求体未含申报大小：%s", body)
		}
		if !strings.Contains(string(body), `"space":"personal"`) {
			t.Errorf("请求体未含空间：%s", body)
		}
		w.Write([]byte(`{"code":200,"data":{"url":"http://store/put?sig=x","path":"dir/a.txt"}}`))
	}))
	defer closeFn()

	url, err := client.PresignPut(context.Background(), "tok", SpacePersonal, "dir/a.txt", 9)
	if err != nil {
		t.Fatalf("PresignPut 出错：%v", err)
	}
	if url != "http://store/put?sig=x" {
		t.Errorf("URL 错误：%s", url)
	}
}

// RuoYi 把业务错误放在 HTTP 200 的 body 里，客户端必须按 code 判失败并透出 msg。
func TestBusinessErrorSurfacesMessage(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":500,"msg":"文件路径包含非法片段","data":null}`))
	}))
	defer closeFn()

	_, err := client.PresignGet(context.Background(), "tok", SpacePersonal, "../escape.txt")
	if err == nil {
		t.Fatal("越权路径应当报错")
	}
	if !strings.Contains(err.Error(), "非法片段") {
		t.Errorf("未透出控制面消息：%v", err)
	}
}

func TestUnauthorizedSurfacesError(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":401,"msg":"登录状态异常，请重新登录","data":null}`))
	}))
	defer closeFn()

	if _, err := client.Objects(context.Background(), "", SpacePersonal); err == nil {
		t.Fatal("未登录应当报错")
	}
}

// Upload 必须先换预签名 URL，再把内容 PUT 到该 URL。
func TestUploadPresignsThenPuts(t *testing.T) {
	var putBody string
	var putPath string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("对象存储应收到 PUT，实际 %s", r.Method)
		}
		putPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		putBody = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer store.Close()

	presigned := 0
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presigned++
		if r.URL.Path != "/easyshare/drive/presign-put" {
			t.Errorf("应调预签名上传接口，实际 %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":200,"data":{"url":"` + store.URL + `/easyshare/users/1/a.txt","path":"a.txt"}}`))
	}))
	defer closeFn()

	content := "hello"
	if err := client.Upload(context.Background(), "tok", SpacePersonal, "a.txt", strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("Upload 出错：%v", err)
	}
	if presigned != 1 {
		t.Errorf("应换取 1 次预签名，实际 %d", presigned)
	}
	if putBody != content {
		t.Errorf("上传内容错误：%q", putBody)
	}
	if putPath != "/easyshare/users/1/a.txt" {
		t.Errorf("上传目标键错误：%s", putPath)
	}
}

// 预签名不签 Content-Type，客户端不得自行设置，否则签名不匹配。
func TestUploadOmitsContentType(t *testing.T) {
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("不应设置 Content-Type，实际 %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer store.Close()

	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":200,"data":{"url":"` + store.URL + `/k","path":"a.txt"}}`))
	}))
	defer closeFn()

	if err := client.Upload(context.Background(), "tok", SpacePersonal, "a.txt", strings.NewReader("x"), 1); err != nil {
		t.Fatalf("Upload 出错：%v", err)
	}
}

// 对象存储返回非 2xx 时必须报错，不能静默成功。
func TestUploadFailsOnStoreError(t *testing.T) {
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer store.Close()

	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":200,"data":{"url":"` + store.URL + `/k","path":"a.txt"}}`))
	}))
	defer closeFn()

	err := client.Upload(context.Background(), "tok", SpacePersonal, "a.txt", strings.NewReader("x"), 1)
	if err == nil {
		t.Fatal("对象存储 403 时应报错")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("错误未含状态码：%v", err)
	}
}

func TestOpenReadsContent(t *testing.T) {
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("file-content"))
	}))
	defer store.Close()

	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/drive/presign-get" {
			t.Errorf("应调预签名下载接口，实际 %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":200,"data":{"url":"` + store.URL + `/k","path":"a.txt"}}`))
	}))
	defer closeFn()

	body, _, err := client.Open(context.Background(), "tok", SpacePersonal, "a.txt")
	if err != nil {
		t.Fatalf("Open 出错：%v", err)
	}
	defer body.Close()
	raw, _ := io.ReadAll(body)
	if string(raw) != "file-content" {
		t.Errorf("内容错误：%q", raw)
	}
}

func TestDeleteSendsPath(t *testing.T) {
	var method, gotBody string
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Write([]byte(`{"code":200,"msg":"ok","data":null}`))
	}))
	defer closeFn()

	if err := client.Delete(context.Background(), "tok", SpacePersonal, "a.txt"); err != nil {
		t.Fatalf("Delete 出错：%v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("应为 DELETE，实际 %s", method)
	}
	if !strings.Contains(gotBody, `"path":"a.txt"`) {
		t.Errorf("请求体错误：%s", gotBody)
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	if got := New("http://host:8090/").baseURL; got != "http://host:8090" {
		t.Errorf("baseURL 未去尾斜杠：%s", got)
	}
}
