//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 仅在临时根比较策略；不改变生产根。模拟网络每个 MiB 延迟固定，用于测首读是否等待整份。
func TestCloudPrimaryHydrationPolicies(t *testing.T) {
	for _, tc := range []struct {
		name              string
		primary, modifier uint16
	}{
		{"full", HydrationPolicyFull, 0},
		{"progressive", HydrationPolicyProgressive, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			payload := bytes.Repeat([]byte("0123456789ABCDEF"), 2<<20) // 32 MiB
			var mu sync.Mutex
			var requests, requestedBytes int64
			policy := SyncPolicies{Hydration: HydrationPolicy{Primary: tc.primary, Modifier: tc.modifier}, Population: PopulationPolicy{Primary: PopulationPolicyAlwaysFull}}
			m, err := mountSpaceForTest(t, MountOptions{
				RootPath: root, Provider: testProvider, SyncRootIdentity: []byte("cloud-primary-rd-" + tc.name), ProtectedPaths: []string{}, Policies: &policy,
				List: func(context.Context) ([]CloudEntry, error) {
					return []CloudEntry{{Path: "sample.bin", Size: int64(len(payload)), Identity: EncodeIdentity(9701)}}, nil
				},
				Open: func(ctx context.Context, _ string, off, length int64) (io.ReadCloser, error) {
					mu.Lock()
					requests++
					requestedBytes += length
					mu.Unlock()
					wait := time.Duration(30+length/(1<<20)*15) * time.Millisecond
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(wait):
					}
					if off < 0 || length <= 0 || off+length > int64(len(payload)) {
						return nil, io.ErrUnexpectedEOF
					}
					return io.NopCloser(bytes.NewReader(payload[off : off+length])), nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			actualPolicy, registered, queryErr := QuerySyncRootStandardInfo(root)
			if queryErr != nil || !registered || actualPolicy.Hydration != policy.Hydration {
				t.Fatalf("registered policy differs: %+v %v", actualPolicy.Hydration, queryErr)
			}
			m.SetFetchObserver(func(r FetchRequest) {
				t.Logf("FETCH %s required=%d+%d optional=%d+%d size=%d process=%s", tc.name, r.RequiredOffset, r.RequiredLength, r.OptionalOffset, r.OptionalLength, r.FileSize, r.ProcessName)
			})
			path := filepath.Join(root, "sample.bin")
			if size := allocatedSize(t, path); size != 0 {
				t.Fatalf("unread allocated=%d", size)
			}
			started := time.Now()
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 4096)
			if _, err = f.ReadAt(buf, 0); err != nil {
				f.Close()
				t.Fatal(err)
			}
			first := time.Since(started)
			if !bytes.Equal(buf, payload[:len(buf)]) {
				f.Close()
				t.Fatal("head differs")
			}
			mu.Lock()
			firstBytes := requestedBytes
			firstCalls := requests
			mu.Unlock()
			for _, off := range []int64{int64(len(payload)) - 4096, 7 << 20, 17 << 20} {
				if _, err = f.ReadAt(buf, off); err != nil {
					f.Close()
					t.Fatal(err)
				}
				if !bytes.Equal(buf, payload[off:off+int64(len(buf))]) {
					f.Close()
					t.Fatal("random read differs")
				}
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(120 * time.Millisecond)
			mu.Lock()
			calls, total := requests, requestedBytes
			mu.Unlock()
			t.Logf("POLICY %s firstRead=%v firstBytes=%d firstRequests=%d totalRequested=%d totalRequests=%d allocatedAfterClose=%d logical=%d", tc.name, first, firstBytes, firstCalls, total, calls, allocatedSize(t, path), len(payload))
		})
	}
}
