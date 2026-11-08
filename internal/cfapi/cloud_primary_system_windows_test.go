//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// 系统自带 PowerShell 是另一种独立消费者；同时查询 placeholder 元数据而非只看逻辑大小。
func TestCloudPrimarySystemConsumer(t *testing.T) {
	root := t.TempDir()
	content := bytes.Repeat([]byte("abcdefgh"), 4<<20)
	var requested atomic.Int64
	policy := SyncPolicies{Hydration: HydrationPolicy{Primary: HydrationPolicyFull}, Population: PopulationPolicy{Primary: 3}}
	m, err := mountSpaceForTest(t, MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte("system-consumer"), Policies: &policy, ProtectedPaths: []string{}, List: func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{Path: "sample.bin", Size: int64(len(content)), Identity: EncodeIdentity(9781)}}, nil
	}, Open: func(ctx context.Context, p string, o, n int64) (io.ReadCloser, error) {
		if o < 0 || n <= 0 || o+n > int64(len(content)) {
			return nil, fmt.Errorf("bad range")
		}
		requested.Add(n)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(35 * time.Millisecond):
		}
		return io.NopCloser(bytes.NewReader(content[o : o+n])), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	// 使用实际异步分块回填；同步/流式原生标志对照另留在显式实验中。
	m.session.transferObserver = func(offset, length int64, hr uintptr) {
		t.Logf("transfer offset=%d length=%d result=0x%08x", offset, length, uint32(hr))
	}
	path := filepath.Join(root, "sample.bin")
	h, err := OpenPlaceholderHandle(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8192)
	var returned uint32
	query, err := proc("CfGetPlaceholderInfo")
	if err != nil {
		t.Fatal(err)
	}
	hr, _, _ := query.Call(uintptr(h), 1, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&returned)))
	syscall.CloseHandle(h)
	if err = checkHResult(hr, "CfGetPlaceholderInfo"); err != nil {
		t.Fatal(err)
	}
	t.Logf("before bytes=%d disk=%d validated=%d modified=%d pin=%d inSync=%d", returned, binary.LittleEndian.Uint64(buf[:8]), binary.LittleEndian.Uint64(buf[8:16]), binary.LittleEndian.Uint64(buf[16:24]), binary.LittleEndian.Uint32(buf[32:36]), binary.LittleEndian.Uint32(buf[36:40]))
	if returned < 60 || binary.LittleEndian.Uint64(buf[:8]) != 0 || requested.Load() != 0 || allocatedSize(t, path) != 0 {
		t.Fatal("system consumer precondition: expected unread cloud-only file")
	}
	m.SetFetchObserver(func(r FetchRequest) {
		t.Logf("SYSTEM consumer=%s range=%d+%d optional=%d+%d", r.ProcessName, r.RequiredOffset, r.RequiredLength, r.OptionalOffset, r.OptionalLength)
	})
	script := fmt.Sprintf(`$ErrorActionPreference='Stop';Write-Output 'consumer-start';$w=[Diagnostics.Stopwatch]::StartNew();$f=[IO.File]::Open('%s',[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]7);$open=$w.Elapsed.TotalMilliseconds;Write-Output ('opened='+$open);$b=New-Object byte[] 4096;$n=$f.Read($b,0,$b.Length);$first=$w.Elapsed.TotalMilliseconds;Write-Output ('read='+$n);$f.Dispose();@{openMs=$open;firstMs=$first;bytes=$n;data=[Convert]::ToBase64String($b)}|ConvertTo-Json -Compress`, strings.ReplaceAll(path, "'", "''"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("system consumer: %v context=%v requested=%d %s", err, ctx.Err(), requested.Load(), output)
	}
	if !bytes.Contains(output, []byte(base64.StdEncoding.EncodeToString(content[:4096]))) || requested.Load() == 0 {
		t.Fatal("system consumer did not return exact remote bytes")
	}
	time.Sleep(150 * time.Millisecond)
	t.Logf("SYSTEM exact first-read bytes=4096 requested=%d allocated=%d", requested.Load(), allocatedSize(t, path))
}
