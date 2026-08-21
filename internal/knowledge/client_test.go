package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeKnowledgeServer 模拟知识服务（FastAPI）的登录与问答端点。
func fakeKnowledgeServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/login", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body.Username != "alice" || body.Password != "secret" {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(writer).Encode(map[string]string{"detail": "用户名或口令错误"})
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"token": token, "username": "alice", "role": "member", "expires_at": "2026-12-31T00:00:00Z",
		})
	})
	mux.HandleFunc("POST /query", func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(writer).Encode(map[string]string{"detail": "未登录或令牌无效"})
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"answer": "每晚四百元",
			"contexts": []map[string]any{
				{"doc_id": "d1", "filename": "差旅制度.pdf", "score": 0.92, "text": "住宿标准每晚400元"},
			},
		})
	})
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"records": 12, "llm": "configured", "watch_dirs": 1})
	})
	return httptest.NewServer(mux)
}

func TestNormalizeAndValidateServerURL(t *testing.T) {
	if got := NormalizeServerURL(" 192.168.1.10:8000/ "); got != "http://192.168.1.10:8000" {
		t.Fatalf("normalize = %q", got)
	}
	for _, invalid := range []string{"", "ftp://x", "http://", "not a url"} {
		if ValidServerURL(invalid) {
			t.Fatalf("ValidServerURL(%q) = true", invalid)
		}
	}
	if !ValidServerURL("http://10.0.0.5:8000") || !ValidServerURL("https://kb.corp.local") {
		t.Fatal("valid URLs rejected")
	}
}

func TestClientLoginAndQuery(t *testing.T) {
	remote := fakeKnowledgeServer(t, "tok-1")
	defer remote.Close()

	client := NewClient(remote.URL)
	session, err := client.Login(context.Background(), "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if session.Token != "tok-1" || session.Username != "alice" || session.Role != "member" {
		t.Fatalf("session = %+v", session)
	}

	answer, err := client.Query(context.Background(), "tok-1", "出差住宿标准")
	if err != nil {
		t.Fatal(err)
	}
	if answer.Answer != "每晚四百元" || len(answer.Contexts) != 1 || answer.Contexts[0].Text == "" {
		t.Fatalf("answer = %+v", answer)
	}
}

func TestClientErrorMapping(t *testing.T) {
	remote := fakeKnowledgeServer(t, "tok-1")
	defer remote.Close()
	client := NewClient(remote.URL)

	// 错误凭据 → 401 RemoteError，detail 透传
	_, err := client.Login(context.Background(), "alice", "wrong")
	remoteErr, ok := err.(*RemoteError)
	if !ok || remoteErr.Status != http.StatusUnauthorized || remoteErr.Detail != "用户名或口令错误" {
		t.Fatalf("login error = %#v", err)
	}

	// 令牌失效 → query 401
	_, err = client.Query(context.Background(), "bad", "问题")
	if remoteErr, ok := err.(*RemoteError); !ok || remoteErr.Status != http.StatusUnauthorized {
		t.Fatalf("query error = %#v", err)
	}

	// 服务不可达 → 普通 error（非 RemoteError）
	unreachable := NewClient("http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := unreachable.Login(ctx, "a", "b"); err == nil || strings.Contains(err.Error(), "RemoteError") {
		t.Fatalf("unreachable login err = %v", err)
	}
}

func TestClientHealth(t *testing.T) {
	remote := fakeKnowledgeServer(t, "tok-1")
	defer remote.Close()
	health, err := NewClient(remote.URL).HealthWithTimeout(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if health.Records != 12 || health.LLM != "configured" || health.WatchDirs != 1 {
		t.Fatalf("health = %+v", health)
	}
}
