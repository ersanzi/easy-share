//go:build windows

package cfmount

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

func trustedDirectoryTemp(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	h, e := trustedDirectoryOpen(p, windows.FILE_READ_ATTRIBUTES)
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(h)
	b := make([]uint16, 32768)
	n, e := windows.GetFinalPathNameByHandle(h, &b[0], uint32(len(b)), 0)
	if e != nil {
		t.Fatal(e)
	}
	return strings.TrimPrefix(windows.UTF16ToString(b[:n]), `\\?\`)
}

func trustedDirectorySource(t *testing.T, root, state string) cfapi.VerifiedDirectoryRestore {
	t.Helper()
	a, e := cfapi.ReadDirectoryNTFSIdentity(root)
	if e != nil {
		t.Fatal(e)
	}
	b, e := cfapi.ReadDirectoryNTFSIdentity(filepath.Join(root, "docs"))
	if e != nil {
		t.Fatal(e)
	}
	return cfapi.VerifiedDirectoryRestore{Namespace: "test-server/account/personal", Root: root, RelativePath: "docs", RemoteDirectoryFileID: "701", PlaceholderIdentity: dirIdentity("docs"), ExpectedRoot: a, ExpectedDirectory: b, StateDirectory: state}
}
func trustedDirectoryRecordPath(s cfapi.VerifiedDirectoryRestore) string {
	d := sha256.Sum256([]byte(s.Namespace + "\x00" + strings.ToLower(s.Root) + "\x00" + strings.ToLower(s.RelativePath)))
	return filepath.Join(s.StateDirectory, "directory-restore-v1-"+hex.EncodeToString(d[:])+".json")
}

type trustedDirectoryClient struct{ *wbFake }

func (c *trustedDirectoryClient) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	rows, e := c.wbFake.Objects(ctx, token, space)
	for i := range rows {
		rows[i].FileId = 702
		rows[i].LastModified = time.Unix(1767225600, 0)
	}
	return append(rows, drive.Object{Path: "docs", Directory: true, FileId: 701}), e
}

func TestTrustedDirectoryProvenanceRejectsUncommittedAndForgery(t *testing.T) {
	root, state := trustedDirectoryTemp(t), trustedDirectoryTemp(t)
	if e := os.Mkdir(filepath.Join(root, "docs"), 0700); e != nil {
		t.Fatal(e)
	}
	s := trustedDirectorySource(t, root, state)
	v, e := NewTrustedDirectoryVerifier([]cfapi.VerifiedDirectoryRestore{s})
	if e != nil {
		t.Fatal(e)
	}
	remote := drive.Object{Path: "docs", Directory: true, FileId: 701}
	verify := func(want bool) {
		t.Helper()
		ok, e := v.Verify(context.Background(), s.Namespace, s.Root, "docs", remote)
		if ok != want {
			t.Fatalf("verify=%v err=%v", ok, e)
		}
	}
	verify(false)
	// Ordinary Directory=true state is not provenance.
	if e = os.WriteFile(filepath.Join(state, "pending.json"), []byte(`{"records":{"docs":{"directory":true}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	verify(false)
	// Even a correctly named record with a forged namespace or NTFS identity
	// must differ from the independently supplied explicit authorization.
	for _, kind := range []string{"namespace", "directory-id", "root-id", "kind"} {
		forged := s
		recordKind := "verified-directory-metadata-conversion"
		switch kind {
		case "namespace":
			forged.Namespace = "attacker/account"
		case "directory-id":
			forged.ExpectedDirectory.FileID[0] ^= 0xff
		case "root-id":
			forged.ExpectedRoot.FileID[0] ^= 0xff
		case "kind":
			recordKind = "directory"
		}
		r := map[string]any{"version": 1, "kind": recordKind, "source": forged, "actual": forged.ExpectedDirectory, "usn": 0}
		b, _ := json.Marshal(r)
		if e = os.WriteFile(trustedDirectoryRecordPath(s), b, 0600); e != nil {
			t.Fatal(e)
		}
		verify(false)
	}
}

func TestTrustedDirectoryRealCFAPIProjectionPreservesLocal(t *testing.T) {
	if !cfapi.Available() {
		t.Skip("CFAPI unavailable")
	}
	root, state := trustedDirectoryTemp(t), trustedDirectoryTemp(t)
	sr, e := cfapi.NewSyncRoot(cfapi.ProviderInfo{Name: "TrustedDirectoryTest", Version: "1", ID: windows.GUID{Data1: 0x1caef7e1, Data2: 0x4343, Data3: 0x4151, Data4: [8]byte{0xa4, 0x51, 1, 2, 3, 4, 5, 6}}}, []byte("trusted-directory-isolated-test"), []byte("esf:"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = cfapi.RegisterSyncRoot(root, &sr.Registration, &sr.Policies, cfapi.RegisterFlagNone); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := cfapi.UnregisterSyncRoot(root); e != nil {
			t.Errorf("test root unregister: %v", e)
		}
	}()
	if e = os.Mkdir(filepath.Join(root, "docs"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(root, "unknown"), 0700); e != nil {
		t.Fatal(e)
	}
	payload := bytes.Repeat([]byte("local bytes must never change\x00"), 4096)
	localPath := filepath.Join(root, "docs", "local.bin")
	if e = os.WriteFile(localPath, payload, 0600); e != nil {
		t.Fatal(e)
	}
	s := trustedDirectorySource(t, root, state)
	result, e := cfapi.RestoreVerifiedDirectory(s)
	if e != nil {
		t.Fatalf("convert: %+v %v", result, e)
	}
	v, e := NewTrustedDirectoryVerifier([]cfapi.VerifiedDirectoryRestore{s})
	if e != nil {
		t.Fatal(e)
	}
	remote := drive.Object{Path: "docs", Directory: true, FileId: 701}
	if ok, e := v.Verify(context.Background(), s.Namespace, root, "docs", remote); !ok || e != nil {
		t.Fatalf("valid committed provenance rejected: %v %v", ok, e)
	}
	for _, mutation := range []string{"namespace", "remote-id", "ntfs-id", "root-id"} {
		ns := s.Namespace
		r := remote
		source := s
		switch mutation {
		case "namespace":
			ns = "other/account"
		case "remote-id":
			r.FileId++
		case "ntfs-id":
			source.ExpectedDirectory.FileID[0] ^= 0xff
		case "root-id":
			source.ExpectedRoot.FileID[0] ^= 0xff
		}
		verifier, e := NewTrustedDirectoryVerifier([]cfapi.VerifiedDirectoryRestore{source})
		if e != nil {
			t.Fatal(e)
		}
		if ok, e := verifier.Verify(context.Background(), ns, root, "docs", r); ok {
			t.Fatalf("accepted %s: %v", mutation, e)
		}
	}
	// Simulate replacing directory with another real NTFS object after commit.
	if e = os.Rename(filepath.Join(root, "docs"), filepath.Join(root, "saved-docs")); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(root, "docs"), 0700); e != nil {
		t.Fatal(e)
	}
	if ok, e := v.Verify(context.Background(), s.Namespace, root, "docs", remote); ok {
		t.Fatalf("replacement NTFS identity accepted: %v", e)
	}
	if e = os.Remove(filepath.Join(root, "docs")); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(filepath.Join(root, "saved-docs"), filepath.Join(root, "docs")); e != nil {
		t.Fatal(e)
	}
	client := &trustedDirectoryClient{&wbFake{data: map[string][]byte{"docs/missing.bin": []byte("remote"), "unknown/missing.bin": []byte("blocked")}, versions: map[string]int64{"docs/missing.bin": 1, "unknown/missing.bin": 1}}}
	cfg := WritebackConfig{RootPath: root, StateDir: filepath.Join(state, "writeback"), Namespace: s.Namespace, Space: "personal", Client: client, Token: func() string { return "test" }, StableFor: time.Hour, TrustedDirectory: v.Verify}
	engine, e := NewWritebackEngine(cfg)
	if e != nil {
		t.Fatal(e)
	}
	// Only one scan: local content is unstable and must NOT be uploaded.
	out, e := engine.Reconcile(context.Background(), func(ctx context.Context, protected []string) error {
		rows, e := client.Objects(ctx, "test", "personal")
		if e != nil {
			return e
		}
		stats := cfapi.BuildTreeProtected(root, entries(rows, true), protected)
		if len(stats.Failures) > 0 {
			return &stats
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	foundLocal, foundUnknown := false, false
	for _, p := range out.ProtectedPaths {
		if p == "docs" {
			t.Fatal("trusted docs still subtree protected")
		}
		foundLocal = foundLocal || p == "docs/local.bin"
		foundUnknown = foundUnknown || p == "unknown"
	}
	if !foundLocal || !foundUnknown {
		t.Fatalf("file/unknown protection lost: %v", out.ProtectedPaths)
	}
	if _, e = os.Lstat(filepath.Join(root, "docs", "missing.bin")); e != nil {
		t.Fatalf("trusted remote child not projected: %v", e)
	}
	if _, e = os.Lstat(filepath.Join(root, "unknown", "missing.bin")); !os.IsNotExist(e) {
		t.Fatalf("unknown directory opened: %v", e)
	}
	got, e := os.ReadFile(localPath)
	if e != nil || !bytes.Equal(got, payload) {
		t.Fatalf("local bytes changed: %v", e)
	}
	if client.uploads != 0 {
		t.Fatalf("unexpected upload: %d", client.uploads)
	}
	u, e := windows.UTF16PtrFromString(filepath.Join(root, "docs"))
	if e != nil {
		t.Fatal(e)
	}
	var converted windows.Win32finddata
	h, e := windows.FindFirstFile(u, &converted)
	if e != nil {
		t.Fatal(e)
	}
	windows.FindClose(h)
	t.Logf("converted docs: attrs=%#x tag=%#x", converted.FileAttributes, converted.Reserved0)
	// docs 已执行 RestoreVerifiedDirectory；Windows 可以在连接/枚举/投影后
	// 给它附上 Cloud tag，不能再拿它证明“普通目录”的默认保护。
	// 使用从未注册、连接或转换的独立根，并直接检查真实 tag。
	ordinaryRoot := trustedDirectoryTemp(t)
	ordinaryDocs := filepath.Join(ordinaryRoot, "docs")
	if e = os.Mkdir(ordinaryDocs, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(ordinaryRoot, "unknown"), 0700); e != nil {
		t.Fatal(e)
	}
	ordinaryLocal := filepath.Join(ordinaryDocs, "local.bin")
	if e = os.WriteFile(ordinaryLocal, payload, 0600); e != nil {
		t.Fatal(e)
	}
	assertOrdinary := func() {
		t.Helper()
		u, err := windows.UTF16PtrFromString(ordinaryDocs)
		if err != nil {
			t.Fatal(err)
		}
		var data windows.Win32finddata
		h, err := windows.FindFirstFile(u, &data)
		if err != nil {
			t.Fatal(err)
		}
		windows.FindClose(h)
		if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || data.Reserved0 != 0 {
			t.Fatalf("ordinary fixture converted: attrs=%#x tag=%#x", data.FileAttributes, data.Reserved0)
		}
		t.Logf("ordinary fixture: attrs=%#x tag=%#x", data.FileAttributes, data.Reserved0)
	}
	assertOrdinary()
	cfg.RootPath = ordinaryRoot
	cfg.TrustedDirectory = nil
	cfg.StateDir = filepath.Join(state, "default-writeback")
	legacy, e := NewWritebackEngine(cfg)
	if e != nil {
		t.Fatal(e)
	}
	out, e = legacy.Reconcile(context.Background(), func(ctx context.Context, protected []string) error {
		rows, err := client.Objects(ctx, "test", "personal")
		if err != nil {
			return err
		}
		stats := cfapi.BuildTreeProtected(ordinaryRoot, entries(rows, true), protected)
		if len(stats.Failures) != 0 {
			return &stats
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, p := range out.ProtectedPaths {
		found = found || p == "docs"
	}
	if !found {
		t.Fatal("default ordinary-directory protection changed")
	}
	assertOrdinary()
	if _, e = os.Lstat(filepath.Join(ordinaryDocs, "missing.bin")); !os.IsNotExist(e) {
		t.Fatalf("unauthorized ordinary directory received remote child: %v", e)
	}
	got, e = os.ReadFile(ordinaryLocal)
	if e != nil || !bytes.Equal(got, payload) {
		t.Fatalf("ordinary local bytes changed: %v", e)
	}
}
