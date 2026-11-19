//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"easyshare/internal/shellroot"
	"golang.org/x/sys/windows"
)

func TestOperationSDKLayout(t *testing.T) {
	if statusCloudFileUnsuccessful != 0xC000CF12 {
		t.Fatal("一般失败误写成只读卷状态")
	}
	if unsafe.Sizeof(operationAckParameters{}) != 16 || unsafe.Offsetof(operationAckParameters{}.CompletionStatus) != 12 {
		t.Fatal("SDK ACK union布局错误")
	}
	if unsafe.Offsetof(operationCallbackParameters{}.Flags) != 8 || unsafe.Offsetof(operationCallbackParameters{}.Path) != 16 {
		t.Fatal("SDK操作参数union布局错误")
	}
	for _, item := range []struct{ path, want string }{
		{`C:\cloud\file.txt`, "file.txt"}, {`\cloud\dir\file.txt`, "dir/file.txt"}, {`C:\elsewhere\file.txt`, ""},
		{`C:\cloud-copy\file.txt`, ""}, {`C:\cloud\..\file.txt`, ""}, {`file.txt`, ""}, {`C:\cloud`, ""},
	} {
		got, ok := RelativeOperationPath(`C:\cloud`, item.path)
		if got != item.want || ok != (item.want != "") {
			t.Fatalf("scope %q=%q/%t want %q", item.path, got, ok, item.want)
		}
	}
}

// 只记录系统事实。远端为独立fixture，不把此探针当生产同步已通过。
func TestWindowsExplorerUndoProbe(t *testing.T) {
	if os.Getenv("EASYSHARE_UNDO_PROBE") != "1" {
		t.Skip("真实Explorer撤销需显式启用")
	}
	if !Available() {
		t.Fatal("CFAPI不可用")
	}
	work, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	rd := filepath.Join(work, ".rd-test")
	if err := os.MkdirAll(rd, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(rd, "undo-probe-")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(run, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	content := map[string][]byte{"云端.txt": []byte("cloud committed identity fixture\n"), "目录/子项.txt": []byte("nested cloud committed fixture\n")}
	entries := []CloudEntry{
		{Path: "云端.txt", Identity: EncodeIdentity(880101), Version: 3, Size: int64(len(content["云端.txt"]))},
		{Path: "目录", IsDir: true, Identity: []byte("esd:undo-fixture")},
		{Path: "目录/子项.txt", Identity: EncodeIdentity(880102), Version: 2, Size: int64(len(content["目录/子项.txt"]))},
	}
	manifest := map[string]any{"run": run, "root": root, "items": []string{"未上传.txt", "云端.txt", "目录"}}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(run, "manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	provider := ProviderInfo{Name: "EasyShareUndoIsolatedProbe", Version: "test"}
	provider.ID, err = windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []OperationRequest
	var replies []map[string]any
	restores := make(chan OperationRequest, 16)
	record := func(req OperationRequest) {
		mu.Lock()
		events = append(events, req)
		mu.Unlock()
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	shellID := "EasyShareUndoIsolatedProbe!" + tokenUser.User.Sid.String() + "!" + filepath.Base(run)
	if err := shellroot.Register(root, shellID, "撤销测试", "%SystemRoot%/system32/imageres.dll,-1040", "test", []byte(filepath.Base(run))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := shellroot.UnregisterIsolated(shellID); err != nil {
			t.Errorf("清理隔离Shell根:%v", err)
		}
		cleanup := exec.Command("python", "-B", "-X", "utf8", filepath.Join(work, "..", "..", "tools", "maintenance", "cleanup-owned-undo-probes.py"), "--run", run)
		out, err := cleanup.CombinedOutput()
		_ = os.WriteFile(filepath.Join(run, "cleanup.log"), out, 0600)
		if err != nil {
			t.Errorf("精确测试根收尾失败:%v %s", err, out)
		}
	})
	callbacks := &OperationCallbacks{Before: func(_ context.Context, req OperationRequest) error {
		record(req)
		if os.Getenv("EASYSHARE_UNDO_PROBE_PREPARE_DELETE") == "1" && req.Kind == CallbackTypeNotifyDelete && req.Flags&OperationIsDirectory == 0 {
			rel, ok := RelativeOperationPath(root, req.Path)
			if !ok {
				return fmt.Errorf("隔离删除作用域错误")
			}
			if value, exists := content[rel]; exists {
				if req.SupplyData == nil {
					return fmt.Errorf("回收准备缺回填上下文")
				}
				return req.SupplyData(value, 0)
			}
		}
		return nil
	}, Completed: record,
		Result: func(req OperationRequest, err error) {
			mu.Lock()
			defer mu.Unlock()
			message := ""
			if err != nil {
				message = err.Error()
			}
			replies = append(replies, map[string]any{"request": req, "ackError": message})
			if err == nil && req.Kind == CallbackTypeNotifyDelete && req.Flags&OperationIsUndelete != 0 && os.Getenv("EASYSHARE_UNDO_PROBE_OBSERVE_ONLY") != "1" {
				if _, err := os.Stat(filepath.Join(run, "undo-issued")); err == nil {
					restores <- req
				}
			}
		}}
	if os.Getenv("EASYSHARE_UNDO_PROBE_NO_OPERATIONS") == "1" {
		callbacks = nil
	}
	m, err := MountSpace(MountOptions{RootPath: root, Provider: provider, SyncRootIdentity: []byte(filepath.Base(run)),
		Policies:       &SyncPolicies{Hydration: HydrationPolicy{Primary: 2}, Population: PopulationPolicy{Primary: 3}},
		ProtectedPaths: []string{}, List: func(context.Context) ([]CloudEntry, error) { return entries, nil },
		Open: func(_ context.Context, p string, off, n int64) (io.ReadCloser, error) {
			value := content[p]
			if off < 0 || off > int64(len(value)) || n <= 0 {
				return nil, fmt.Errorf("fixture range错误: path=%s off=%d n=%d size=%d", p, off, n, len(value))
			}
			end := min(int64(len(value)), off+n)
			return io.NopCloser(bytes.NewReader(value[off:end])), nil
		}, Operations: callbacks})
	if err != nil {
		t.Fatal(err)
	}
	stopRestore := make(chan struct{})
	var restoreWork sync.WaitGroup
	restoreWork.Add(1)
	go func() {
		defer restoreWork.Done()
		for {
			select {
			case <-stopRestore:
				return
			case req := <-restores:
				// ACK完成后按原服务器fixture身份铺回；不是新建本地替代云端文件。
				time.Sleep(150 * time.Millisecond)
				var restored []CloudEntry
				for _, entry := range entries {
					if bytes.Equal(entry.Identity, req.CloudIdentity) {
						restored = append(restored, entry)
						if entry.IsDir {
							for _, child := range entries {
								if strings.HasPrefix(child.Path, entry.Path+"/") {
									restored = append(restored, child)
								}
							}
						}
						break
					}
				}
				stats := BuildTreeProtected(root, restored, nil)
				mu.Lock()
				replies = append(replies, map[string]any{"restoreProjection": stats})
				mu.Unlock()
			}
		}
	}()
	defer func() {
		close(stopRestore)
		restoreWork.Wait()
		if err := m.Close(); err != nil {
			t.Errorf("断开probe失败: %v", err)
			return
		}
	}()
	if os.Getenv("EASYSHARE_UNDO_PROBE_COPYHOOK") == "1" {
		stopHook := startUndoCopyHookProbe(t, root, shellID, run, content)
		defer stopHook()
	}
	local := []byte("not uploaded local content\n")
	if err := os.WriteFile(filepath.Join(root, "未上传.txt"), local, 0600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("EASYSHARE_UNDO_PROBE_HYDRATE") == "1" {
		for p, expected := range content {
			actual, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
			if readErr != nil || !bytes.Equal(actual, expected) {
				t.Fatalf("缓存对照fixture读取失败:%v", readErr)
			}
		}
	}
	identities := map[string]DirectoryNTFSIdentity{}
	cloudIDs := map[string]string{}
	for _, rel := range []string{"云端.txt", "目录", "目录/子项.txt", "未上传.txt"} {
		identity, err := ReadDirectoryNTFSIdentity(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		identities[rel] = identity
		status, err := m.QueryPlaceholderStatus(rel)
		if err != nil {
			t.Fatal(err)
		}
		cloudIDs[rel] = string(status.CloudIdentity)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python", "-B", "-X", "utf8", filepath.Join(work, "tools", "gui", "undo-isolated-actions.py"), filepath.Join(run, "manifest.json"))
	output, inputErr := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(run, "gui-input.log"), output, 0600)
	mu.Lock()
	observed := append([]OperationRequest(nil), events...)
	mu.Unlock()
	b, _ = json.MarshalIndent(observed, "", "  ")
	_ = os.WriteFile(filepath.Join(run, "events.json"), b, 0600)
	b, _ = json.MarshalIndent(replies, "", "  ")
	_ = os.WriteFile(filepath.Join(run, "replies.json"), b, 0600)
	if inputErr != nil || !strings.Contains(string(output), "ISOLATED_EXPLORER_UNDO_INPUT_PASS") {
		t.Fatalf("真实输入失败: %v\n%s\n证据:%s", inputErr, output, run)
	}
	var proofs []map[string]any
	for rel, before := range identities {
		after, err := ReadDirectoryNTFSIdentity(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		status, err := m.QueryPlaceholderStatus(rel)
		if err != nil {
			t.Fatal(err)
		}
		proofs = append(proofs, map[string]any{"path": rel, "sameNTFS": before == after, "beforeCloudIdentity": cloudIDs[rel], "afterCloudIdentity": string(status.CloudIdentity), "onDisk": status.OnDisk})
		want := content[rel]
		if rel == "未上传.txt" {
			want = local
		}
		if want != nil {
			got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("撤销丢失内容 %s: %v", rel, err)
			}
			digest := sha256.Sum256(got)
			proofs[len(proofs)-1]["sha256"] = hex.EncodeToString(digest[:])
		}
	}
	b, _ = json.MarshalIndent(map[string]any{"status": "isolated-system-observation-completed", "proofs": proofs, "events": observed, "serverClosedLoop": false}, "", "  ")
	_ = os.WriteFile(filepath.Join(run, "result.json"), b, 0600)
	t.Logf("WINDOWS_UNDO_PROBE_PASS: events=%d evidence=%s（仅系统探针，不是服务器闭环）", len(observed), run)
}
