//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// 使用普通同步 CreateFile 的 Win32 读取，与 Go os.Open 所带共享/标志作隔离对照。
func TestCloudPrimaryNativeRead(t *testing.T) {
	for _, primary := range []uint16{HydrationPolicyFull, HydrationPolicyProgressive} {
		root := t.TempDir()
		payload := bytes.Repeat([]byte("abcdefgh"), 4<<20)
		var downloaded atomic.Int64
		policy := SyncPolicies{Hydration: HydrationPolicy{Primary: primary}, Population: PopulationPolicy{Primary: 3}}
		m, err := mountSpaceForTest(t, MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte("native-progressive"), Policies: &policy, ProtectedPaths: []string{}, List: func(context.Context) ([]CloudEntry, error) {
			return []CloudEntry{{Path: "data.bin", Size: int64(len(payload)), Identity: EncodeIdentity(9911)}}, nil
		}, Open: func(ctx context.Context, p string, off, n int64) (io.ReadCloser, error) {
			downloaded.Add(n)
			time.Sleep(30 * time.Millisecond)
			return io.NopCloser(bytes.NewReader(payload[off : off+n])), nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		m.SetFetchObserver(func(r FetchRequest) {
			t.Logf("native flags=%d required=%d+%d optional=%d+%d", primary, r.RequiredOffset, r.RequiredLength, r.OptionalOffset, r.OptionalLength)
		})
		p, _ := windows.UTF16PtrFromString(filepath.Join(root, "data.bin"))
		started := time.Now()
		h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		var n uint32
		b := make([]byte, 4096)
		if err = windows.ReadFile(h, b, &n, nil); err != nil {
			windows.CloseHandle(h)
			t.Fatal(err)
		}
		if n != 4096 || !bytes.Equal(b, payload[:4096]) {
			t.Fatal("native read mismatch")
		}
		t.Logf("native primary=%d first=%v downloaded=%d", primary, time.Since(started), downloaded.Load())
		windows.CloseHandle(h)
		time.Sleep(150 * time.Millisecond)
		t.Logf("native primary=%d allocated=%d", primary, allocatedSize(t, filepath.Join(root, "data.bin")))
		if err = m.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloudPrimaryRawCallbackSnapshot(t *testing.T) {
	root := t.TempDir()
	sr, err := NewSyncRoot(testProvider, []byte("raw-callback-rd"), nil, &SyncPolicies{Hydration: HydrationPolicy{Primary: 1}, Population: PopulationPolicy{Primary: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err = RegisterSyncRoot(root, &sr.Registration, &sr.Policies, 2); err != nil {
		t.Fatal(err)
	}
	defer UnregisterSyncRoot(root)
	data := bytes.Repeat([]byte{1}, 32<<20)
	sess, err := Connect(root, func(r FetchRequest) FetchResult {
		t.Logf("raw observed request=%d+%d", r.RequiredOffset, r.RequiredLength)
		return FetchResult{Data: data, Offset: 0}
	}, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.parameterObserver = func(raw []byte) { t.Logf("raw callback bytes=%x length=%d", raw, len(raw)) }
	ph := NewPlaceholders([]PlaceholderSpec{{RelativeName: "x", Size: int64(len(data)), Identity: EncodeIdentity(99), NoMarkInSync: true}})
	if _, err = CreatePlaceholders(root, ph.Infos, 0); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, "x"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var b [1]byte
	if _, err = f.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	_ = unsafe.Sizeof(b)
}
