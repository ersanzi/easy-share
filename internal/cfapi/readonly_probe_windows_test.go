//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// mountReadOnlyProbe 挂一个单文件同步根，文件带 FILE_ATTRIBUTE_READONLY。
func mountReadOnlyProbe(t *testing.T) (string, func()) {
	t.Helper()
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	content := []byte("original cloud content")

	list := func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path: "ro.txt", Size: int64(len(content)),
			ModifiedUnix: 1767225600, Identity: EncodeIdentity(7777),
			ReadOnly: true,
		}}, nil
	}
	open := func(_ context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
		end := offset + length
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		if offset >= int64(len(content)) {
			return io.NopCloser(strings.NewReader("")), nil
		}
		return io.NopCloser(bytes.NewReader(content[offset:end])), nil
	}

	m, err := MountSpace(MountOptions{
		RootPath: root, Provider: testProvider, List: list, Open: open,
		SyncRootIdentity: []byte("easyshare-ro-probe"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	return root, func() { _ = m.Close() }
}

// TestReadOnlyPlaceholderBlocksWrite 验证只读属性真的挡住了写入。
//
// 这是只读原型的**前提**：cfapi 没有写回调，用户编辑不会被上报；
// 若文件本身又可写，用户就会得到「保存成功但下次同步丢失」的结果。
// 打上 FILE_ATTRIBUTE_READONLY 后应当得到明确的拒绝访问。
func TestReadOnlyPlaceholderBlocksWrite(t *testing.T) {
	root, cleanup := mountReadOnlyProbe(t)
	defer cleanup()

	full := filepath.Join(root, "ro.txt")
	if err := os.WriteFile(full, []byte("USER EDITED THIS"), 0o644); err == nil {
		t.Errorf("只读占位符仍可写入——用户改动会静默丢失（无写回调上报）。" +
			"必须让写入失败并给出「拒绝访问」")
	} else {
		t.Logf("写入被拒：%v（符合只读原型预期）", err)
	}
}

// TestReadOnlyPlaceholderStillHydrates 验证只读**不影响按需读取**。
//
// 这条同样关键：只读位如果在文件系统层阻断了 CfApi 的写回填（hydration 本质是
// 内核往这个文件写数据），那「点开才拉」就废了——为了防写把读也一起弄坏。
// 必须实测确认读仍然正常。
func TestReadOnlyPlaceholderStillHydrates(t *testing.T) {
	root, cleanup := mountReadOnlyProbe(t)
	defer cleanup()

	full := filepath.Join(root, "ro.txt")
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("只读占位符读取失败——只读位可能阻断了 hydration: %v", err)
	}
	if string(got) != "original cloud content" {
		t.Fatalf("内容 = %q，期望 %q", got, "original cloud content")
	}
	t.Logf("只读占位符按需读取正常：读回 %d 字节", len(got))
}

// TestReadOnlyPlaceholderIsMarkedReadOnly 确认属性真的落到了文件上。
func TestReadOnlyPlaceholderIsMarkedReadOnly(t *testing.T) {
	root, cleanup := mountReadOnlyProbe(t)
	defer cleanup()

	full := filepath.Join(root, "ro.txt")
	// 用 Win32 GetFileAttributes 直接读原始属性位：
	// os.Stat 的 Mode 在 Windows 上不直接暴露 FILE_ATTRIBUTE_*，看不到只读位。
	ptr, err := windows.UTF16PtrFromString(full)
	if err != nil {
		t.Fatalf("路径转换失败: %v", err)
	}
	attrs, err := windows.GetFileAttributes(ptr)
	if err != nil {
		t.Fatalf("读属性失败: %v", err)
	}
	if attrs&FileAttributeReadOnly == 0 {
		t.Errorf("FILE_ATTRIBUTE_READONLY 未生效（attrs=0x%X）", attrs)
	} else {
		t.Logf("FILE_ATTRIBUTE_READONLY 已生效（attrs=0x%X）", attrs)
	}
}
