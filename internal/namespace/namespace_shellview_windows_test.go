//go:build windows

package namespace

import (
	"crypto/rand"
	"fmt"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// This test owns a fresh, valid GUID only; no product keys or Shell notifications.
func TestShellViewOwnedRegistration(t *testing.T) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	guid := fmt.Sprintf("{%x-%x-%x-%x-%x}", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	base := `Software\Classes\CLSID\` + guid
	ns := `Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\` + guid
	for _, path := range []string{base, ns} {
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err == nil {
			key.Close()
			t.Fatalf("refuse existing key: %s", path)
		}
		if err != registry.ErrNotExist {
			t.Fatal(err)
		}
	}
	key, existing, err := registry.CreateKey(registry.CURRENT_USER, base, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	key.Close()
	if existing {
		t.Fatal("GUID collision: refusing cleanup")
	}
	t.Cleanup(func() {
		unregisterOne(guid)
		for _, path := range []string{base, ns} {
			key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
			if err == nil {
				key.Close()
				t.Errorf("owned key remains: %s", path)
			} else if err != registry.ErrNotExist {
				t.Error(err)
			}
		}
	})
	checkDWORD := func(sub, name string, want uint64, absent bool) {
		t.Helper()
		key, err := registry.OpenKey(registry.CURRENT_USER, base+sub, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		defer key.Close()
		got, typ, err := key.GetIntegerValue(name)
		if absent {
			if err != registry.ErrNotExist {
				t.Fatalf("%s must be absent: value=%x err=%v", name, got, err)
			}
		} else if err != nil || typ != registry.DWORD || got != want {
			t.Fatalf("%s: got=%x type=%d err=%v want=%x DWORD", name, got, typ, err, want)
		}
	}
	checkString := func(sub, name, want string, typ uint32) {
		t.Helper()
		key, err := registry.OpenKey(registry.CURRENT_USER, base+sub, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		defer key.Close()
		got, actualType, err := key.GetStringValue(name)
		if err != nil || actualType != typ || got != want {
			t.Fatalf("%s: got=%q type=%d err=%v want=%q type=%d", name, got, actualType, err, want, typ)
		}
	}
	entry := Entry{CLSID: guid, Name: "Isolated ShellView test", TargetPath: `C:\isolated\account-a`, UserID: "account-a", writableFilesystem: true}
	// Same-account idempotence, new account, legacy, and back to CFAPI.
	for _, mode := range []string{"cfapi", "cfapi", "account-b", "legacy", "cfapi"} {
		entry.writableFilesystem = mode != "legacy"
		if mode == "account-b" {
			entry.UserID = "account-b"
			entry.TargetPath = `C:\isolated\account-b`
		}
		if mode == "legacy" {
			entry.UserID = ""
			entry.TargetPath = WebDAVUNC(19082)
		}
		if mode == "cfapi" {
			entry.UserID = "account-a"
			entry.TargetPath = `C:\isolated\account-a`
		}
		if err := registerOne(entry); err != nil {
			t.Fatal(err)
		}
		checkDWORD(`\ShellFolder`, "Attributes", uint64(shellFolderAttributes(entry)), false)
		checkDWORD(`\ShellFolder`, "FolderValueFlags", 0x28, !entry.writableFilesystem)
		checkDWORD(`\Instance\InitPropertyBag`, "Attributes", 0x11, !entry.writableFilesystem)
		typ := uint32(registry.SZ)
		if entry.writableFilesystem {
			typ = registry.EXPAND_SZ
		}
		checkString(`\Instance\InitPropertyBag`, "TargetFolderPath", entry.TargetPath, typ)
		checkString(`\Instance`, "CLSID", shellFolder, registry.SZ)
		if entry.UserID != "" {
			checkString("", "UserId", entry.UserID, registry.SZ)
		} else {
			key, err := registry.OpenKey(registry.CURRENT_USER, base, registry.QUERY_VALUE)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = key.GetStringValue("UserId")
			key.Close()
			if err != registry.ErrNotExist {
				t.Fatalf("old account remains: %v", err)
			}
		}
	}
}
