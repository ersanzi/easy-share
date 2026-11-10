//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 少量文件和不同共享方式的独立 .NET 消费者对照，不修改或读取生产根。
func TestCloudPrimaryDotNetOpenMatrix(t *testing.T) {
	for _, mode := range []string{"normal", "cloud-full", "cloud-progressive", "cloud-progressive-synced"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "document.txt")
			content := bytes.Repeat([]byte("document\r\n"), 512)
			var fetched atomic.Int64
			if mode == "normal" {
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				policies := SyncPolicies{Hydration: HydrationPolicy{Primary: 1}, Population: PopulationPolicy{Primary: 3}}
				if mode == "cloud-full" {
					policies.Hydration = HydrationPolicy{Primary: 2}
				}
				var protect []string
				if mode != "cloud-progressive-synced" {
					protect = []string{}
				}
				m, err := mountSpaceForTest(t, MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte(mode), Policies: &policies, ProtectedPaths: protect, List: func(context.Context) ([]CloudEntry, error) {
					return []CloudEntry{{Path: "document.txt", Size: int64(len(content)), Identity: EncodeIdentity(1121)}}, nil
				}, Open: func(ctx context.Context, p string, off, n int64) (io.ReadCloser, error) {
					fetched.Add(n)
					return io.NopCloser(bytes.NewReader(content[off : off+n])), nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				defer m.Close()
				if allocatedSize(t, path) != 0 || fetched.Load() != 0 {
					t.Fatal("external open fixture must be cloud-only and unread")
				}
				m.session.transferObserver = func(off, n int64, hr uintptr) { t.Logf("transfer %d+%d hr=%x", off, n, uint32(hr)) }
			}
			script := fmt.Sprintf(`$ErrorActionPreference='Stop';Write-Output 'start';$f=[IO.File]::Open('%s',[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]7);Write-Output 'opened';$b=New-Object byte[] 20;$n=$f.Read($b,0,20);$f.Dispose();Write-Output ('bytes='+$n);Write-Output ([Convert]::ToBase64String($b));`, strings.ReplaceAll(path, "'", "''"))
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
			if err != nil {
				t.Fatalf("consumer %s: %v context=%v output=%s", mode, err, ctx.Err(), output)
			}
			if !bytes.Contains(output, []byte("bytes=20")) || !bytes.Contains(output, []byte(base64.StdEncoding.EncodeToString(content[:20]))) || (mode != "normal" && fetched.Load() == 0) {
				t.Fatalf("consumer did not return exact cloud bytes: %s", output)
			}
			t.Logf("mode=%s output=%s", mode, output)
		})
	}
}

func TestMountSpaceRejectsStreamingBeforeRegistration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "untouched.txt")
	content := []byte("must not touch existing bytes")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	m, err := MountSpace(MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte("rejected-streaming"), Policies: &SyncPolicies{Hydration: HydrationPolicy{Primary: HydrationPolicyProgressive, Modifier: 2}, Population: PopulationPolicy{Primary: PopulationPolicyAlwaysFull}}, List: func(context.Context) ([]CloudEntry, error) {
		called = true
		return nil, nil
	}, Open: func(context.Context, string, int64, int64) (io.ReadCloser, error) {
		called = true
		return nil, io.EOF
	}})
	if err == nil || m != nil || called || !strings.Contains(err.Error(), "STREAMING") {
		t.Fatalf("unsupported streaming admitted: mount=%v called=%t error=%v", m != nil, called, err)
	}
	if _, registered, err := QuerySyncRootStandardInfo(root); err != nil || registered {
		t.Fatalf("unsupported policy registered root: %t %v", registered, err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("unsupported policy touched user bytes: %v", err)
	}
}
