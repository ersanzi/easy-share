package knowledge

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.json")
	store := NewStore(path)

	// 缺文件 → 空会话，不报错
	session, err := store.Load()
	if err != nil || session != (Session{}) {
		t.Fatalf("initial load = %+v, err = %v", session, err)
	}

	want := Session{ServerURL: "http://kb:8000", Token: "tok", Username: "alice", Role: "member", ExpiresAt: "2026-12-31"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded != want {
		t.Fatalf("loaded = %+v, err = %v", loaded, err)
	}
	// 会话文件不得全局可读（Windows 上 Chmod 近似 no-op，仅 POSIX 断言）
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("session file mode = %v", info.Mode())
		}
	}

	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file still exists after clear")
	}
	// 再次 Clear（文件已不存在）不应报错
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceLifecycle(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "knowledge.json"))
	service := NewService(store)

	status := service.Status()
	if status.Configured || status.LoggedIn {
		t.Fatalf("fresh status = %+v", status)
	}

	session := Session{ServerURL: "http://kb:8000", Token: "tok", Username: "alice", Role: "member"}
	if err := service.SignIn(session); err != nil {
		t.Fatal(err)
	}
	status = service.Status()
	if !status.Configured || !status.LoggedIn || status.Username != "alice" || status.ServerURL != "http://kb:8000" {
		t.Fatalf("signed-in status = %+v", status)
	}
	if got := service.Current(); got != session {
		t.Fatalf("current = %+v", got)
	}

	// 重启语义：新 Service 从磁盘恢复登录态
	restarted := NewService(store)
	if restarted.Status() != status {
		t.Fatalf("restarted status = %+v", restarted.Status())
	}

	if err := service.SignOut(); err != nil {
		t.Fatal(err)
	}
	if status := service.Status(); status.LoggedIn || status.Configured {
		t.Fatalf("signed-out status = %+v", status)
	}
}
