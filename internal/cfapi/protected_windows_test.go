//go:build windows

package cfapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPathProtected(t *testing.T) {
	for _, p := range []string{"docs", "DOCS/local.txt", "docs/local.txt/child"} {
		if !pathProtected(p, []string{"Docs/local.txt"}) {
			t.Errorf("not protected: %s", p)
		}
	}
	for _, p := range []string{"docs/other.txt", "docs2"} {
		if pathProtected(p, []string{"docs/local.txt"}) {
			t.Errorf("overmatched: %s", p)
		}
	}
	for _, p := range []string{"../escape", "a/../b", "C:/file", "/abs", "a\\b", "a.", "a "} {
		if validProtectedEntry(p) {
			t.Errorf("unsafe accepted: %s", p)
		}
	}
}

func TestProtectedSyncRetainsBytesAndFullHydrationIndex(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local.txt")
	if err := os.WriteFile(local, []byte("new local bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	m := &Mount{rootPath: root}
	entries := []CloudEntry{{Path: "local.txt", Size: 1, Identity: EncodeIdentity(42)}}
	// The local path was created AFTER the hypothetical scan (no explicit paths).
	if err := m.SyncProtected(func(context.Context) ([]CloudEntry, error) { return entries, nil }, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(local)
	if err != nil || string(b) != "new local bytes" {
		t.Fatalf("local overwritten: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "empty")); err != nil {
		t.Fatalf("empty directory removed: %v", err)
	}
	if m.index["42"] != "local.txt" {
		t.Fatalf("protected entry lost from hydration index: %v", m.index)
	}
}

func TestProtectedInitialListHandshake(t *testing.T) {
	root := t.TempDir()
	m := &Mount{rootPath: root}
	list := func(ctx context.Context) ([]CloudEntry, error) {
		if !ProtectPaths(ctx, []string{"docs/local"}) {
			t.Fatal("missing protection context")
		}
		return []CloudEntry{{Path: "docs", IsDir: true}, {Path: "docs/local", Identity: EncodeIdentity(5)}}, nil
	}
	if err := m.Sync(list); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs")); !os.IsNotExist(err) {
		t.Fatalf("protected ancestor created: %v", err)
	}
	if m.index["5"] != "docs/local" {
		t.Fatal("initial index filtered")
	}
	if ProtectPaths(context.Background(), nil) {
		t.Fatal("unscoped list accepted")
	}
}

func TestProtectedCreationDoesNotClaimInSync(t *testing.T) {
	ph := NewPlaceholders([]PlaceholderSpec{{RelativeName: "new", NoMarkInSync: true}, {RelativeName: "old"}})
	if ph.Infos[0].Flags&PlaceholderCreateFlagMarkInSync != 0 {
		t.Fatal("writable new entry marked in-sync")
	}
	if ph.Infos[1].Flags&PlaceholderCreateFlagMarkInSync == 0 {
		t.Fatal("legacy default changed")
	}
}
