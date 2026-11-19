//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
)

func TestNativeDeleteFailureRestoresRemoteAfterRestart(t *testing.T) {
	for _, preparing := range []bool{false, true} {
		t.Run(map[bool]string{false: "ack-failed", true: "response-timeout"}[preparing], func(t *testing.T) {
			c, remote, req := deletionCompensationFixture(t)
			if err := c.Before(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			if preparing {
				r, _ := c.store.Get("a.txt")
				r.Status = "preparing"
				r.BatchID = ""
				if err := c.store.Put(r); err != nil {
					t.Fatal(err)
				}
			} else {
				c.Result(req, errors.New("Windows ACK failed"))
			}
			// 模拟进程退出后内存 issues 消失；必须用持久意图恢复，而不是靠上一进程回调。
			restarted, err := newNativeDeleteCoordinator(c.cfg, c.stateDir, c.namespace, c.engine)
			if err != nil {
				t.Fatal(err)
			}
			restarted.queryStatus = c.queryStatus
			if err := restarted.BeforeReconcile(context.Background(), &cfapi.Mount{}); err != nil {
				t.Fatal(err)
			}
			r, _ := restarted.store.Get("a.txt")
			if r.Status != "restored" || len(restarted.store.ActivePaths()) != 0 {
				t.Fatalf("not compensated: %+v", r)
			}
			rows, _ := remote.Objects(context.Background(), "", "")
			if len(rows) != 1 || rows[0].FileId != 7 || rows[0].VersionId != 3 {
				t.Fatalf("remote identity changed: %+v", rows)
			}
			if b, err := os.ReadFile(req.Path); err != nil || string(b) != "original content\n" {
				t.Fatal("local original was altered")
			}
		})
	}
}

func TestNativeDeleteFailureNeverRestoresSameNameReplacement(t *testing.T) {
	c, remote, req := deletionCompensationFixture(t)
	if err := c.Before(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	c.Result(req, errors.New("Windows cancelled"))
	if err := os.Rename(req.Path, req.Path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(req.Path, []byte("replacement content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.BeforeReconcile(context.Background(), &cfapi.Mount{}); err == nil {
		t.Fatal("same-name replacement accepted as original")
	}
	rows, _ := remote.Objects(context.Background(), "", "")
	if len(rows) != 0 || len(c.store.ActivePaths()) != 1 {
		t.Fatal("uncertain identity lost deletion protection")
	}
	if b, _ := os.ReadFile(req.Path); string(b) != "replacement content\n" {
		t.Fatal("replacement was modified")
	}
}

func deletionCompensationFixture(t *testing.T) (*nativeDeleteCoordinator, *undoFlowRemote, cfapi.OperationRequest) {
	t.Helper()
	base := t.TempDir()
	root, state := filepath.Join(base, "root"), filepath.Join(base, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, "a.txt")
	body := []byte("original content\n")
	if err := os.WriteFile(full, body, 0600); err != nil {
		t.Fatal(err)
	}
	remote := &undoFlowRemote{objects: []drive.Object{{Path: "a.txt", FileId: 7, VersionId: 3, Size: int64(len(body))}}, content: map[string][]byte{"a.txt": body}}
	cfg := cfmount.Config{RootPath: root, Space: drive.SpacePersonal, Client: remote, Token: func() string { return "fixture-token" }}
	engine, err := cfmount.NewWritebackEngine(cfmount.WritebackConfig{RootPath: root, StateDir: state, Namespace: "cancel-fixture", Space: cfg.Space, Client: remote, Token: cfg.Token})
	if err != nil {
		t.Fatal(err)
	}
	c, err := newNativeDeleteCoordinator(cfg, state, "cancel-fixture", func() *cfmount.WritebackEngine { return engine })
	if err != nil {
		t.Fatal(err)
	}
	c.mount = &cfapi.Mount{}
	c.queryStatus = func(_ *cfapi.Mount, path string) (cfapi.PlaceholderStatus, error) {
		id, err := cfapi.ReadDirectoryNTFSIdentity(filepath.Join(root, filepath.FromSlash(path)))
		return cfapi.PlaceholderStatus{Size: int64(len(body)), FullContent: true, LocalIdentity: id, CloudIdentity: cfapi.EncodeIdentity(7)}, err
	}
	return c, remote, cfapi.OperationRequest{Kind: cfapi.CallbackTypeNotifyDelete, Path: full, CloudIdentity: cfapi.EncodeIdentity(7)}
}

func TestNativeDeleteCompletionSurvivesFastUndoBeforePoll(t *testing.T) {
	c, remote, req := deletionCompensationFixture(t)
	if err := c.Before(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// 删除完成通知到达时，Windows 已经把原文件撤销回原位置；路径轮询看不到缺失。
	req.Kind = cfapi.CallbackTypeNotifyDeleteCompletion
	c.Completed(req)
	r, _ := c.store.Get("a.txt")
	if !r.LocalRemoved {
		t.Fatal("删除完成回调仍用路径存在性丢掉快速撤销事实")
	}
	if err := c.BeforeReconcile(context.Background(), &cfapi.Mount{}); err != nil {
		t.Fatal(err)
	}
	rows, _ := remote.Objects(context.Background(), "", "")
	if len(rows) != 1 || rows[0].FileId != 7 || rows[0].VersionId != 3 {
		t.Fatal("原服务器身份未恢复")
	}
}

func TestNativeRecycleRenameCompletionUsesOriginalSourcePath(t *testing.T) {
	c, _, req := deletionCompensationFixture(t)
	if err := c.Before(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Kind = cfapi.CallbackTypeNotifyRenameCompletion
	req.SourcePath, req.Path = req.Path, `C:\$Recycle.Bin\S-1-5-21-fixture\$Rfixture.txt`
	c.Completed(req)
	r, _ := c.store.Get("a.txt")
	if !r.LocalRemoved {
		t.Fatal("移入系统回收站完成时未记录原路径删除事实")
	}
}

func TestClientProjectionRejectsRestoredRemoteOperation(t *testing.T) {
	c, remote, req := deletionCompensationFixture(t)
	if err := c.before(context.Background(), req, true); err != nil {
		t.Fatal(err)
	}
	r, _ := c.store.Get("a.txt")
	if _, err := remote.RestoreOperation(context.Background(), "", "", r.RemoteID); err != nil {
		t.Fatal(err)
	}
	if err := c.finishClientProjection(context.Background(), r); err == nil {
		t.Fatal("服务器已还原的旧删除请求仍允许移除本机文件")
	}
	if b, err := os.ReadFile(req.Path); err != nil || string(b) != "original content\n" {
		t.Fatal("本机原内容未保留")
	}
}

func TestClientProjectionRejectsEditedBodyAndRetainsIdentity(t *testing.T) {
	c, _, req := deletionCompensationFixture(t)
	if err := c.before(context.Background(), req, true); err != nil {
		t.Fatal(err)
	}
	r, _ := c.store.Get("a.txt")
	if err := os.WriteFile(req.Path, []byte("edited original content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.finishClientProjection(context.Background(), r); err == nil {
		t.Fatal("删除等待期间的编辑被丢弃")
	}
	if b, err := os.ReadFile(req.Path); err != nil || string(b) != "edited original content\n" {
		t.Fatal("未提交编辑未保留")
	}
}
