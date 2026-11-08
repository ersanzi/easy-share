//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// 对照仅云端初始文件的同步标记；只在隔离根实验，绝不改变生产默认标记规则。
func TestCloudPrimaryInitialSyncState(t *testing.T) {
	for _, inSync := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmarked", true: "cloud-confirmed"}[inSync], func(t *testing.T) {
			root := t.TempDir()
			data := bytes.Repeat([]byte("0123456789ABCDEF"), 2<<20)
			var count atomic.Int64
			policies := SyncPolicies{Hydration: HydrationPolicy{Primary: 1}, Population: PopulationPolicy{Primary: 3}}
			var protected []string
			if !inSync {
				protected = []string{}
			}
			m, err := mountSpaceForTest(t, MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte("state-comparison"), Policies: &policies, ProtectedPaths: protected, List: func(context.Context) ([]CloudEntry, error) {
				return []CloudEntry{{Path: "data.bin", Size: int64(len(data)), Identity: EncodeIdentity(9751)}}, nil
			}, Open: func(ctx context.Context, p string, off, n int64) (io.ReadCloser, error) {
				count.Add(n)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(40 * time.Millisecond):
				}
				return io.NopCloser(bytes.NewReader(data[off : off+n])), nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			m.SetFetchObserver(func(r FetchRequest) {
				t.Logf("inSync=%v required=%d+%d optional=%d+%d", inSync, r.RequiredOffset, r.RequiredLength, r.OptionalOffset, r.OptionalLength)
			})
			path := filepath.Join(root, "data.bin")
			started := time.Now()
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			opened := time.Since(started)
			buf := make([]byte, 4096)
			_, err = f.Read(buf)
			if err != nil {
				f.Close()
				t.Fatal(err)
			}
			t.Logf("inSync=%v open=%v first=%v requested=%d", inSync, opened, time.Since(started), count.Load())
			f.Close()
			time.Sleep(150 * time.Millisecond)
			t.Logf("inSync=%v afterCloseRequested=%d allocated=%d", inSync, count.Load(), allocatedSize(t, path))
		})
	}
}
