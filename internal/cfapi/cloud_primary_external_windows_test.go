//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// 独立消费者不是持有 CfConnectSyncRoot 的提供方进程，贴近 Explorer/编辑器打开云盘。
func TestCloudPrimaryExternalConsumer(t *testing.T) {
	for _, test := range []struct {
		name              string
		primary, modifier uint16
	}{
		{"full-external", 2, 0}, {"progressive-external", 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			content := make([]byte, 32<<20)
			for i := range content {
				content[i] = byte((i*13 + (i >> 9)) % 251)
			}
			var requested atomic.Int64
			policy := SyncPolicies{Hydration: HydrationPolicy{Primary: test.primary, Modifier: test.modifier}, Population: PopulationPolicy{Primary: 3}}
			m, err := mountSpaceForTest(t, MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte(test.name), Policies: &policy, ProtectedPaths: []string{}, List: func(context.Context) ([]CloudEntry, error) {
				return []CloudEntry{{Path: "external.bin", Size: int64(len(content)), Identity: EncodeIdentity(9870)}}, nil
			}, Open: func(ctx context.Context, path string, off, n int64) (io.ReadCloser, error) {
				if off < 0 || n < 0 || off+n > int64(len(content)) {
					return nil, fmt.Errorf("out of range")
				}
				requested.Add(n)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(45 * time.Millisecond):
				}
				return io.NopCloser(bytes.NewReader(content[off : off+n])), nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			m.SetFetchObserver(func(r FetchRequest) {
				t.Logf("consumer=%s required=%d+%d optional=%d+%d", r.ProcessName, r.RequiredOffset, r.RequiredLength, r.OptionalOffset, r.OptionalLength)
			})
			path := filepath.Join(root, "external.bin")
			ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			python := `import sys,time,json,hashlib,ctypes
nt=ctypes.WinDLL('ntdll');get=nt.RtlQueryProcessPlaceholderCompatibilityMode;get.restype=ctypes.c_byte
before=get()
if len(sys.argv)>2:
 setmode=nt.RtlSetProcessPlaceholderCompatibilityMode;setmode.argtypes=[ctypes.c_byte];setmode.restype=ctypes.c_byte
 result=setmode(int(sys.argv[2]));assert result>=0
p=sys.argv[1];t=time.perf_counter();f=open(p,'rb',buffering=0);opened=time.perf_counter()-t;b=f.read(4096);first=time.perf_counter()-t;f.close();print(json.dumps({'modeBefore':before,'modeAfter':get(),'openSeconds':opened,'firstSeconds':first,'bytes':len(b),'headSHA256':hashlib.sha256(b).hexdigest()}))`
			arguments := []string{"-c", python, path}
			output, err := exec.CommandContext(ctx, "python", arguments...).CombinedOutput()
			if err != nil {
				t.Fatalf("external consumer failed: %v %s", err, output)
			}
			var result map[string]any
			if err = json.Unmarshal(output, &result); err != nil {
				t.Fatal(err)
			}
			if result["bytes"] != float64(4096) {
				t.Fatalf("invalid read %s", output)
			}
			time.Sleep(200 * time.Millisecond)
			t.Logf("EXTERNAL %s result=%s requestedAfterClose=%d allocation=%d", test.name, bytes.TrimSpace(output), requested.Load(), allocatedSize(t, path))
		})
	}
}
