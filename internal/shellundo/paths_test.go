package shellundo

import (
	"errors"
	"testing"
)

func TestLocalPathRejectsBeforeNormalization(t *testing.T) {
	for _, path := range []string{"", `relative`, `C:relative`, `\\host\share\a`, `\\?\C:\root\a`, `\\.\C:\root\a`, `C:\root\..\other`, `C:\root\.\a`, `C:\root\\a`, `C:\root\a:stream`, `C:\root\a.`, `C:\root\a `, `C:\root\CON.txt`, "C:\\root\\a\x00", `C:\root\*.txt`} {
		if normal, err := normalizeLocalPath(path); err == nil {
			t.Fatalf("误清理/放行 %q -> %q", path, normal)
		}
	}
	for _, test := range []struct{ in, want string }{{`c:/Easy Share/a.txt`, `C:\Easy Share\a.txt`}, {`C:\root\`, `C:\root`}, {`C:\`, `C:\`}} {
		got, err := normalizeLocalPath(test.in)
		if err != nil || got != test.want {
			t.Fatalf("%q -> %q / %v", test.in, got, err)
		}
	}
}

func TestSelectionExactRootBoundary(t *testing.T) {
	root := `C:\EasyShare\cloud`
	for _, path := range []string{root, root + `-other\a`, `C:\other\a`, `D:\EasyShare\cloud\a`} {
		if _, err := selectionInRoot(root, []string{path}); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("必须拒绝当前云根外 %q: %v", path, err)
		}
	}
	if _, err := selectionInRoot(root, []string{`c:\easyshare\CLOUD\目录\file.txt`}); err != nil {
		t.Fatal(err)
	}
}

func TestSelectionRejectsDuplicatesAndOverlappingDirectories(t *testing.T) {
	for _, paths := range [][]string{nil, {`C:\root\a`, `c:\ROOT\A`}, {`C:\root\dir`, `C:\root\dir\a`}} {
		if _, err := normalizeSelection(paths); err == nil {
			t.Fatalf("应拒绝选择 %q", paths)
		}
	}
	if _, err := normalizeSelection([]string{`C:\root\dir`, `C:\root\directory\a`}); err != nil {
		t.Fatal(err)
	}
}
