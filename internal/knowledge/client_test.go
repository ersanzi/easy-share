package knowledge

import (
	"context"
	"encoding/json"
	"io"
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
		_ = json.NewEncoder(writer).Encode(map[string]any{"records": 12, "documents": 3, "llm": "configured", "watch_dirs": 1})
	})
	mux.HandleFunc("GET /documents", func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"documents": []map[string]any{{
				"job_id": "job-1", "file_id": "file-1", "version_id": "v2",
				"filename": "差旅制度.pdf", "status": "failed", "stage": "parsing",
				"progress": 40, "error_message": "NoSuchKey", "ready": true,
				"read_version_id": "v1", "chunks": 12, "characters": 3456,
				"updated_at": "2026-10-20T11:00:00Z", "processed_at": "2026-10-20T10:00:00Z",
			}},
		})
	})
	mux.HandleFunc("GET /documents/file-1/versions/v1/view", func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"manifest": map[string]any{
				"file_id": "file-1", "version_id": "v1", "filename": "差旅制度.pdf",
				"object_key": "source/file-1.pdf", "blocks": 1, "processed_at": "2026-10-20T10:00:00Z",
				"visible_depts": "10,20",
			},
			"document": map[string]any{
				"file_id": "file-1", "version_id": "v1", "filename": "差旅制度.pdf",
				"blocks": []map[string]any{{
					"id": "b1", "type": "paragraph", "text": "住宿标准每晚400元",
					"source": map[string]any{"page": 3},
				}},
			},
		})
	})
	mux.HandleFunc("GET /documents/file-1/versions/v1/source", func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/pdf")
		_, _ = writer.Write([]byte("%PDF-source"))
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

func TestClientDocuments(t *testing.T) {
	remote := fakeKnowledgeServer(t, "tok-1")
	defer remote.Close()
	client := NewClient(remote.URL)

	list, err := client.Documents(context.Background(), "tok-1", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Documents) != 1 {
		t.Fatalf("documents = %+v", list)
	}
	document := list.Documents[0]
	if document.FileID != "file-1" || document.Status != "failed" || !document.Ready {
		t.Fatalf("document = %+v", document)
	}
	if document.ReadVersionID == nil || *document.ReadVersionID != "v1" {
		t.Fatalf("read version = %+v", document.ReadVersionID)
	}
	if document.Chunks != 12 {
		t.Fatalf("chunks = %d", document.Chunks)
	}
}

func TestClientDocumentViewAndSource(t *testing.T) {
	remote := fakeKnowledgeServer(t, "tok-1")
	defer remote.Close()
	client := NewClient(remote.URL)

	view, err := client.DocumentView(context.Background(), "tok-1", "file-1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Manifest.Filename != "差旅制度.pdf" || view.Manifest.Blocks != 1 {
		t.Fatalf("manifest = %+v", view.Manifest)
	}
	if len(view.Document.Blocks) != 1 || view.Document.Blocks[0].Text == "" {
		t.Fatalf("document = %+v", view.Document)
	}
	if view.Document.Blocks[0].Source.Page == nil || *view.Document.Blocks[0].Source.Page != 3 {
		t.Fatalf("source page = %+v", view.Document.Blocks[0].Source)
	}

	response, err := client.OpenDocumentSource(context.Background(), "tok-1", "file-1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "%PDF-source" {
		t.Fatalf("source body = %q", body)
	}

	// 未带令牌 → 远端 401，必须映射为 RemoteError
	if _, err := client.OpenDocumentSource(context.Background(), "", "file-1", "v1"); err == nil {
		t.Fatal("expected unauthorized error")
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
