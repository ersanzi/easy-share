//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"easyshare/internal/shellroot"
	"golang.org/x/sys/windows"
)

// 真实Windows回收/撤销，远端fixture独立；不登录、不调用用户服务器。
type undoFlowRemote struct {
	mu        sync.Mutex
	objects   []drive.Object
	deleted   []drive.Object
	content   map[string][]byte
	operation *drive.RecycleOperationResult
	uploads   int
}

func (r *undoFlowRemote) Objects(context.Context, string, string) ([]drive.Object, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]drive.Object(nil), r.objects...), nil
}
func (r *undoFlowRemote) Open(_ context.Context, _, _, path string) (io.ReadCloser, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.content[path]
	if !ok {
		return nil, 0, fmt.Errorf("fixture missing")
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}
func (r *undoFlowRemote) UploadFileIfVersion(context.Context, string, string, string, string, *int64, drive.ProgressFunc) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploads++
	return fmt.Errorf("unexpected fixture upload")
}
func (r *undoFlowRemote) DeleteOperation(_ context.Context, _, _ string, id int64, path string, version *int64, op string, recursive bool) (*drive.RecycleOperationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.operation != nil && r.operation.OperationId == op {
		copy := *r.operation
		return &copy, nil
	}
	var target drive.Object
	found := false
	for _, o := range r.objects {
		if o.FileId.Int64() == id {
			target = o
			found = true
		}
	}
	if !found || target.Path != path || version == nil || *version != target.VersionId.Int64() {
		return nil, fmt.Errorf("fixture identity/version mismatch")
	}
	r.deleted = nil
	keep := r.objects[:0]
	for _, o := range r.objects {
		if o.Path == path || recursive && strings.HasPrefix(o.Path, path+"/") {
			r.deleted = append(r.deleted, o)
		} else {
			keep = append(keep, o)
		}
	}
	r.objects = keep
	r.operation = &drive.RecycleOperationResult{BatchId: "fixture-batch-" + op, OperationId: op, FileId: strconv.FormatInt(id, 10), Path: path, Directory: target.Directory, Status: "recycled", MemberCount: len(r.deleted)}
	copy := *r.operation
	return &copy, nil
}
func (r *undoFlowRemote) QueryRecycleOperation(_ context.Context, _, _ string, id int64, op string) (*drive.RecycleOperationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.operation == nil || r.operation.FileId != strconv.FormatInt(id, 10) || r.operation.OperationId != op {
		return nil, fmt.Errorf("fixture operation unknown")
	}
	copy := *r.operation
	return &copy, nil
}
func (r *undoFlowRemote) RestoreOperation(_ context.Context, _, _ string, id int64) (*drive.RecycleOperationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.operation == nil || r.operation.FileId != strconv.FormatInt(id, 10) {
		return nil, fmt.Errorf("fixture restore mismatch")
	}
	if r.operation.Status != "restored" {
		r.objects = append(r.objects, r.deleted...)
		r.operation.Status = "restored"
	}
	copy := *r.operation
	return &copy, nil
}

func TestNativeDeleteWindowsUndoIsolated(t *testing.T) {
	if os.Getenv("EASYSHARE_UNDO_FLOW") != "1" {
		t.Skip("真实原生回收撤销需显式隔离启用")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(filepath.Join(wd, ".rd-test"), "undo-probe-")
	if err != nil {
		t.Fatal(err)
	}
	root, state := filepath.Join(run, "root"), filepath.Join(run, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	remote := &undoFlowRemote{content: map[string][]byte{"云端.txt": []byte("cloud committed identity fixture\n"), "目录/子项.txt": []byte("nested cloud committed fixture\n")}}
	remote.objects = []drive.Object{
		{Path: "云端.txt", Size: int64(len(remote.content["云端.txt"])), FileId: 880101, VersionId: 3},
		{Path: "目录", Directory: true, FileId: 880100, VersionId: 1},
		{Path: "目录/子项.txt", Size: int64(len(remote.content["目录/子项.txt"])), FileId: 880102, VersionId: 2},
	}
	cfg := cfmount.Config{RootPath: root, Space: drive.SpacePersonal, Client: remote, Token: func() string { return "isolated-token" }, Writable: true, ProviderName: "EasyShareUndoFlowIsolated"}
	cfg.ProviderID, err = windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := cfmount.NewWritebackEngine(cfmount.WritebackConfig{RootPath: root, StateDir: state, Space: cfg.Space, Namespace: filepath.Base(run), Client: remote, Token: cfg.Token, StableFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := newNativeDeleteCoordinator(cfg, state, filepath.Base(run), func() *cfmount.WritebackEngine { return engine })
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	shellID := cfg.ProviderName + "!" + user.User.Sid.String() + "!" + filepath.Base(run)
	if err := shellroot.Register(root, shellID, "撤销验证", "%SystemRoot%/System32/imageres.dll,-1040", "test", []byte(filepath.Base(run))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = shellroot.UnregisterIsolated(shellID)
		cmd := exec.Command("python", "-B", "-X", "utf8", filepath.Join(wd, "..", "..", "tools", "maintenance", "cleanup-owned-undo-probes.py"), "--run", run)
		out, err := cmd.CombinedOutput()
		_ = os.WriteFile(filepath.Join(run, "cleanup.log"), out, 0600)
		if err != nil {
			t.Errorf("fixture cleanup:%v %s", err, out)
		}
	})
	mount, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "test", ID: cfg.ProviderID}, SyncRootIdentity: []byte(filepath.Base(run)), ProtectedPaths: []string{}, List: func(context.Context) ([]cfapi.CloudEntry, error) {
		return []cfapi.CloudEntry{{Path: "云端.txt", Size: int64(len(remote.content["云端.txt"])), Version: 3, Identity: cfapi.EncodeIdentity(880101)},
			{Path: "目录", IsDir: true, Identity: []byte("esd:undo-fixture")},
			{Path: "目录/子项.txt", Size: int64(len(remote.content["目录/子项.txt"])), Version: 2, Identity: cfapi.EncodeIdentity(880102)}}, nil
	}, Open: cfmount.Open(cfg), Operations: coordinator.Callbacks()})
	if err != nil {
		t.Fatal(err)
	}
	defer mount.Close()
	coordinator.Callbacks(mount)
	if err := os.WriteFile(filepath.Join(root, "未上传.txt"), []byte("not uploaded local content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"run": run, "root": root, "items": []string{"未上传.txt", "云端.txt", "目录"}, "menuRequest": true, "providerPID": os.Getpid()}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(run, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	stop := make(chan struct{})
	var tasks sync.WaitGroup
	tasks.Add(1)
	go func() {
		defer tasks.Done()
		tick := time.NewTicker(40 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				b, err := os.ReadFile(filepath.Join(run, "menu-request.json"))
				if err == nil {
					var req struct{ Path string }
					if json.Unmarshal(b, &req) == nil {
						_ = os.Remove(filepath.Join(run, "menu-request.json"))
						err = coordinator.menuDelete(ctx, []string{req.Path})
						reply := map[string]any{"path": req.Path, "error": ""}
						if err != nil {
							reply["error"] = err.Error()
						}
						out, _ := json.Marshal(reply)
						_ = os.WriteFile(filepath.Join(run, "menu-response.json"), out, 0600)
					}
				}
				_ = coordinator.BeforeReconcile(ctx, mount)
			}
		}
	}()
	defer func() { close(stop); tasks.Wait() }()
	cmd := exec.CommandContext(ctx, "python", "-B", "-X", "utf8", filepath.Join(wd, "tools", "gui", "undo-isolated-actions.py"), filepath.Join(run, "manifest.json"))
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(run, "gui.log"), out, 0600)
	if err != nil || !strings.Contains(string(out), "ISOLATED_EXPLORER_UNDO_INPUT_PASS") {
		t.Fatalf("原生回收撤销输入:%v %s evidence=%s", err, out, run)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_ = coordinator.BeforeReconcile(ctx, mount)
		pending := coordinator.store.ActivePaths()
		if len(pending) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pending := coordinator.store.ActivePaths(); len(pending) != 0 {
		t.Fatalf("撤销未联动fixture服务器:%v evidence=%s", pending, run)
	}
	remote.mu.Lock()
	count, uploads := len(remote.objects), remote.uploads
	remote.mu.Unlock()
	if count != 3 || uploads != 0 {
		t.Fatalf("恢复错误或误当新文件重传 count=%d uploads=%d", count, uploads)
	}
	proof, _ := json.MarshalIndent(map[string]any{"status": "isolated-native-undo-closed-loop-pass", "serverFixture": true, "productionServer": false, "objects": count, "newUploads": uploads, "records": coordinator.store.Records()}, "", "  ")
	_ = os.WriteFile(filepath.Join(run, "result.json"), proof, 0600)
	t.Logf("NATIVE_UNDO_FLOW_PASS evidence=%s", run)
}
