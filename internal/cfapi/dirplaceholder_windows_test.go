//go:build windows

package cfapi

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCreateDirectoryPlaceholder 探明一个必须先确定的语义：
// **CfCreatePlaceholders 能否把「目录」建成占位符**，还是要求父目录预先存在。
//
// 这决定了编排层怎么写：若能，则整个树（目录 + 文件）可一次批量建；
// 若不能，则要先 os.MkdirAll 建真目录，只有文件用占位符。
//
// WPS 的做法暗示「能」——它给目录也设了 FileAttributes=DIRECTORY 与
// DISABLE_ON_DEMAND_POPULATION（取证 §3.3b）。但那是它对**已存在目录**改属性，
// 还是凭空建目录占位符，其证据不足以区分。所以这里实测。
func TestCreateDirectoryPlaceholder(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	sr, err := NewSyncRoot(testProvider, []byte("easyshare-syncroot-dirph"), []byte("esf:"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterSyncRoot(root, &sr.Registration, &sr.Policies, RegisterFlagNone); err != nil {
		t.Fatalf("RegisterSyncRoot 失败: %v", err)
	}
	defer func() { _ = UnregisterSyncRoot(root) }()

	// 先试：凭空建一个目录占位符（父目录 = 同步根，已存在）
	ph := NewPlaceholders([]PlaceholderSpec{{
		RelativeName: "subdir",
		IsDir:        true,
		Size:         0,
		ModifiedUnix: 1767225600,
		Identity:     []byte("esf:dircode"),
	}})
	processed, err := CreatePlaceholders(root, ph.Infos, CreateFlagNone)
	if err != nil {
		t.Fatalf("CreatePlaceholders 调用失败: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d，期望 1", processed)
	}
	r := ph.Infos[0].Result
	t.Logf("目录占位符 Result = 0x%08x", uint32(r))

	if r != 0 {
		t.Fatalf("无法创建目录占位符（Result=0x%08x）——编排层必须改为先 os.MkdirAll", uint32(r))
	}
	fi, err := os.Stat(filepath.Join(root, "subdir"))
	if err != nil {
		t.Fatalf("目录占位符未落盘: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("subdir 不是目录")
	}
	t.Log("目录占位符创建成功")

	// ── 关键实测：RelativeFileName **只能是单个名字，不能带路径分隔符** ──
	//
	// 实测：传 "subdir/inside.txt" 会整体失败并返回 0x8007007B
	// （HRESULT_FROM_WIN32(ERROR_INVALID_NAME)）。
	// 语义是：CfCreatePlaceholders 的 rootPath 决定父目录，
	// RelativeFileName 只给出该父目录下的**一个名字**。
	// 因此**建树必须逐层进行**：先建父目录占位符，再以父目录为 rootPath 建子项。
	bad := NewPlaceholders([]PlaceholderSpec{{
		RelativeName: "subdir/inside.txt", // 带分隔符 → 非法
		Size:         42,
		Identity:     []byte("esf:filecode"),
	}})
	_, err = CreatePlaceholders(root, bad.Infos, CreateFlagNone)
	if err == nil {
		t.Fatal("传带路径分隔符的 RelativeFileName 竟然成功了——与实测不符，需重新确认语义")
	}
	t.Logf("带分隔符的名字被拒（符合预期）: %v", err)

	// 正确做法：以子目录为 rootPath，只给单名
	ph2 := NewPlaceholders([]PlaceholderSpec{{
		RelativeName: "inside.txt",
		Size:         42,
		ModifiedUnix: 1767225600,
		Identity:     []byte("esf:filecode"),
	}})
	sub := filepath.Join(root, "subdir")
	if _, err := CreatePlaceholders(sub, ph2.Infos, CreateFlagNone); err != nil {
		t.Fatalf("以子目录为 rootPath 建文件占位符失败: %v", err)
	}
	if r2 := ph2.Infos[0].Result; r2 != 0 {
		t.Fatalf("子目录内建文件占位符失败 Result=0x%08x", uint32(r2))
	}
	if fi, err := os.Stat(filepath.Join(sub, "inside.txt")); err != nil {
		t.Fatalf("子文件未落盘: %v", err)
	} else if fi.Size() != 42 {
		t.Fatalf("子文件大小 = %d，期望 42", fi.Size())
	}
	t.Log("逐层创建成功：以子目录为 rootPath + 单名 RelativeFileName")
}
