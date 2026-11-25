package api

import (
	"bytes"
	"encoding/json"
	"io"
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

func TestKnowledgeDownloadPropagatesUpstreamRejection(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		for _, kind := range []string{"audio", "export"} {
			t.Run(http.StatusText(status)+"/"+kind, func(t *testing.T) {
				remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]string{"detail": "access unavailable"})
				}))
				defer remote.Close()
				server := NewServer(config.Config{APIToken: "fixture"}, task.NewStore())
				service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
				server.ConfigureKnowledge(service)
				if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "fixture"}); err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.SetPathValue("meetingID", "m1")
				req.SetPathValue("format", "markdown")
				result := httptest.NewRecorder()
				if kind == "audio" {
					server.knowledgeMeetingAudio(result, req)
				} else {
					server.knowledgeMeetingExport(result, req)
				}
				expected := status
				if status == http.StatusServiceUnavailable {
					expected = http.StatusBadGateway
				}
				if result.Code != expected {
					t.Fatalf("upstream=%d gateway=%d body=%q", status, result.Code, result.Body.String())
				}
			})
		}
	}
}

func TestKnowledgeRejectedSSOProbePreservesIndependentSession(t *testing.T) {
	core, remote, service := newKnowledgeTestServer(t)
	session := knowledge.Session{ServerURL: remote.URL, Token: "tok-1", Username: "alice", Role: "member"}
	if err := service.SignIn(session); err != nil {
		t.Fatal(err)
	}
	status, _ := knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/sso", "secret", map[string]string{"serverUrl": remote.URL, "token": "unsupported-sso"})
	if status != http.StatusUnauthorized {
		t.Fatalf("expected rejected SSO probe, got %d", status)
	}
	if service.Current().Token != session.Token {
		t.Fatal("failed automatic SSO probe erased independent login")
	}
	status, _ = knowledgeRequest(t, http.MethodPost, core.URL+"/api/knowledge/query", "secret", map[string]string{"question": "fixture"})
	if status != http.StatusOK {
		t.Fatalf("independent session no longer works: %d", status)
	}
}

// 实际 HTTP 传输使用分块正文，半途换号必须产生下载错误，不能正常 EOF。
func TestKnowledgeDownloadSessionSwitchAbortsPartialResponse(t *testing.T) {
	for _, kind := range []string{"source", "audio", "export"} {
		t.Run(kind, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			first := bytes.Repeat([]byte("a"), 64*1024)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(first)
				w.(http.Flusher).Flush()
				<-release
				_, _ = w.Write([]byte("old-account-tail"))
			}))
			defer remote.Close()
			defer unblock()
			server := NewServer(config.Config{APIToken: "fixture"}, task.NewStore())
			service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
			server.ConfigureKnowledge(service)
			if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "old"}); err != nil {
				t.Fatal(err)
			}
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				req.SetPathValue("meetingID", "m1")
				req.SetPathValue("format", "markdown")
				req.SetPathValue("file_id", "f1")
				req.SetPathValue("version_id", "v1")
				switch kind {
				case "source":
					server.knowledgeDocumentSource(w, req)
				case "audio":
					server.knowledgeMeetingAudio(w, req)
				case "export":
					server.knowledgeMeetingExport(w, req)
				}
			}))
			defer core.Close()
			defer unblock()
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Get(core.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			prefix := make([]byte, len(first))
			if _, err := io.ReadFull(response.Body, prefix); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(prefix, first) {
				t.Fatal("合法下载内容被改变")
			}
			if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "new"}); err != nil {
				t.Fatal(err)
			}
			unblock()
			rest, err := io.ReadAll(response.Body)
			if err == nil {
				t.Fatal("截断流返回正常 EOF，调用方会把残缺文件当成功下载")
			}
			if strings.Contains(string(rest), "old-account-tail") {
				t.Fatal("换号后仍收到旧账号正文")
			}
		})
	}
}

func TestKnowledgeDownloadSuccessPreservesBodyAndHeaders(t *testing.T) {
	for _, kind := range []string{"source", "audio", "export"} {
		t.Run(kind, func(t *testing.T) {
			body := "complete-download-content"
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Disposition", "attachment; filename=fixture.bin")
				_, _ = w.Write([]byte(body))
			}))
			defer remote.Close()
			server := NewServer(config.Config{APIToken: "fixture"}, task.NewStore())
			service := knowledge.NewService(knowledge.NewStore(filepath.Join(t.TempDir(), "knowledge.json")))
			server.ConfigureKnowledge(service)
			if err := service.SignIn(knowledge.Session{ServerURL: remote.URL, Token: "current"}); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.SetPathValue("meetingID", "m1")
			req.SetPathValue("format", "markdown")
			req.SetPathValue("file_id", "f1")
			req.SetPathValue("version_id", "v1")
			result := httptest.NewRecorder()
			switch kind {
			case "source":
				server.knowledgeDocumentSource(result, req)
			case "audio":
				server.knowledgeMeetingAudio(result, req)
			case "export":
				server.knowledgeMeetingExport(result, req)
			}
			if result.Code != http.StatusOK || result.Body.String() != body {
				t.Fatalf("合法下载失败: %d %q", result.Code, result.Body.String())
			}
			if result.Header().Get("Content-Length") != "25" {
				t.Fatalf("下载长度丢失: %v", result.Header())
			}
			if kind != "audio" && result.Header().Get("Content-Disposition") == "" {
				t.Fatal("下载文件名丢失")
			}
		})
	}
}
