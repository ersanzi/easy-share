package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"easyshare/internal/config"
	"easyshare/internal/knowledge"
	"easyshare/internal/task"
)

func TestKnowledgeLateContentIsNotDeliveredToNewAccount(t *testing.T) {
	for _, path := range []string{"query", "documents", "source", "export"} {
		t.Run(path, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Disposition", "attachment; filename=old-secret.txt")
				_, _ = w.Write([]byte("{\"answer\":\"old-secret\",\"documents\":[]}"))
			}))
			defer remote.Close()
			defer unblock()
			server := NewServer(config.Config{APIToken: "fixture"}, task.NewStore())
			service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
			server.ConfigureKnowledge(service)
			if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "old-fixture"}); err != nil {
				t.Fatal(err)
			}
			result := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{\"question\":\"fixture\"}"))
				req.SetPathValue("file_id", "f1")
				req.SetPathValue("version_id", "v1")
				req.SetPathValue("meetingID", "m1")
				req.SetPathValue("format", "markdown")
				switch path {
				case "query":
					server.knowledgeQuery(result, req)
				case "documents":
					server.knowledgeDocuments(result, req)
				case "source":
					server.knowledgeDocumentSource(result, req)
				case "export":
					server.knowledgeMeetingExport(result, req)
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("旧请求未到达远端")
			}
			if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "new-fixture"}); err != nil {
				t.Fatal(err)
			}
			unblock()
			<-done
			if result.Code != http.StatusConflict || strings.Contains(result.Body.String(), "old-secret") || strings.Contains(result.Header().Get("Content-Disposition"), "old-secret") {
				t.Fatalf("旧账号内容仍被发送: status=%d", result.Code)
			}
		})
	}
}

// 真实代理请求停在远端边界，确定性重现注销和换人期间的迟到响应。
func TestKnowledgeLateLoginCannotRestoreLoggedOutOrNewSession(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "logout", true: "new login"}[replace], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer old-fixture" {
					close(entered)
					<-release
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"username": "fixture-user", "role": "member"})
			}))
			defer remote.Close()
			defer unblock()
			server := NewServer(config.Config{APIToken: "fixture"}, task.NewStore())
			store := knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json"))
			service := knowledge.NewService(store)
			server.ConfigureKnowledge(service)
			register := func(token string) *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]string{"serverUrl": remote.URL, "token": token})
				req := httptest.NewRequest(http.MethodPost, "/api/knowledge/sso", bytes.NewReader(body))
				res := httptest.NewRecorder()
				server.knowledgeRegisterSSO(res, req)
				return res
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- register("old-fixture") }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("旧登录未到达远端")
			}
			if replace {
				if res := register("new-fixture"); res.Code != http.StatusOK {
					t.Fatalf("new login: %d", res.Code)
				}
			} else {
				server.knowledgeLogout(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
			}
			unblock()
			res := <-done
			if res.Code == http.StatusOK {
				t.Fatal("旧登录响应被接受")
			}
			expected := ""
			if replace {
				expected = "new-fixture"
			}
			if service.Current().Token != expected {
				t.Fatal("旧登录恢复或覆盖了当前会话")
			}
			restored := knowledge.NewService(store)
			if restored.Current().Token != expected {
				t.Fatal("磁盘会话与当前身份不一致")
			}
		})
	}
}

func TestKnowledgeRejectedAccountSwitchClearsOldSession(t *testing.T) {
	core, remote, service := newKnowledgeTestServer(t)
	if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "cp-token-1", Mode: knowledge.ModeSSO}); err != nil {
		t.Fatal(err)
	}
	status, _ := knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/sso", "secret",
		map[string]string{"serverUrl": remote.URL, "token": "rejected-fixture"})
	if status == http.StatusOK {
		t.Fatal("错误身份被接受")
	}
	if service.Current().Token != "" {
		t.Fatal("账号切换失败仍使用旧账号知识会话")
	}
}

func TestKnowledgeLateUnauthorizedDoesNotClearNewSession(t *testing.T) {
	for _, path := range []string{"query", "documents", "same-token"} {
		t.Run(path, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				http.Error(w, "expired fixture", http.StatusUnauthorized)
			}))
			defer remote.Close()
			defer unblock()
			server := NewServer(config.Config{APIToken: "fixture"}, task.NewStore())
			service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
			server.ConfigureKnowledge(service)
			if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "old-fixture"}); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{\"question\":\"fixture\"}"))
				if path == "query" {
					server.knowledgeQuery(httptest.NewRecorder(), req)
				} else {
					server.knowledgeDocuments(httptest.NewRecorder(), req)
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("旧请求未到达远端")
			}
			nextToken := "new-fixture"
			if path == "same-token" {
				nextToken = "old-fixture"
			}
			if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: nextToken}); err != nil {
				t.Fatal(err)
			}
			unblock()
			<-done
			if service.Current().Token != nextToken {
				t.Fatal("旧请求的401删除了新账号会话")
			}
		})
	}
}
