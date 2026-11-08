//go:build windows

package cfmount

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"golang.org/x/sys/windows"
)

func TestAutomaticVerifiedCacheWindows(t *testing.T) {
	// CFAPI 安全路径核对使用长路径，不把 ADMINI~1 别名当作规范根。
	root, state := trustedDirectoryTemp(t), trustedDirectoryTemp(t)
	f := &cacheFake{wbFake: &wbFake{data: map[string][]byte{}, versions: map[string]int64{}}, id: 123}
	e, err := NewWritebackEngine(WritebackConfig{RootPath: root, StateDir: state, Space: "personal", Namespace: "isolated-auto-cache", Client: f, Token: func() string { return "fake" }, StableFor: time.Nanosecond, CacheIdleFor: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	wbWrite(t, e.cfg, "a", "payload")
	wbStable(t, e)
	cfg := Config{RootPath: e.cfg.RootPath, Space: e.cfg.Space, Client: f, Token: e.cfg.Token, Writable: true}
	remote := List(cfg)
	m, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: cfg.RootPath, Provider: cfapi.ProviderInfo{Name: "EasyShareIsolatedAutoCache", Version: "test", ID: windows.GUID{Data1: 0xa9ca12b4}}, SyncRootIdentity: []byte("isolated-auto-cache"), ProtectedPaths: []string{"a"}, List: remote, Open: Open(cfg)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
		if err := cfapi.UnregisterSyncRoot(cfg.RootPath); err != nil {
			t.Error(err)
		}
	}()
	releases := 0
	reconcile := func() WritebackResult {
		t.Helper()
		out, err := e.Reconcile(context.Background(), func(_ context.Context, p []string) error { return m.SyncProtected(remote, p) }, func(ctx context.Context, c CacheCandidate) (bool, error) {
			id := cfapi.DirectoryNTFSIdentity{VolumeSerial: c.LocalIdentity.VolumeSerial, FileID: c.LocalIdentity.FileID}
			r, err := m.ReleaseVerifiedCache(ctx, cfapi.CacheBaseline{RelativePath: c.RelativePath, RemoteFileID: c.RemoteFileID, Version: c.Version, Size: c.Size, SHA256: c.SHA256, LocalInfo: c.LocalInfo, LocalIdentity: id, Confirm: c.Confirm})
			if r.Released {
				releases++
			}
			return r.Released, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := reconcile()
	if releases != 1 || len(out.Issues) != 0 {
		t.Fatalf("release: %+v count=%d", out, releases)
	}
	// Automatic scans must not hydrate online-only content or attempt another release.
	reconcile()
	reconcile()
	info, err := os.Lstat(filepath.Join(cfg.RootPath, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if safe, err := writebackReadable(filepath.Join(cfg.RootPath, "a"), info); err != nil || safe {
		t.Fatalf("scan hydrated cache: %v %v", safe, err)
	}
	if releases != 1 {
		t.Fatal("repeated release")
	}
	b, err := os.ReadFile(filepath.Join(cfg.RootPath, "a"))
	if err != nil || string(b) != "payload" {
		t.Fatalf("fetch: %q %v", b, err)
	}
	reconcile()
	time.Sleep(time.Millisecond)
	out = reconcile()
	if releases != 2 || len(out.Issues) != 0 {
		t.Fatalf("rehydrated auto release: %+v count=%d", out, releases)
	}
}
