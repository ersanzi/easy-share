package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"easyshare/internal/config"
	"easyshare/internal/knowledge"
	"easyshare/internal/task"
)

// newKnowledgeTestServer 起一个带知识网关的 Core API 测试服务，返回其 httptest 服务与远端模拟服务。
func newKnowledgeTestServer(t *testing.T) (*httptest.Server, *httptest.Server, *knowledge.Service) {
	t.Helper()
	remote := knowledgeTestRemote(t)
	server := NewServer(config.Config{APIToken: "secret"}, task.NewStore())
	service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
	server.ConfigureKnowledge(service)
	core := httptest.NewServer(server.httpServer.Handler)
	t.Cleanup(core.Close)
	t.Cleanup(remote.Close)
	return core, remote, service
}

// knowledgeTestRemote 模拟知识服务（FastAPI）：alice/secret 可登录，令牌 tok-1。
func knowledgeTestRemote(t *testing.T) *httptest.Server {
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
		_ = json.NewEncoder(writer).Encode(map[string]any{"token": "tok-1", "username": "alice", "role": "member", "expires_at": "2026-12-31T00:00:00Z"})
	})
	mux.HandleFunc("POST /query", func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer tok-1" {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(writer).Encode(map[string]string{"detail": "未登录或令牌无效"})
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"answer": "每晚四百元",
			"sources": []map[string]any{{"doc_id": "d1", "score": 0.9}},
			"contexts": []map[string]any{
				{"doc_id": "d1", "filename": "差旅制度.pdf", "score": 0.92, "ingested_at": "2026-08-01", "text": "住宿标准每晚400元"},
			},
		})
	})
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"records": 3, "llm": "configured", "watch_dirs": 1})
	})
	return httptest.NewServer(mux)
}

func knowledgeRequest(t *testing.T, method, url, token string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(response.Body).Decode(&payload)
	return response.StatusCode, payload
}

func TestKnowledgeGatewayFlow(t *testing.T) {
	core, remote, service := newKnowledgeTestServer(t)

	// 初始：未配置未登录
	code, payload := knowledgeRequest(t, http.MethodGet, core.URL+"/api/knowledge/status", "secret", nil)
	if code != http.StatusOK || payload["configured"] != false || payload["loggedIn"] != false {
		t.Fatalf("initial status = %d %v", code, payload)
	}

	// 未登录提问 → 401 knowledge_not_logged_in
	code, payload = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/query", "secret", map[string]string{"question": "出差住宿标准"})
	if code != http.StatusUnauthorized || payload["code"] != "knowledge_not_logged_in" {
		t.Fatalf("unauthenticated query = %d %v", code, payload)
	}

	// 错误密码 → 401 凭据错误（远端 detail 不直接透出，用户可读）
	code, payload = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/login", "secret", map[string]string{"serverUrl": remote.URL, "username": "alice", "password": "wrong"})
	if code != http.StatusUnauthorized || payload["code"] != "knowledge_invalid_credentials" {
		t.Fatalf("bad login = %d %v", code, payload)
	}

	// 登录成功 → 状态变已登录
	code, payload = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/login", "secret", map[string]string{"serverUrl": remote.URL, "username": "alice", "password": "secret"})
	if code != http.StatusOK || payload["loggedIn"] != true || payload["username"] != "alice" {
		t.Fatalf("login = %d %v", code, payload)
	}

	// 问答代理：答案 + 引用片段透传
	code, payload = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/query", "secret", map[string]string{"question": "出差住宿标准"})
	if code != http.StatusOK || payload["answer"] != "每晚四百元" {
		t.Fatalf("query = %d %v", code, payload)
	}
	contexts, ok := payload["contexts"].([]any)
	if !ok || len(contexts) != 1 {
		t.Fatalf("contexts = %v", payload["contexts"])
	}

	// 健康探测
	code, payload = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/health", "secret", nil)
	if code != http.StatusOK || payload["records"] != float64(3) || payload["llm"] != "configured" {
		t.Fatalf("health = %d %v", code, payload)
	}

	// 退出 → 会话清空且落盘文件删除
	code, _ = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/logout", "secret", nil)
	if code != http.StatusOK {
		t.Fatalf("logout = %d", code)
	}
	if status := service.Status(); status.LoggedIn {
		t.Fatalf("status after logout = %+v", status)
	}
}

func TestKnowledgeGatewayValidation(t *testing.T) {
	core, _, _ := newKnowledgeTestServer(t)

	// 非法服务器地址 → 400
	code, payload := knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/login", "secret", map[string]string{"serverUrl": "ftp://x", "username": "a", "password": "b"})
	if code != http.StatusBadRequest || payload["code"] != "invalid_server_url" {
		t.Fatalf("invalid url login = %d %v", code, payload)
	}

	// 不可达服务器 → 502
	code, payload = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/login", "secret", map[string]string{"serverUrl": "http://127.0.0.1:1", "username": "a", "password": "b"})
	if code != http.StatusBadGateway || payload["code"] != "knowledge_unreachable" {
		t.Fatalf("unreachable login = %d %v", code, payload)
	}

	// 空问题 → 400
	code, _ = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/query", "secret", map[string]string{"question": ""})
	if code != http.StatusBadRequest {
		t.Fatalf("empty question = %d", code)
	}
}

func TestKnowledgeGatewayExpiredTokenSignsOut(t *testing.T) {
	core, _, service := newKnowledgeTestServer(t)
	// 直接注入一个对远端已失效的会话（令牌不匹配）
	if err := service.SignIn(knowledge.Session{ServerURL: "http://127.0.0.1:1", Token: "stale", Username: "alice"}); err != nil {
		t.Fatal(err)
	}

	// 远端不可达 → 502；换用可达但令牌失效的远端验证 401 清会话路径
	remote := knowledgeTestRemote(t)
	defer remote.Close()
	if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "stale", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	code, payload := knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/query", "secret", map[string]string{"question": "任何问题"})
	if code != http.StatusUnauthorized || payload["code"] != "knowledge_auth_expired" {
		t.Fatalf("expired query = %d %v", code, payload)
	}
	if status := service.Status(); status.LoggedIn {
		t.Fatalf("session not cleared after expired token: %+v", status)
	}
}
