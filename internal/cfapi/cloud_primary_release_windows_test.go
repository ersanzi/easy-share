//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// 真正读取/释放测试：只用临时根与固定内存服务端，不修改生产同步根。
func TestCloudPrimaryConvertReleaseRoundTrip(t *testing.T) {
	root := directoryRestoreTempRoot(t)
	payload := bytes.Repeat([]byte("verified-cloud-content-"), 65536)
	m, err := mountSpaceForTest(t, MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte("release-rd"), Policies: &SyncPolicies{Hydration: HydrationPolicy{Primary: 1}, Population: PopulationPolicy{Primary: 3}}, ProtectedPaths: []string{"sample.bin"}, List: func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{Path: "sample.bin", Size: int64(len(payload)), Identity: EncodeIdentity(9981)}}, nil
	}, Open: func(_ context.Context, _ string, offset, n int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload[offset : offset+n])), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	path := filepath.Join(root, "sample.bin")
	if err = os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	before := allocatedSize(t, path)
	// 独占临时文件，在相同句柄下核实所有字节后再转；不是仅凭上传状态置同步。
	u, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(u, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(h), path)
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(got, payload) {
		f.Close()
		t.Fatal("locked verification failed")
	}
	var beforeID windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &beforeID); err != nil {
		f.Close()
		t.Fatal(err)
	}
	convert, err := proc("CfConvertToPlaceholder")
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	id := EncodeIdentity(9981)
	var usn int64
	hr, _, _ := convert.Call(uintptr(h), uintptr(unsafe.Pointer(&id[0])), uintptr(len(id)), 3, uintptr(unsafe.Pointer(&usn)), 0)
	runtime.KeepAlive(id)
	if err = checkHResult(hr, "CfConvertToPlaceholder locked mark+dehydrate"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	after := allocatedSize(t, path)
	t.Logf("converted verified ordinary file allocation %d -> %d", before, after)
	if after != 0 {
		t.Fatal("not cloud-only after conversion")
	}
	// 读真实placeholder identity，而非仅把稀疏零文件当成云占位符。
	ph, err := OpenPlaceholderHandle(path)
	if err != nil {
		t.Fatal(err)
	}
	infoProc, _ := proc("CfGetPlaceholderInfo")
	raw := make([]byte, 8192)
	var used uint32
	hr, _, _ = infoProc.Call(uintptr(ph), 1, uintptr(unsafe.Pointer(&raw[0])), uintptr(len(raw)), uintptr(unsafe.Pointer(&used)))
	syscall.CloseHandle(ph)
	if hr != 0 || used < 60 {
		t.Fatalf("placeholder info hr=%x bytes=%d", hr, used)
	}
	n := binary.LittleEndian.Uint32(raw[56:60])
	if n > used-60 || !bytes.Equal(raw[60:60+n], id) {
		t.Fatal("placeholder identity differs")
	}
	if binary.LittleEndian.Uint64(raw[:8]) != 0 || binary.LittleEndian.Uint32(raw[36:40]) != 1 {
		t.Fatal("dehydrated/synced state not proved")
	}
	recovered, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(recovered, payload) {
		t.Fatalf("read after release: %v", err)
	}
	// 再释放已水合占位符，保留数据身份；设置inSync之前仍须持锁校验。
	h, err = windows.CreateFile(u, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if err != nil {
		t.Fatal(err)
	}
	f = os.NewFile(uintptr(h), path)
	defer f.Close()
	recovered, err = io.ReadAll(f)
	if err != nil || !bytes.Equal(recovered, payload) {
		t.Fatal("second locked verification failed")
	}
	syncProc, _ := proc("CfSetInSyncState")
	hr, _, _ = syncProc.Call(uintptr(h), 1, 0, 0)
	if hr != 0 {
		t.Fatal(checkHResult(hr, "mark locked verified file"))
	}
	dehydrate, _ := proc("CfDehydratePlaceholder")
	hr, _, _ = dehydrate.Call(uintptr(h), 0, ^uintptr(0), 0, 0)
	if err = checkHResult(hr, "CfDehydratePlaceholder"); err != nil {
		t.Fatal(err)
	}
	var afterID windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &afterID); err != nil {
		t.Fatal(err)
	}
	if beforeID.FileIndexHigh != afterID.FileIndexHigh || beforeID.FileIndexLow != afterID.FileIndexLow {
		t.Fatal("NTFS identity changed")
	}
	f.Close()
	if size := allocatedSize(t, path); size != 0 {
		t.Fatal(fmt.Sprintf("second release allocation=%d", size))
	}
	recovered, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(recovered, payload) {
		t.Fatalf("second release read: %v", err)
	}
	t.Log("RELEASE_ROUNDTRIP_REAL_PASS: verified bytes, identity preserved, disk0 twice, hydrated bytes exact")
}
