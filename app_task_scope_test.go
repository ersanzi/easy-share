package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easyshare/internal/account"
	"easyshare/internal/desktop"
)

func taskScopeReviewApp(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *App {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	return &App{ctx: context.Background(), logger: log.New(io.Discard, "", 0),
		client: desktop.NewClient(server.URL, "ipc-fixture"), accountSession: &account.Session{Token: "alice"}}
}

func TestTaskSnapshotRejectsLatePreviousAccountResult(t *testing.T) {
	var app *App
	app = taskScopeReviewApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			io.WriteString(w, `{}`)
		case "/api/peers":
			io.WriteString(w, `[]`)
		case "/api/tasks":
			if r.Header.Get("X-EasyShare-Account-Token") != "alice" {
				t.Error("任务请求丢失身份")
			}
			app.accountMu.Lock()
			app.accountSession = &account.Session{Token: "bob"}
			app.accountMu.Unlock()
			io.WriteString(w, `[{"id":"old","kind":"cloud_download","fileName":"alice-secret.pdf","localPath":"C:/alice-secret.pdf"}]`)
		default:
			t.Error(r.URL.Path)
		}
	})
	snapshot, err := app.GetSnapshot()
	if err == nil || len(snapshot.Tasks) != 0 {
		t.Fatalf("旧账号快照交给新账号: %+v %v", snapshot, err)
	}
}

func TestTaskPathDoesNotOpenAfterDenialOrAccountSwitch(t *testing.T) {
	for _, scenario := range []string{"allowed", "denied", "switched"} {
		t.Run(scenario, func(t *testing.T) {
			var app *App
			app = taskScopeReviewApp(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/tasks/check-path" {
					t.Error(r.URL.Path)
				}
				if scenario == "denied" {
					http.Error(w, `{"message":"denied"}`, 403)
					return
				}
				if scenario == "switched" {
					app.accountMu.Lock()
					app.accountSession = &account.Session{Token: "bob"}
					app.accountMu.Unlock()
				}
				io.WriteString(w, `{"allowed":true}`)
			})
			opened := false
			err := app.openTaskPath(filepath.Join(t.TempDir(), "private.pdf"), func(string) error { opened = true; return nil })
			if opened != (scenario == "allowed") || (err == nil) != (scenario == "allowed") {
				t.Fatalf("opened=%v err=%v", opened, err)
			}
		})
	}
}

type taskReviewBody struct {
	io.Reader
	before func()
}

func (r *taskReviewBody) Read(p []byte) (int, error) {
	if r.before != nil {
		f := r.before
		r.before = nil
		f()
	}
	return r.Reader.Read(p)
}
func (r *taskReviewBody) Close() error { return nil }

func TestTaskDownloadOnlyPlacesCompleteCurrentAuthorizedResult(t *testing.T) {
	for _, scenario := range []string{"valid", "switch-read", "switch-check", "denied", "short"} {
		t.Run(scenario, func(t *testing.T) {
			var app *App
			lastStatus := ""
			app = taskScopeReviewApp(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-EasyShare-Account-Token") != "alice" {
					t.Error("后台旧任务借用了新账号")
				}
				if r.URL.Path == "/api/tasks/check-path" {
					if scenario == "denied" {
						http.Error(w, `{"message":"revoked"}`, 403)
						return
					}
					if scenario == "switch-check" {
						app.accountMu.Lock()
						app.accountSession = &account.Session{Token: "bob"}
						app.accountMu.Unlock()
					}
					io.WriteString(w, `{"allowed":true}`)
					return
				}
				var patch map[string]any
				json.NewDecoder(r.Body).Decode(&patch)
				if status, ok := patch["status"].(string); ok {
					lastStatus = status
				}
				io.WriteString(w, `{}`)
			})
			session := app.accountSession
			dir := t.TempDir()
			target := filepath.Join(dir, "download.txt")
			os.WriteFile(target, []byte("existing"), 0600)
			temp, err := os.CreateTemp(dir, ".part-*")
			if err != nil {
				t.Fatal(err)
			}
			body := &taskReviewBody{Reader: strings.NewReader("payload")}
			if scenario == "switch-read" {
				body.before = func() {
					app.accountMu.Lock()
					app.accountSession = &account.Session{Token: "bob"}
					app.accountMu.Unlock()
				}
			}
			total := int64(7)
			if scenario == "short" {
				total = 9
			}
			app.copyCloudDownload(app.client.WithAccountToken("alice"), "task-fixture", body, temp, target, total, session)
			content, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			want := "existing"
			status := "failed"
			if scenario == "valid" {
				want = "payload"
				status = "completed"
			}
			if string(content) != want || lastStatus != status {
				t.Fatalf("target=%q status=%s", content, lastStatus)
			}
			if _, err := os.Stat(temp.Name()); !os.IsNotExist(err) {
				t.Fatalf("临时文件未清理: %v", err)
			}
		})
	}
}
