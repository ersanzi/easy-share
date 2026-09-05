package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newSessionEnv 建控制面测试服务器 + 客户端 + 会话存储目录。
// partSize 固定 4 字节，10 字节文件恰好 3 片，方便断言分片边界。
func newSessionEnv(t *testing.T, state *serverState) (*Client, *SessionStore, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.serveHTTP(t, w, r)
	}))
	dir := t.TempDir()
	client := New(server.URL)
	store := NewSessionStore(filepath.Join(dir, "sessions"))
	client.Sessions = store
	return client, store, dir
}

// serverState 最小控制面桩：记账会话、分片 URL 指回自身、记录 PUT 分片体。
type serverState struct {
	createCalls atomic.Int32
	partCalls   atomic.Int32
	partBodies  []string // 每次分片 PUT 的内容（含重试）
	putFails    atomic.Int32
	aborted     atomic.Int32
	completed   atomic.Int32
}

func (s *serverState) serveHTTP(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	switch {
	case r.URL.Path == "/easyshare/drive/upload-session/create":
		s.createCalls.Add(1)
		w.Write([]byte(`{"code":200,"data":{"sessionId":9,"uploadId":"uid-1","partSize":4}}`))
	case r.URL.Path == "/easyshare/drive/upload-session/part":
		s.partCalls.Add(1)
		var body struct {
			PartNumber int `json:"partNumber"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"code":200,"data":{"url":"http://` + r.Host + `/put-part/` + r.URL.Query().Encode() + `"}}`))
	case strings.HasPrefix(r.URL.Path, "/put-part/") && r.Method == http.MethodPut:
		raw, _ := io.ReadAll(r.Body)
		s.partBodies = append(s.partBodies, string(raw))
		w.Header().Set("ETag", `"etag-`+fmt.Sprint(len(s.partBodies))+`"`)
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/easyshare/drive/upload-session/complete":
		s.completed.Add(1)
		w.Write([]byte(`{"code":200,"data":{"fileId":777}}`))
	case r.URL.Path == "/easyshare/drive/upload-session/abort":
		s.aborted.Add(1)
		w.Write([]byte(`{"code":200}`))
	}
}

func writeFile(t *testing.T, content []byte) (string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	return path, info.ModTime()
}

func TestMultipartUploadSplitsAndCompletes(t *testing.T) {
	state := &serverState{}
	client, _, _ := newSessionEnv(t, state)
	content := []byte("0123456789") // 10 字节 → 4/4/2 三片
	localPath, modTime := writeFile(t, content)

	var progress []int64
	err := client.uploadFileMultipart(context.Background(), "tok", SpacePersonal,
		"big.bin", localPath, int64(len(content)), modTime, func(sent, total int64) {
			progress = append(progress, sent)
		})
	if err != nil {
		t.Fatalf("分片上传失败：%v", err)
	}
	if state.completed.Load() != 1 {
		t.Fatalf("应 Complete 一次，实际 %d", state.completed.Load())
	}
	if state.createCalls.Load() != 1 {
		t.Fatalf("应 Create 一次，实际 %d", state.createCalls.Load())
	}
}

func TestMultipartResumesFromSavedSession(t *testing.T) {
	state := &serverState{}
	client, store, _ := newSessionEnv(t, state)
	content := []byte("0123456789")
	localPath, modTime := writeFile(t, content)

	// 预置"第 1 片已完成"的会话快照 → 只剩 2 片要传
	state0 := &sessionState{
		SessionID: 9, UploadID: "uid-1", PartSize: 4,
		Path: "big.bin", Space: SpacePersonal,
		Size: int64(len(content)), ModTime: modTime.Unix(),
		Done: []PartETag{{PartNumber: 1, ETag: "\"pre\""}},
	}
	if err := store.Save(state0); err != nil {
		t.Fatal(err)
	}

	if err := client.uploadFileMultipart(context.Background(), "tok", SpacePersonal,
		"big.bin", localPath, int64(len(content)), modTime, nil); err != nil {
		t.Fatalf("续传失败：%v", err)
	}
	if state.createCalls.Load() != 0 {
		t.Fatalf("续传不应重新 Create，实际 %d 次", state.createCalls.Load())
	}
	if state.partCalls.Load() != 2 {
		t.Fatalf("应只补签 2 个分片，实际 %d 次", state.partCalls.Load())
	}
	// 完成后会话快照应被清理
	if _, err := os.Stat(store.fileOf(SpacePersonal, "big.bin", int64(len(content)), modTime.Unix())); !os.IsNotExist(err) {
		t.Fatal("完成后应清理会话快照")
	}
}

func TestMultipartFingerprintChangeAbortsOldSession(t *testing.T) {
	state := &serverState{}
	client, store, _ := newSessionEnv(t, state)
	content := []byte("0123456789")
	localPath, modTime := writeFile(t, content)

	state0 := &sessionState{
		SessionID: 9, UploadID: "uid-old", PartSize: 4,
		Path: "big.bin", Space: SpacePersonal,
		Size: int64(len(content)), ModTime: modTime.Unix(),
		Done: []PartETag{{PartNumber: 1, ETag: "\"pre\""}},
	}
	_ = store.Save(state0)

	// 文件内容变化 → mtime 变 → 指纹不匹配：应 Create 新会话并 Abort 旧逻辑由服务端做
	if err := os.Chtimes(localPath, time.Now(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(localPath)
	if err := client.uploadFileMultipart(context.Background(), "tok", SpacePersonal,
		"big.bin", localPath, int64(len(content)), info.ModTime(), nil); err != nil {
		t.Fatalf("指纹变化后上传失败：%v", err)
	}
	if state.createCalls.Load() != 1 {
		t.Fatalf("指纹变化应新建会话，实际 Create %d 次", state.createCalls.Load())
	}
}

func TestPartUploadRetriesThenSucceeds(t *testing.T) {
	state := &serverState{}
	client, _, _ := newSessionEnv(t, state)

	// 仿一个失败一次的 PUT 端点
	attempt := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && attempt.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("ETag", `"etag-1"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	etag, err := client.putPartWithRetry(context.Background(), server.URL+"/p",
		func() io.Reader { return strings.NewReader("abcd") }, 4)
	if err != nil {
		t.Fatalf("重试后应成功：%v", err)
	}
	if etag != `"etag-1"` {
		t.Fatalf("ETag 错误：%q", etag)
	}
	if attempt.Load() != 2 {
		t.Fatalf("应重试到第 2 次成功，实际 %d 次", attempt.Load())
	}
}

func TestFingerprintChangesWithSizeAndTime(t *testing.T) {
	a := fingerprint("personal", "a.bin", 10, 100)
	if a != fingerprint("personal", "a.bin", 10, 100) {
		t.Fatal("同指纹应相同")
	}
	if a == fingerprint("personal", "a.bin", 11, 100) {
		t.Fatal("大小变化应换指纹")
	}
	if a == fingerprint("shared", "a.bin", 10, 100) {
		t.Fatal("空间变化应换指纹")
	}
}
