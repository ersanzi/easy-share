//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

// 真实服务器+CFAPI+客户端删除还原；只创建本轮短名，不注册正式此电脑入口。
func TestLiveNativeClientDeleteRestore(t *testing.T) {
	if os.Getenv("EASYSHARE_DELETE_LIVE") != "1" {
		t.Skip("真实删还原必须显式启用")
	}
	base, token := os.Getenv("EASYSHARE_LIVE_BASE"), os.Getenv("EASYSHARE_LIVE_TOKEN")
	if base == "" || token == "" {
		t.Fatal("缺少显式服务器测试会话")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	client := drive.New(base)
	client.VerifyUploadCommit = true
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "删还原-" + hex.EncodeToString(nonce[:3]) + ".txt"
	rows, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal("读取服务器失败")
	}
	for _, row := range rows {
		if row.Path == name {
			t.Fatal("测试短名已存在")
		}
	}
	wd, _ := os.Getwd()
	run, err := os.MkdirTemp(filepath.Join(wd, ".rd-test"), "delete-live-")
	if err != nil {
		t.Fatal(err)
	}
	root, state := filepath.Join(run, "root"), filepath.Join(run, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	content := append([]byte("native remote deletion roundtrip\n"), nonce[:]...)
	source := filepath.Join(run, "source.txt")
	if err := os.WriteFile(source, content, 0600); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if err := client.UploadFileIfVersion(ctx, token, drive.SpacePersonal, name, source, &zero, nil); err != nil {
		t.Fatal("上传测试正文失败")
	}
	rows, err = client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal(err)
	}
	var original drive.Object
	for _, row := range rows {
		if row.Path == name {
			original = row
		}
	}
	if original.FileId <= 0 {
		t.Fatal("测试文件身份未提交")
	}
	providerID, _ := windows.GenerateGUID()
	cfg := cfmount.Config{RootPath: root, Space: drive.SpacePersonal, Client: client, Token: func() string { return token }, Writable: true, ProviderName: "EasyShareDeleteIsolated", ProviderID: providerID}
	engine, err := cfmount.NewWritebackEngine(cfmount.WritebackConfig{RootPath: root, StateDir: state, Namespace: filepath.Base(run), Space: cfg.Space, Client: client, Token: cfg.Token, StableFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := newNativeDeleteCoordinator(cfg, state, filepath.Base(run), func() *cfmount.WritebackEngine { return engine })
	if err != nil {
		t.Fatal(err)
	}
	// 主线协调器读取完整空间；隔离根只投影本轮测试文件，不下载用户其它正文。
	mount, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "test", ID: providerID}, SyncRootIdentity: []byte(filepath.Base(run)), ProtectedPaths: []string{}, List: func(context.Context) ([]cfapi.CloudEntry, error) {
		return []cfapi.CloudEntry{{Path: name, Size: original.Size, Version: original.VersionId.Int64(), Identity: cfapi.EncodeIdentity(original.FileId.Int64())}}, nil
	}, Open: cfmount.Open(cfg), Operations: deletion.Callbacks()})
	if err != nil {
		t.Fatal(err)
	}
	deletion.Callbacks(mount)
	t.Cleanup(func() {
		if err := mount.Close(); err != nil {
			t.Errorf("隔离挂载断开失败，保留证据:%v", err)
			return
		}
		clean, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		current, err := client.Objects(clean, token, drive.SpacePersonal)
		if err != nil {
			t.Errorf("精确测试清理读取失败")
			return
		}
		for _, row := range current {
			if row.Path == name {
				if row.FileId != original.FileId {
					t.Errorf("测试短名身份已变，拒绝清理")
					return
				}
				rc, _, err := client.Open(clean, token, drive.SpacePersonal, name)
				if err != nil {
					t.Errorf("清理正文验证失败")
					return
				}
				b, e := io.ReadAll(io.LimitReader(rc, int64(len(content)+1)))
				_ = rc.Close()
				if e != nil || !bytes.Equal(b, content) {
					t.Errorf("测试正文已改，拒绝清理")
					return
				}
				v := row.VersionId.Int64()
				if _, err := client.DeleteOperation(clean, token, drive.SpacePersonal, row.FileId.Int64(), name, &v, "cleanup-"+hex.EncodeToString(nonce[:]), false); err != nil {
					t.Errorf("精确测试回收失败")
					return
				}
			}
		}
		if err := client.RecyclePurge(clean, token, drive.SpacePersonal, original.FileId.Int64()); err != nil {
			t.Errorf("精确测试快照清理失败")
			return
		}
		// 注销只作用于新建的本轮隔离根；若还原临时索引带入别项，则保守保留本机投影。
		if err := cfapi.UnregisterSyncRoot(root); err != nil {
			t.Errorf("精确隔离根注销失败:%v", err)
		}
	})
	if err := deletion.clientDelete(ctx, name); err != nil {
		t.Fatal("客户端原生删除失败: ", err)
	}
	if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatal("服务器已删但本机仍存在")
	}
	for _, row := range mustObjects(t, ctx, client, token) {
		if row.FileId == original.FileId {
			t.Fatal("服务器删除未完成")
		}
	}
	result, err := client.RestoreOperation(ctx, token, drive.SpacePersonal, original.FileId.Int64())
	if err != nil {
		t.Fatal(err)
	}
	// 隔离根不能使用全盘投影；此处先测试主线精确还原算法并保护其它服务器路径。
	all := mustObjects(t, ctx, client, token)
	other := []string{}
	for _, row := range all {
		if row.Path != name {
			other = append(other, row.Path)
		}
	}
	if err := mount.SyncProtected(cfmount.List(cfg), other); err != nil {
		t.Fatal(err)
	}
	if err := deletion.remoteRestore(ctx, result); err != nil {
		t.Fatal("客户端还原投影失败: ", err)
	}
	actual, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatal("客户端还原后CFAPI读取正文错误", err)
	}
	rowFound := false
	for _, row := range mustObjects(t, ctx, client, token) {
		if row.Path == name {
			rowFound = row.FileId == original.FileId && row.VersionId == original.VersionId
		}
	}
	if !rowFound {
		t.Fatal("还原改变服务器身份或版本")
	}
	h := sha256.Sum256(actual)
	proof := map[string]any{"status": "live-native-client-delete-restore-pass", "name": name, "fileId": fmt.Sprint(original.FileId), "version": original.VersionId.Int64(), "sha256": hex.EncodeToString(h[:]), "serverReal": true, "clientDelete": true, "cloudRecycleRestore": true, "ctrlZTested": false, "thisPCMouseTested": false}
	data, _ := json.MarshalIndent(proof, "", "  ")
	_ = os.WriteFile(filepath.Join(run, "proof.json"), data, 0600)
	t.Log("LIVE_CLIENT_DELETE_RESTORE_PASS", run)
}

func mustObjects(t *testing.T, ctx context.Context, c *drive.Client, token string) []drive.Object {
	t.Helper()
	rows, err := c.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
