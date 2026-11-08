//go:build windows

package cfapi

import (
	"bytes"
	"encoding/json"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

// Temp paths may contain ADMINI~1; API intentionally requires canonical paths.
func directoryRestoreTempRoot(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	h, err := restoreOpen(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	final, err := restoreFinalPath(h)
	if err != nil {
		t.Fatal(err)
	}
	return final
}

func directoryRestoreSource(t *testing.T, root, rel, state string) VerifiedDirectoryRestore {
	t.Helper()
	rootID, err := ReadDirectoryNTFSIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	dirID, err := ReadDirectoryNTFSIdentity(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return VerifiedDirectoryRestore{Namespace: "restore-regression-only", Root: root, RelativePath: rel,
		RemoteDirectoryFileID: "verified-remote-dir-id", PlaceholderIdentity: []byte("esf:verified-remote-dir-id"),
		ExpectedRoot: rootID, ExpectedDirectory: dirID, StateDirectory: state}
}

func TestDirectoryRestoreValidation(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "known"), 0700); err != nil {
		t.Fatal(err)
	}
	base := directoryRestoreSource(t, root, "known", state)
	cases := []struct {
		name   string
		change func(*VerifiedDirectoryRestore)
	}{
		{"traversal", func(s *VerifiedDirectoryRestore) { s.RelativePath = `..\outside` }},
		{"absolute", func(s *VerifiedDirectoryRestore) { s.RelativePath = `C:\outside` }},
		{"alternate-stream", func(s *VerifiedDirectoryRestore) { s.RelativePath = `known:stream` }},
		{"normalization-alias", func(s *VerifiedDirectoryRestore) { s.RelativePath = `known.` }},
		{"missing-provenance", func(s *VerifiedDirectoryRestore) { s.RemoteDirectoryFileID = "" }},
		{"missing-root-identity", func(s *VerifiedDirectoryRestore) { s.ExpectedRoot = DirectoryNTFSIdentity{} }},
		{"state-inside-root", func(s *VerifiedDirectoryRestore) { s.StateDirectory = root }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.change(&s)
			result, err := RestoreVerifiedDirectory(s)
			if err == nil || result.Converted {
				t.Fatalf("unsafe request accepted: %+v %v", result, err)
			}
		})
	}
}

func TestDirectoryRestoreRejectIdentityAndReparse(t *testing.T) {
	if !Available() {
		t.Skip("CFAPI unavailable")
	}
	root, state := directoryRestoreTempRoot(t), directoryRestoreTempRoot(t)
	dir := filepath.Join(root, "known")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := directoryRestoreSource(t, root, "known", state)
	source.ExpectedDirectory.FileID[0] ^= 0xff
	if result, err := RestoreVerifiedDirectory(source); err == nil || result.Converted {
		t.Fatalf("changed identity accepted: %+v %v", result, err)
	}
	source = directoryRestoreSource(t, root, "known", state)
	source.ExpectedRoot.FileID[0] ^= 0xff
	if result, err := RestoreVerifiedDirectory(source); err == nil || result.Converted {
		t.Fatalf("changed root accepted: %+v %v", result, err)
	}
	// A symlink is an unknown reparse tag. Never follow it, even when its NTFS
	// identity was explicitly supplied. The outside tree must remain untouched.
	outside := t.TempDir()
	payload := []byte("outside must survive")
	if err := os.WriteFile(filepath.Join(outside, "safe.bin"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation requires privilege: %v", err)
	}
	source = directoryRestoreSource(t, root, "link", state)
	if result, err := RestoreVerifiedDirectory(source); err == nil || result.Converted {
		t.Fatalf("unknown reparse accepted: %+v %v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(outside, "safe.bin"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("outside modified: %q %v", got, err)
	}
}

func TestDirectoryRestoreRealCFAPIMetadataOnly(t *testing.T) {
	if !Available() {
		t.Skip("CFAPI unavailable")
	}
	root, state := directoryRestoreTempRoot(t), directoryRestoreTempRoot(t)
	sr, err := NewSyncRoot(testProvider, []byte("directory-restore-isolated-test"), []byte("esf:"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = RegisterSyncRoot(root, &sr.Registration, &sr.Policies, RegisterFlagNone); err != nil {
		t.Fatal(err)
	}
	// Explicit unregister ONLY our disposable test root; no Mount.Close and no
	// production sync roots/processes are involved in this regression.
	defer func() {
		if err := UnregisterSyncRoot(root); err != nil {
			t.Errorf("test-root unregister: %v", err)
		}
	}()
	dir := filepath.Join(root, "known")
	if err = os.MkdirAll(filepath.Join(dir, "deep"), 0700); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte{0, 1, 2, 0xff}, bytes.Repeat([]byte("local-only-child"), 8192)...)
	child := filepath.Join(dir, "deep", "bytes.bin")
	if err = os.WriteFile(child, payload, 0600); err != nil {
		t.Fatal(err)
	}
	source := directoryRestoreSource(t, root, "known", state)
	source.AllowedChildren = []string{"not-the-actual-child"}
	if result, err := RestoreVerifiedDirectory(source); err == nil || result.Converted {
		t.Fatalf("allowlist mismatch accepted: %+v %v", result, err)
	}
	source.AllowedChildren = []string{"deep"}
	result, err := RestoreVerifiedDirectory(source)
	if err != nil {
		t.Fatalf("metadata conversion: %+v %v", result, err)
	}
	if !result.Converted || result.RecordPath == "" {
		t.Fatalf("missing provenance: %+v", result)
	}
	got, err := os.ReadFile(child)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("local child bytes changed: len=%d err=%v", len(got), err)
	}
	after, err := ReadDirectoryNTFSIdentity(dir)
	if err != nil || after != source.ExpectedDirectory {
		t.Fatalf("identity changed: %+v %v", after, err)
	}
	encoded, err := os.ReadFile(result.RecordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record directoryRestoreRecord
	if err = json.Unmarshal(encoded, &record); err != nil {
		t.Fatal(err)
	}
	if record.Version != 1 || record.Source.RemoteDirectoryFileID != source.RemoteDirectoryFileID || record.Actual != after || !bytes.Equal(record.Source.PlaceholderIdentity, source.PlaceholderIdentity) {
		t.Fatalf("invalid provenance: %+v", record)
	}
	again, err := RestoreVerifiedDirectory(source)
	if err == nil || again.Converted {
		t.Fatalf("repeat must fail closed: %+v %v", again, err)
	}
	still, err := os.ReadFile(result.RecordPath)
	if err != nil || !bytes.Equal(still, encoded) {
		t.Fatalf("provenance overwritten: %v", err)
	}
	got, err = os.ReadFile(child)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("repeat changed child: %v", err)
	}
	// A preexisting record is never overwritten and prevents conversion.
	secondDir := filepath.Join(root, "another")
	if err = os.Mkdir(secondDir, 0700); err != nil {
		t.Fatal(err)
	}
	second := directoryRestoreSource(t, root, "another", state)
	// Preflight failure (missing state) must not alter ordinary directory.
	second.StateDirectory = filepath.Join(state, "missing")
	if r, e := RestoreVerifiedDirectory(second); e == nil || r.Converted {
		t.Fatalf("persistence preflight accepted: %+v %v", r, e)
	}
	info, err := os.Lstat(secondDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("failed conversion destroyed directory: %v", err)
	}
}
