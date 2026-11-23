package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"easyshare/internal/config"
	"easyshare/internal/knowledge"
	"easyshare/internal/task"
)

func TestKnowledgeProcessDriveFilePreservesIdentityAndVersion(t *testing.T) {
	const fileID = "1981234567890123456"
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/documents/process-drive" {
			t.Errorf("unexpected knowledge operation: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Header.Get("Authorization") != "Bearer cp-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		var input map[string]string
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input) != 2 || input["file_id"] != fileID || input["version_id"] != "7" {
			t.Errorf("drive identity and expected version must be preserved: %v", input)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"id": "job-1", "file_id": "drive-" + fileID, "version_id": "7-abcdef1234567890",
			"filename": "制度.pdf", "status": "queued", "stage": "queued", "progress": 0,
		})
	}))
	defer remote.Close()
	server := NewServer(config.Config{APIToken: "secret"}, task.NewStore())
	service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
	if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "cp-token", Mode: knowledge.ModeSSO}); err != nil {
		t.Fatal(err)
	}
	server.ConfigureKnowledge(service)
	core := httptest.NewServer(server.httpServer.Handler)
	defer core.Close()
	code, payload := knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/documents/process-drive", "secret",
		map[string]string{"fileId": fileID, "versionId": "7"})
	if code != http.StatusAccepted || payload["file_id"] != "drive-"+fileID || payload["version_id"] != "7-abcdef1234567890" || payload["status"] != "queued" {
		t.Fatalf("accepted drive processing = %d %v", code, payload)
	}
	if _, leaked := payload["token"]; leaked {
		t.Fatal("knowledge token must not enter the result")
	}
}

func TestKnowledgeProcessDriveFileRejectsUntrustedInput(t *testing.T) {
	server := NewServer(config.Config{APIToken: "secret"}, task.NewStore())
	service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
	if err := service.SignIn(knowledge.Session{ServerURL: "http://127.0.0.1:1", Token: "cp-token", Mode: knowledge.ModeSSO}); err != nil {
		t.Fatal(err)
	}
	server.ConfigureKnowledge(service)
	core := httptest.NewServer(server.httpServer.Handler)
	defer core.Close()
	for _, body := range []string{
		`{"fileId":"1981234567890123456","versionId":"7"} {"objectKey":"users/other/private.pdf"}`,
		`{"fileId":"1981234567890123456","versionId":"7","owner":"someone-else"}`,
		`{"fileId":1981234567890123456,"versionId":"7"}`,
		`{"fileId":"1981234567890123456","versionId":"0"}`,
	} {
		request, err := http.NewRequest(http.MethodPost, core.URL+"/api/knowledge/documents/process-drive", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("untrusted input was not rejected: status=%d body=%s", response.StatusCode, body)
		}
	}
}

func TestKnowledgeProcessDriveFileRequiresSSOSession(t *testing.T) {
	for _, mode := range []string{"", knowledge.ModePassword} {
		server := NewServer(config.Config{APIToken: "secret"}, task.NewStore())
		service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
		if mode != "" {
			if err := service.SignIn(knowledge.Session{ServerURL: "http://127.0.0.1:1", Token: "local-token", Mode: mode}); err != nil {
				t.Fatal(err)
			}
		}
		server.ConfigureKnowledge(service)
		core := httptest.NewServer(server.httpServer.Handler)
		code, _ := knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/documents/process-drive", "secret",
			map[string]string{"fileId": "1981234567890123456", "versionId": "7"})
		core.Close()
		want := http.StatusForbidden
		if mode == "" {
			want = http.StatusUnauthorized
		}
		if code != want {
			t.Fatalf("session mode %q status=%d, want %d", mode, code, want)
		}
	}
}

func TestKnowledgeProcessDriveFilePreservesPermissionAndVersionErrors(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusConflict, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(status)
				_ = json.NewEncoder(writer).Encode(map[string]string{"detail": "权限或版本已变化，请刷新后重试"})
			}))
			defer remote.Close()
			server := NewServer(config.Config{APIToken: "secret"}, task.NewStore())
			service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
			if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "cp-token", Mode: knowledge.ModeSSO}); err != nil {
				t.Fatal(err)
			}
			server.ConfigureKnowledge(service)
			core := httptest.NewServer(server.httpServer.Handler)
			defer core.Close()
			code, _ := knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/documents/process-drive", "secret",
				map[string]string{"fileId": "1981234567890123456", "versionId": "7"})
			if code != status {
				t.Fatalf("expected actionable status %d, got %d", status, code)
			}
			if !service.Status().LoggedIn {
				t.Fatal("a file permission or service error must not clear the SSO session")
			}
		})
	}
}
