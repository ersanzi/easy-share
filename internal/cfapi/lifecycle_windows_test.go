//go:build windows

package cfapi

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Explicit destructive cleanup belongs only to disposable test roots, not Close.
func mountSpaceForTest(t *testing.T, opt MountOptions) (*Mount, error) {
	return mountSpaceWithForTest(t, opt, MountSpace)
}

func mountSpaceWithForTest(t *testing.T, opt MountOptions, connect func(MountOptions) (*Mount, error)) (*Mount, error) {
	t.Helper()
	m, err := connect(opt)
	t.Cleanup(func() {
		if m != nil {
			if err := m.Close(); err != nil {
				t.Errorf("disconnect test root: %v", err)
				return
			}
		}
		_, exists, err := QuerySyncRootStandardInfo(opt.RootPath)
		if err != nil {
			t.Errorf("query test root cleanup: %v", err)
			return
		}
		if exists {
			if err := UnregisterSyncRoot(opt.RootPath); err != nil {
				t.Errorf("explicit test unregister: %v", err)
			}
		}
	})
	return m, err
}

func TestStandardInfoBoundsAndLayout(t *testing.T) {
	b := make([]byte, 1064)
	binary.LittleEndian.PutUint16(b[8:], 2)
	binary.LittleEndian.PutUint16(b[12:], 3)
	binary.LittleEndian.PutUint16(b[28:], 'P')
	binary.LittleEndian.PutUint32(b[1052:], 3)
	copy(b[1056:], "abc")
	i, err := parseSyncRootStandardInfo(b, 1059)
	if err != nil || i.Hydration.Primary != 2 || i.Population.Primary != 3 || i.ProviderName != "P" || string(i.Identity) != "abc" {
		t.Fatalf("SDK layout: %+v %v", i, err)
	}
	for _, n := range []uint32{0, 1055, 1065} {
		if _, err := parseSyncRootStandardInfo(b, n); err == nil {
			t.Fatalf("accepted length %d", n)
		}
	}
	binary.LittleEndian.PutUint32(b[1052:], 9)
	if _, err := parseSyncRootStandardInfo(b, 1064); err == nil {
		t.Fatal("accepted truncated identity")
	}
}

func TestPersistentDisconnectReconnect(t *testing.T) {
	if !Available() {
		t.Skip("CfApi unavailable")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "unknown"), 0700); err != nil {
		t.Fatal(err)
	}
	entries := []CloudEntry{{Path: "docs", IsDir: true, Identity: []byte("esd:docs")}, {Path: "docs/deep", IsDir: true, Identity: []byte("esd:deep")}, {Path: "docs/deep/unread", Size: 137, Identity: EncodeIdentity(701)}}
	opt := MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte("persistent-lifecycle"), ProtectedPaths: []string{"unknown"}, List: func(context.Context) ([]CloudEntry, error) { return entries, nil }, Open: func(context.Context, string, int64, int64) (io.ReadCloser, error) {
		t.Error("unexpected hydration")
		return nil, io.EOF
	}}
	m, err := mountSpaceForTest(t, opt)
	if err != nil {
		t.Fatal(err)
	}
	attrs := func(rel string) (uint32, uint32) {
		t.Helper()
		p, _ := windows.UTF16PtrFromString(filepath.Join(root, filepath.FromSlash(rel)))
		var d windows.Win32finddata
		h, err := windows.FindFirstFile(p, &d)
		if err != nil {
			t.Fatal(err)
		}
		windows.FindClose(h)
		return d.FileAttributes, d.Reserved0
	}
	// FindFirstFile may hide Cloud reparse metadata while disconnected. Query the
	// actual placeholder identity through CfGetPlaceholderInfo on an attribute handle.
	placeholderIdentity := func(rel string) string {
		t.Helper()
		h, err := OpenPlaceholderHandle(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.CloseHandle(h)
		p, err := proc("CfGetPlaceholderInfo")
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 28+4096+8)
		var returned uint32
		hr, _, _ := p.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&returned)))
		if err := checkHResult(hr, "CfGetPlaceholderInfo"); err != nil {
			t.Fatal(err)
		}
		if returned < 28 || returned > uint32(len(buf)) {
			t.Fatal("invalid placeholder length")
		}
		n := binary.LittleEndian.Uint32(buf[24:28])
		if n > returned-28 {
			t.Fatal("invalid placeholder identity")
		}
		return string(buf[28 : 28+n])
	}
	beforeIdentity := placeholderIdentity("docs/deep")
	a, tag := attrs("docs/deep")
	if a&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 || tag&0xffff0fff != 0x9000001a {
		t.Fatalf("not Cloud: %x %x", a, tag)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	i, exists, err := QuerySyncRootStandardInfo(root)
	if err != nil || !exists || string(i.Identity) != "persistent-lifecycle" {
		t.Fatalf("registration lost: %+v %v", i, err)
	}
	a2, tag2 := attrs("docs/deep")
	t.Logf("directory FindFirstFile attributes/tag connected=%x/%x disconnected=%x/%x", a, tag, a2, tag2)
	if got := placeholderIdentity("docs/deep"); got != beforeIdentity || got != "esd:deep" {
		t.Fatalf("persistent directory identity changed: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(root, "docs/deep/unread")); err != nil {
		t.Fatal("disconnect removed unread", err)
	}
	if allocatedSize(t, filepath.Join(root, "docs/deep/unread")) != 0 {
		t.Fatal("unread hydrated")
	}
	// Wrong account/provider and nested root must fail before UPDATE/Connect.
	bad := opt
	bad.SyncRootIdentity = []byte("wrong-account")
	if x, e := MountSpace(bad); e == nil {
		x.Close()
		t.Fatal("accepted wrong identity")
	}
	bad = opt
	bad.Provider.Name = "foreign"
	if x, e := MountSpace(bad); e == nil {
		x.Close()
		t.Fatal("accepted foreign provider")
	}
	bad = opt
	bad.RootPath = filepath.Join(root, "docs")
	if x, e := MountSpace(bad); e == nil {
		x.Close()
		t.Fatal("accepted descendant root")
	}
	// A failed initial list must disconnect but preserve all persistent placeholders.
	failed := opt
	failed.List = func(context.Context) ([]CloudEntry, error) { return nil, errFakeCloudDown }
	if x, e := MountSpace(failed); x != nil || !errors.Is(e, errFakeCloudDown) {
		t.Fatalf("unexpected failure: %v %v", x, e)
	}
	if _, err := os.Lstat(filepath.Join(root, "docs/deep/unread")); err != nil {
		t.Fatal("failure removed unread", err)
	}
	// A concurrent/duplicate connection failure likewise must not unregister.
	m, err = mountSpaceForTest(t, opt)
	if err != nil {
		t.Fatal(err)
	}
	ra, rt := attrs("docs/deep")
	if ra&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 || rt&0xffff0fff != 0x9000001a {
		t.Fatalf("reconnect lost persistent directory Cloud tag: %x/%x", ra, rt)
	}
	if x, e := MountSpace(opt); e == nil {
		x.Close()
		t.Fatal("duplicate connection unexpectedly accepted")
	}
	if _, exists, err := QuerySyncRootStandardInfo(root); err != nil || !exists {
		t.Fatalf("connect failure removed registration: %v", err)
	}
	entries = append(entries, CloudEntry{Path: "docs/deep/new", Size: 17, Identity: EncodeIdentity(702)}, CloudEntry{Path: "unknown/remote", Size: 1, Identity: EncodeIdentity(703)})
	if err := m.SyncProtected(opt.List, []string{"unknown"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "docs/deep/new")); err != nil {
		t.Fatal("deep remote not projected", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "unknown/remote")); !os.IsNotExist(err) {
		t.Fatal("unknown ordinary directory protection bypassed")
	}
}
