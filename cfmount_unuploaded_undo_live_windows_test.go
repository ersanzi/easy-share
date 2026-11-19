//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

// 无生产namespace：真实Windows原生撤销后，用原WritebackEngine向真实服务器提交一次。
func TestLiveUnuploadedWindowsUndoContinuesUpload(t *testing.T) {
	if os.Getenv("EASYSHARE_UNUPLOADED_UNDO_LIVE") != "1" {
		t.Skip("真实Windows/服务器未上传撤销必须显式启用")
	}
	base, token := os.Getenv("EASYSHARE_LIVE_BASE"), os.Getenv("EASYSHARE_LIVE_TOKEN")
	if base == "" || token == "" {
		t.Fatal("缺少真实测试会话")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	client := drive.New(base)
	client.VerifyUploadCommit = true
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	remoteName := "未上传验收-" + hex.EncodeToString(nonce[:3]) + ".txt"
	adapter := &localUndoUploadClient{Client: client, RemotePath: remoteName}
	if rows, err := adapter.Objects(ctx, token, drive.SpacePersonal); err != nil || len(rows) != 0 {
		t.Fatal("真实服务器测试短名不为空")
	}
	wd, _ := os.Getwd()
	run, err := os.MkdirTemp(filepath.Join(wd, ".rd-test"), "undo-probe-")
	if err != nil {
		t.Fatal(err)
	}
	root, state := filepath.Join(run, "root"), filepath.Join(run, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	providerID, _ := windows.GenerateGUID()
	cfg := cfmount.Config{RootPath: root, Space: drive.SpacePersonal, Client: adapter, Token: func() string { return token }, Writable: true}
	mount, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: "EasyShareUnuploadedUndoIsolated", Version: "test", ID: providerID}, SyncRootIdentity: []byte(filepath.Base(run)), ProtectedPaths: []string{}, List: func(context.Context) ([]cfapi.CloudEntry, error) { return nil, nil }, Open: cfmount.Open(cfg)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mount.Close(); err != nil {
			t.Errorf("断开本轮隔离根失败:%v", err)
			return
		}
		clean, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		rows, err := client.Objects(clean, token, drive.SpacePersonal)
		if err != nil {
			t.Errorf("清理服务器测试文件读取失败")
			return
		}
		for _, row := range rows {
			if row.Path != remoteName {
				continue
			}
			stream, _, err := client.Open(clean, token, drive.SpacePersonal, remoteName)
			if err != nil {
				t.Errorf("清理正文核对失败")
				return
			}
			b, readErr := io.ReadAll(io.LimitReader(stream, 64))
			_ = stream.Close()
			if readErr != nil || !bytes.Equal(b, []byte("not uploaded local content\n")) {
				t.Errorf("测试正文已变，保留")
				return
			}
			v := row.VersionId.Int64()
			if _, err := client.DeleteOperation(clean, token, drive.SpacePersonal, row.FileId.Int64(), remoteName, &v, "cleanup-"+hex.EncodeToString(nonce[:]), false); err != nil {
				t.Errorf("准确测试回收失败")
				return
			}
			if err := client.RecyclePurge(clean, token, drive.SpacePersonal, row.FileId.Int64()); err != nil {
				t.Errorf("准确测试清理失败")
				return
			}
		}
		cmd := exec.Command("python", "-B", "-X", "utf8", filepath.Join(wd, "..", "..", "tools", "maintenance", "cleanup-owned-undo-probes.py"), "--run", run)
		out, err := cmd.CombinedOutput()
		_ = os.WriteFile(filepath.Join(run, "cleanup.log"), out, 0600)
		if err != nil {
			t.Errorf("本轮原生隔离根收尾失败:%v %s", err, out)
		}
	})
	body := []byte("not uploaded local content\n")
	full := filepath.Join(root, "未上传.txt")
	if err := os.WriteFile(full, body, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := cfapi.ReadDirectoryNTFSIdentity(full)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := cfmount.NewWritebackEngine(cfmount.WritebackConfig{RootPath: root, StateDir: state, Namespace: filepath.Base(run), Space: cfg.Space, Client: adapter, Token: cfg.Token, StableFor: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	// 第一轮只记录不稳定观察，未发起上传；测试刻意不运行并发轮询。
	if _, err := engine.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if adapter.Uploads != 0 {
		t.Fatal("前置文件已上传，不可当未上传撤销验收")
	}
	manifest, _ := json.Marshal(map[string]any{"run": run, "root": root, "items": []string{"未上传.txt"}})
	path := filepath.Join(run, "manifest.json")
	if err := os.WriteFile(path, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "python", "-B", "-X", "utf8", filepath.Join(wd, "tools", "gui", "undo-isolated-actions.py"), path)
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(run, "gui.log"), out, 0600)
	if err != nil || !strings.Contains(string(out), "ISOLATED_EXPLORER_UNDO_INPUT_PASS") {
		t.Fatalf("真实Delete/CtrlZ失败:%v %s", err, out)
	}
	after, err := cfapi.ReadDirectoryNTFSIdentity(full)
	if err != nil || before != after {
		t.Fatal("撤销后的原NTFS身份不一致")
	}
	result, err := engine.Tick(ctx)
	if err != nil || len(result.Verified) != 1 || adapter.Uploads != 1 {
		t.Fatalf("原流程未上传一次:%+v %v uploads=%d", result, err, adapter.Uploads)
	}
	for i := 0; i < 2; i++ {
		if _, err := engine.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := adapter.Objects(ctx, token, drive.SpacePersonal)
	if err != nil || len(rows) != 1 || rows[0].VersionId != 1 || adapter.Uploads != 1 {
		t.Fatal("已确认恢复内容被重复上传")
	}
	proof, _ := json.MarshalIndent(map[string]any{"status": "REAL_UNUPLOADED_WINDOWS_UNDO_UPLOAD_PASS", "controlledTickSchedule": true, "actualDeleteCtrlZ": true, "sameLocalNTFS": true, "remotePath": remoteName, "fileId": rows[0].FileId, "version": 1, "uploadCalls": adapter.Uploads}, "", "  ")
	_ = os.WriteFile(filepath.Join(run, "result.json"), proof, 0600)
	t.Log("REAL_UNUPLOADED_WINDOWS_UNDO_UPLOAD_PASS", run)
}

type localUndoUploadClient struct {
	*drive.Client
	RemotePath string
	Uploads    int
}

func (c *localUndoUploadClient) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	rows, err := c.Client.Objects(ctx, token, space)
	if err != nil {
		return nil, err
	}
	out := []drive.Object{}
	for _, row := range rows {
		if row.Path == c.RemotePath {
			row.Path = "未上传.txt"
			out = append(out, row)
		}
	}
	return out, nil
}
func (c *localUndoUploadClient) Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error) {
	if path != "未上传.txt" {
		return nil, 0, os.ErrNotExist
	}
	return c.Client.Open(ctx, token, space, c.RemotePath)
}
func (c *localUndoUploadClient) UploadFileIfVersion(ctx context.Context, token, space, path, local string, version *int64, progress drive.ProgressFunc) error {
	if path != "未上传.txt" {
		return os.ErrNotExist
	}
	c.Uploads++
	return c.Client.UploadFileIfVersion(ctx, token, space, c.RemotePath, local, version, progress)
}
