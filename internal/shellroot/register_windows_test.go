//go:build windows

package shellroot

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRegisterIsolatedShellRoot(t *testing.T) {
	if os.Getenv("EASYSHARE_SHELL_ROOT_TEST") != "1" {
		t.Skip("需显式隔离Shell根验证")
	}
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	sid, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("EasyShareTest!%s!go-%d", sid.User.Sid.String(), time.Now().UnixNano())
	if err := Register(root, id, "Shell根验证", "%SystemRoot%\\system32\\imageres.dll,-1040", "1.0", []byte("isolated-root-context")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := UnregisterIsolated(id); err != nil {
			t.Error(err)
		}
	})
	if err := SuppressGeneratedNavigation(id, root); err != nil {
		t.Fatal(err)
	}
	t.Log("ISOLATED_WINRT_ROOT_REGISTERED")
}
