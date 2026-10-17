//go:build windows

// 临时诊断程序：用**真实控制面**数据跑一次 CfApi 挂载，验证切片 5 的
// 两条核心承诺——「列表零下载（占位符 0 字节）」与「点开才拉（FETCH_DATA）」。
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"easyshare/internal/account"
	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
)

func main() {
	base := "http://127.0.0.1:8090"
	ctx := context.Background()

	session, err := account.New(base).Login(ctx, "admin", "admin123")
	if err != nil {
		fmt.Println("登录失败:", err)
		os.Exit(1)
	}
	token := session.Token
	client := drive.New(base)

	root := filepath.Join(os.TempDir(), "es-cfapi-probe")
	_ = os.RemoveAll(root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		fmt.Println("建目录失败:", err)
		os.Exit(1)
	}

	if !cfapi.Available() {
		fmt.Println("cldapi.dll 不可用")
		os.Exit(1)
	}

	cfg := cfmount.Config{
		RootPath:     root,
		Space:        drive.SpacePersonal,
		Client:       client,
		Token:        func() string { return token },
		ProviderName: "EasyShare 网盘",
	}

	var opened []string
	mount, err := cfapi.MountSpace(cfapi.MountOptions{
		RootPath: root,
		Provider: cfapi.ProviderInfo{
			Name:    "EasyShare 网盘",
			Version: "1.0",
		},
		SyncRootIdentity: []byte("easyshare:probe"),
		List:             cfmount.List(cfg),
		Open: func(ctx context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
			opened = append(opened, fmt.Sprintf("%s[%d+%d]", cloudPath, offset, length))
			return cfmount.Open(cfg)(ctx, cloudPath, offset, length)
		},
	})
	if err != nil {
		fmt.Println("挂载失败:", err)
		os.Exit(1)
	}
	defer mount.Close()

	// ① 数树：文件数与总占用
	files, dirs, alloc := 0, 0, int64(0)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == root {
			return nil
		}
		if info.IsDir() {
			dirs++
			return nil
		}
		files++
		alloc += allocSize(path)
		return nil
	})
	fmt.Printf("① 建树：%d 目录 / %d 文件，磁盘占用合计 %d 字节\n", dirs, files, alloc)
	if alloc != 0 {
		fmt.Println("   ❌ 期望 0（占位符不该占磁盘）")
	} else {
		fmt.Println("   ✅ 列表零下载成立")
	}

	// ② 找一个最小的文件读它
	objs, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		fmt.Println("列举失败:", err)
		os.Exit(1)
	}
	var target drive.Object
	best := int64(1 << 40)
	for _, o := range objs {
		if o.Size > 0 && o.Size < best {
			best, target = o.Size, o
		}
	}
	if target.Path == "" {
		fmt.Println("② 没有可用文件")
		return
	}
	local := filepath.Join(root, filepath.FromSlash(target.Path))
	data, readErr := os.ReadFile(local)
	fmt.Printf("② 读 %s（%d 字节）→ 读回 %d 字节, err=%v\n", target.Path, target.Size, len(data), readErr)
	fmt.Printf("   触发取数次数=%d\n", len(opened))
	for _, o := range opened {
		fmt.Println("     ", o)
	}
	if readErr == nil && int64(len(data)) == target.Size {
		fmt.Println("   ✅ 点开才拉成立（内容长度与云端一致）")
	} else {
		fmt.Println("   ❌ 读取失败或长度不符")
	}

	// ③ 只拉了被读的那一个
	if len(opened) > 0 && !containsPrefix(opened, target.Path) {
		fmt.Println("   ❌ 取数路径与被读文件不符")
	}

	_ = mount.Close()
	_ = os.RemoveAll(root)
}

func containsPrefix(list []string, path string) bool {
	for _, s := range list {
		if len(s) >= len(path) && s[:len(path)] == path {
			return true
		}
	}
	return false
}

// allocSize 返回文件在磁盘上**实际分配**的字节数。
//
// 不用 os.Stat().Size()：那是逻辑大小（占位符报云端真实大小）。
// 要证明「占位符不占空间」，必须问文件系统实际分配了多少簇。
func allocSize(path string) int64 {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return -1
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getCompressedFileSize := kernel32.NewProc("GetCompressedFileSizeW")
	var high uint32
	low, _, errno := getCompressedFileSize.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&high)),
	)
	if uint32(low) == 0xFFFFFFFF && errno != syscall.Errno(0) {
		return -1
	}
	return (int64(high) << 32) | int64(uint32(low))
}
