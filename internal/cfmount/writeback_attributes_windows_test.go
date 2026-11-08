//go:build windows

package cfmount

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWritebackSkipsRecallAttributes(t *testing.T) {
	e, f, c := wbSetup(t)
	wbWrite(t, c, "offline", "do not hydrate")
	p := filepath.Join(c.RootPath, "offline")
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := windows.GetFileAttributes(u)
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetFileAttributes(u, a|windows.FILE_ATTRIBUTE_OFFLINE); err != nil {
		t.Fatal(err)
	}
	defer windows.SetFileAttributes(u, a)
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	readable, err := writebackReadable(p, info)
	if err != nil || readable {
		t.Fatalf("OFFLINE 被当作可读: %v %v", readable, err)
	}
	r := wbStable(t, e)
	if f.uploads != 0 || len(r.Issues) == 0 {
		t.Fatalf("扫描读取了 OFFLINE 文件: %+v", r)
	}
}
