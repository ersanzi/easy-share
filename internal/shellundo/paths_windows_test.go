//go:build windows

package shellundo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePathsChecksActualRootAndReparseAncestors(t *testing.T) {
	root := canonicalTestDirectory(t)
	selected := filepath.Join(root, "normal.txt")
	if err := os.WriteFile(selected, []byte("validation only"), 0600); err != nil {
		t.Fatal(err)
	}
	if paths, err := ValidatePaths(root, []string{selected}); err != nil || len(paths) != 1 || paths[0] != selected {
		t.Fatalf("正常NTFS路径: %q/%v", paths, err)
	}
	if _, err := ValidatePaths(root, []string{root}); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("禁止删当前云根自身: %v", err)
	}
	if _, err := ValidatePaths(root, []string{filepath.Join(root, "missing.txt")}); err == nil {
		t.Fatal("未确认存在的 path 不能执行")
	}
	outside := canonicalTestDirectory(t)
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "redirect")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("当前测试用户无法创建 symlink，正常/root校验已跑: %v", err)
	}
	if _, err := ValidatePaths(root, []string{filepath.Join(link, "keep.txt")}); err == nil {
		t.Fatal("父路径 symlink 不得跳出云根")
	}
	if _, err := ValidatePaths(root, []string{link}); err == nil {
		t.Fatal("symlink 本身也不是授权 cloud reparse")
	}
}
