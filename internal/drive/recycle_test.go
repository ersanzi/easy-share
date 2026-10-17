package drive

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// 回收站列表：走版本化端点并按 envelope 解包。
func TestRecycleListUnwrapsEntries(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/drive/recycle" {
			t.Errorf("路径错误：%s", r.URL.Path)
		}
		if got := r.URL.Query().Get("space"); got != SpaceShared {
			t.Errorf("空间参数错误：%q", got)
		}
		w.Write([]byte(`{"code":200,"data":[
			{"fileId":7,"path":"docs/x.pdf","originPath":"docs/x.pdf","size":12,"versionId":2,"deletedAt":"2026-10-14T01:00:00Z"}]}`))
	}))
	defer closeFn()

	entries, err := client.RecycleList(context.Background(), "tok", SpaceShared)
	if err != nil {
		t.Fatalf("RecycleList 出错：%v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(entries))
	}
	entry := entries[0]
	if entry.FileId != 7 || entry.OriginPath != "docs/x.pdf" || entry.VersionId != 2 {
		t.Errorf("条目解析错误：%+v", entry)
	}
	if entry.DeletedAt == nil {
		t.Error("删除时间未解析")
	}
}

// 还原/彻底删除：请求体钉住 fileId 与 space；冲突 message 原样透出。
func TestRecycleRestorePostsFileID(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/drive/recycle/restore" || r.Method != http.MethodPost {
			t.Errorf("路由错误：%s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"fileId":7`) || !strings.Contains(string(body), `"space":"personal"`) {
			t.Errorf("请求体错误：%s", body)
		}
		w.Write([]byte(`{"code":200,"data":null}`))
	}))
	defer closeFn()

	if err := client.RecycleRestore(context.Background(), "tok", SpacePersonal, 7); err != nil {
		t.Fatalf("RecycleRestore 出错：%v", err)
	}
}

func TestRecycleRestoreSurfacesConflictMessage(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":500,"msg":"原位置已有同名文件，请先处理现有文件后再还原","data":null}`))
	}))
	defer closeFn()

	err := client.RecycleRestore(context.Background(), "tok", SpacePersonal, 7)
	if err == nil || !strings.Contains(err.Error(), "原位置已有同名文件") {
		t.Fatalf("冲突错误未透出：%v", err)
	}
	if !strings.Contains(err.Error(), "还原回收站文件失败") {
		t.Errorf("缺少 wrap 上下文：%v", err)
	}
}

func TestRecyclePurgePostsFileID(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/drive/recycle/purge" || r.Method != http.MethodPost {
			t.Errorf("路由错误：%s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"fileId":9`) {
			t.Errorf("请求体错误：%s", body)
		}
		w.Write([]byte(`{"code":200,"data":null}`))
	}))
	defer closeFn()

	if err := client.RecyclePurge(context.Background(), "tok", SpacePersonal, 9); err != nil {
		t.Fatalf("RecyclePurge 出错：%v", err)
	}
}
