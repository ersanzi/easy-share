//go:build windows

// 临时诊断：空目录持久化（切片 4 第 3 项）跨进程验证。
//
// 用法：
//   probe.exe write  → 建两个空目录并退出
//   probe.exe read   → 新进程读，应仍看到它们
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"easyshare/internal/account"
	"easyshare/internal/drive"
	"easyshare/internal/spacedav"
)

func main() {
	mode := "write"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	dataRoot := filepath.Join(os.Getenv("LOCALAPPDATA"), "easyshare")
	dirFile := filepath.Join(dataRoot, "probe-dirs.json")

	base := "http://127.0.0.1:8090"
	session, err := account.New(base).Login(context.Background(), "admin", "admin123")
	if err != nil {
		fmt.Println("登录失败:", err)
		os.Exit(1)
	}
	token := session.Token
	fs := spacedav.New(spacedav.Options{
		Client:   drive.New(base),
		Space:    drive.SpacePersonal,
		Token:    func() string { return token },
		DirStore: spacedav.NewDirStore(dirFile),
	})

	ctx := context.Background()
	if mode == "write" {
		for _, name := range []string{"/probe-empty/alpha", "/probe-empty/beta"} {
			if err := fs.Mkdir(ctx, name, 0o755); err != nil {
				fmt.Println("Mkdir 失败:", name, err)
				os.Exit(1)
			}
		}
		fmt.Println("① 已建 2 个空目录并落盘")
		return
	}

	// read：新进程，内存 pendingDirs 是空的，能看到就说明走了持久化
	f, err := fs.OpenFile(ctx, "/probe-empty", os.O_RDONLY, 0)
	if err != nil {
		fmt.Println("列目录失败:", err)
		os.Exit(1)
	}
	entries, err := f.Readdir(-1)
	_ = f.Close()
	if err != nil {
		fmt.Println("Readdir 失败:", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	fmt.Printf("② 新进程看到 %d 项: %v\n", len(names), names)
	if len(names) == 2 {
		fmt.Println("   ✅ 空目录跨进程持久化成立")
	} else {
		fmt.Println("   ❌ 期望 2 项")
	}

	// 清理
	for _, name := range []string{"/probe-empty/alpha", "/probe-empty/beta", "/probe-empty"} {
		_ = fs.RemoveAll(ctx, name)
	}
	_ = os.Remove(dirFile)
	fmt.Println("③ 已清理")
}
