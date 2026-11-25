package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"easyshare/internal/config"
	"easyshare/internal/task"
)

func taskReviewServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner := map[string]string{"Bearer alice": "71", "Bearer bob": "72"}[r.Header.Get("Authorization")]
		if owner == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"userId": owner, "drive": true}})
	}))
	t.Cleanup(authority.Close)
	server := NewServer(config.Config{APIToken: "ipc-secret", PlatformBaseURL: authority.URL}, task.NewStore())
	core := httptest.NewServer(server.httpServer.Handler)
	t.Cleanup(core.Close)
	return server, core
}

func taskReviewRequest(t *testing.T, core *httptest.Server, accountToken, method, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer ipc-secret")
	req.Header.Set("X-EasyShare-Account-Token", accountToken)
	result := httptest.NewRecorder()
	core.Config.Handler.ServeHTTP(result, req)
	return result
}

func TestTaskAccountBoundaryRejectsOtherAccountReadMutationAndPaths(t *testing.T) {
	server, core := taskReviewServer(t)
	result := taskReviewRequest(t, core, "alice", "POST", "/api/tasks", map[string]any{
		"kind": "cloud_download", "status": "running", "fileName": "alice-secret.pdf", "localPath": "C:/private/alice-secret.pdf",
	})
	if result.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", result.Code, result.Body.String())
	}
	var created task.Task
	if err := json.Unmarshal(result.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"bob", ""} {
		response := taskReviewRequest(t, core, token, "GET", "/api/tasks", nil)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "alice-secret") {
			t.Errorf("任务跨账号泄露: token=%q status=%d body=%s", token, response.Code, response.Body.String())
		}
		for _, method := range []string{"PATCH", "DELETE"} {
			response = taskReviewRequest(t, core, token, method, "/api/tasks/"+created.ID, map[string]any{"status": "failed"})
			if response.Code != http.StatusNotFound {
				t.Errorf("跨账号任务修改: method=%s token=%q status=%d", method, token, response.Code)
			}
		}
	}
	if _, found := server.tasks.Get(created.ID); !found {
		t.Fatal("其他账号删除了任务")
	}
	response := taskReviewRequest(t, core, "alice", "GET", "/api/tasks", nil)
	if !strings.Contains(response.Body.String(), "alice-secret") {
		t.Fatal("原账号不能恢复自己的历史")
	}
}

func TestTaskOwnerIsolationIncludesHistoryPathAndRetry(t *testing.T) {
	server, core := taskReviewServer(t)
	server.tasks.EnablePersistence(filepath.Join(t.TempDir(), "tasks.json"))
	var created task.Task
	response := taskReviewRequest(t, core, "alice", "POST", "/api/tasks", map[string]any{
		"kind": "cloud_download", "fileName": "private.pdf", "localPath": "C:/private/private.pdf", "status": "failed",
	})
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.OwnerID == "" {
		t.Fatal("云任务未绑定稳定身份")
	}
	// 状态重排不是执行器，但也必须受账号归属保护。
	if r := taskReviewRequest(t, core, "bob", "PATCH", "/api/tasks/"+created.ID, map[string]string{"status": "queued"}); r.Code != 404 {
		t.Fatalf("跨账号重试状态修改: %d", r.Code)
	}
	if r := taskReviewRequest(t, core, "bob", "POST", "/api/tasks/clear", nil); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if _, ok := server.tasks.Get(created.ID); !ok {
		t.Fatal("别人清除了任务")
	}
	for _, token := range []string{"bob", ""} {
		if r := taskReviewRequest(t, core, token, "POST", "/api/tasks/check-path", map[string]string{"path": created.LocalPath}); r.Code != 403 {
			t.Fatalf("旧路径放行: token=%q code=%d", token, r.Code)
		}
	}
	if r := taskReviewRequest(t, core, "alice", "POST", "/api/tasks/check-path", map[string]string{"path": created.LocalPath}); r.Code != 200 {
		t.Fatal("原账号路径被阻断")
	}
	if r := taskReviewRequest(t, core, "alice", "POST", "/api/tasks/clear", nil); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if r := taskReviewRequest(t, core, "alice", "GET", "/api/tasks", nil); strings.Contains(r.Body.String(), created.ID) {
		t.Fatal("清历史没有隐藏任务")
	}
	if r := taskReviewRequest(t, core, "bob", "POST", "/api/tasks/check-path", map[string]string{"path": created.LocalPath}); r.Code != 403 {
		t.Fatal("清历史解除路径保护")
	}
}

func TestTaskEventsAreFilteredAndRevalidated(t *testing.T) {
	server, _ := taskReviewServer(t)
	alice, err := server.taskOwner(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := server.taskOwner(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Type: "transfer.updated", Data: task.Task{OwnerID: alice, Kind: task.KindCloudDownload, FileName: "private.pdf"}}
	for _, test := range []struct {
		token, owner string
		want         bool
		err          bool
	}{
		{"alice", alice, true, false}, {"bob", bob, false, false}, {"", "", false, false}, {"invalid", alice, false, false},
	} {
		current, err := server.taskOwner(context.Background(), test.token)
		allowed := err == nil && event.Data.(task.Task).VisibleTo(current)
		if err != nil {
			err = nil
		} // 无法授权的批次不交付云事件。
		if allowed != test.want || (err != nil) != test.err {
			t.Fatalf("token=%s allowed=%v err=%v", test.token, allowed, err)
		}
	}
	// 无归属历史云任务在任何账号下都不能归为本人；LAN 保持设备级。
	for _, owner := range []string{"", alice, bob} {
		if (task.Task{Kind: task.KindCloudDownload}).VisibleTo(owner) {
			t.Fatal("旧云任务被认领")
		}
		if !(task.Task{Kind: task.KindLANReceive}).VisibleTo(owner) {
			t.Fatal("LAN任务被阻断")
		}
	}
}

func TestTaskAuthorityFailureKeepsOnlyDeviceTasksAvailable(t *testing.T) {
	server, core := taskReviewServer(t)
	lan, err := server.tasks.Create(task.Task{Kind: task.KindLANReceive, FileName: "local-lan.txt", Status: task.StatusFailed})
	if err != nil {
		t.Fatal(err)
	}
	response := taskReviewRequest(t, core, "invalid", "GET", "/api/tasks", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), lan.ID) || response.Header().Get("X-EasyShare-Task-Scope") != "device" {
		t.Fatalf("账号服务失效阻断LAN: code=%d scope=%s body=%s", response.Code, response.Header().Get("X-EasyShare-Task-Scope"), response.Body.String())
	}
	if r := taskReviewRequest(t, core, "invalid", "DELETE", "/api/tasks/"+lan.ID, nil); r.Code != 200 {
		t.Fatalf("LAN删除被账号服务阻断: %d", r.Code)
	}
	for _, token := range []string{"", "invalid"} {
		r := taskReviewRequest(t, core, token, "POST", "/api/tasks", map[string]any{"kind": "cloud_download", "fileName": "denied.txt"})
		if r.Code != 401 && r.Code != 403 {
			t.Fatalf("无身份云任务被创建: %d", r.Code)
		}
	}
}
