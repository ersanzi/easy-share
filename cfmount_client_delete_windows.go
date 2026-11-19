//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
)

// clientDelete复用同一持久记录和服务器协议，只移除准确自有投影，不从空列表猜删除。
func (c *nativeDeleteCoordinator) clientDelete(ctx context.Context, key string) error {
	path := strings.TrimSuffix(key, "/")
	if record, found := c.store.Get(path); found && record.Status == "recycled" && !record.LocalRemoved {
		record.ClientProjection = true
		if err := c.store.Put(record); err != nil {
			return err
		}
		return c.finishClientProjection(ctx, record)
	}
	status, err := c.queryStatus(c.mount, path)
	if err != nil {
		return err
	}
	req := cfapi.OperationRequest{Kind: cfapi.CallbackTypeNotifyDelete, Path: filepath.Join(c.cfg.RootPath, filepath.FromSlash(path)), CloudIdentity: status.CloudIdentity, FileSize: status.Size}
	if status.Directory {
		req.Flags = cfapi.OperationIsDirectory
	}
	if err := c.before(ctx, req, true); err != nil {
		return err
	}
	record, ok := c.store.Get(path)
	if !ok || record.Status != "recycled" {
		return fmt.Errorf("客户端删除未得到准确回收确认")
	}
	return c.finishClientProjection(ctx, record)
}

func (c *nativeDeleteCoordinator) finishClientProjection(ctx context.Context, record cfmount.DeleteRecord) error {
	if err := c.guard(ctx); err != nil {
		return err
	}
	path := record.Path
	checked, err := c.client.QueryRecycleOperation(ctx, c.token, c.cfg.Space, record.RemoteID, record.OperationID)
	if err != nil {
		return err
	}
	if checked == nil || checked.Status != "recycled" || checked.FileId != strconv.FormatInt(record.RemoteID, 10) || checked.Path != record.Path || checked.Directory != record.Directory || checked.BatchId != record.BatchID || checked.MemberCount != len(record.Members) {
		return fmt.Errorf("原删除批次已变化，保留本机内容")
	}
	proofBytes, err := os.ReadFile(filepath.Join(c.stateDir, "delete-proof-"+record.OperationID+".json"))
	if err != nil {
		return err
	}
	var proof nativeDeleteProof
	if err := json.Unmarshal(proofBytes, &proof); err != nil {
		return err
	}
	if proof.Namespace != c.namespace || proof.Root != c.cfg.RootPath || proof.Space != c.cfg.Space || proof.RootIdentity != c.rootIdentity || proof.OperationID != record.OperationID || len(proof.Members) != len(record.Members) {
		return fmt.Errorf("客户端删除收尾证据身份不一致")
	}
	for i, evidence := range proof.Members {
		if evidence.Member != record.Members[i] {
			return fmt.Errorf("客户端删除收尾成员身份不一致")
		}
		if evidence.LocalHash != "" && evidence.LocalHash != evidence.RemoteHash {
			return fmt.Errorf("服务器已回收，%s存在未提交编辑，本机内容保留", evidence.Member.Path)
		}
	}
	c.mu.Lock()
	c.projectionPaths[path] = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.projectionPaths, path); c.mu.Unlock() }()
	ordered := append([]string(nil), path)
	for _, m := range record.Members {
		if m.Path != path {
			ordered = append(ordered, m.Path)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	engine := c.engine()
	for _, evidence := range proof.Members {
		member := evidence.Member
		baseline, _ := engine.PendingForDelete(member.Path)
		if baseline.Pending {
			return fmt.Errorf("服务器已回收，%s存在未提交修改，本机内容保留", member.Path)
		}
		local, err := c.queryStatus(c.mount, member.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if nativeDeleteLocalIdentity(local.LocalIdentity) != member.LocalIdentity || local.Directory != member.Directory || local.Modified > 0 || !member.Directory && local.RemoteID != member.RemoteID {
			return fmt.Errorf("删除后本机目标已替换或编辑，保留内容")
		}
		if !member.Directory && evidence.LocalHash != "" {
			hash, size, err := nativeDeleteHashLocal(c.cfg.RootPath, member.Path, member.LocalIdentity)
			if err != nil || hash != evidence.LocalHash || size != evidence.LocalSize {
				return fmt.Errorf("删除后本机正文已变化，保留内容")
			}
		}
	}
	for _, p := range ordered {
		err := os.Remove(filepath.Join(c.cfg.RootPath, filepath.FromSlash(p)))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("服务器已回收，本机删除待重试 %s:%w", p, err)
		}
	}
	record.LocalRemoved = true
	if err := c.store.Put(record); err != nil {
		return err
	}
	return engine.ForgetConfirmedDeletion(ordered)
}

// remoteRestore仅由准确服务端还原结果调用。不存在的路径按原身份投影，已有未知内容不覆盖。
func (c *nativeDeleteCoordinator) remoteRestore(ctx context.Context, result *drive.RecycleOperationResult) error {
	if result == nil || result.Status != "restored" {
		return fmt.Errorf("服务器尚未确认还原")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.guard(ctx); err != nil {
		return err
	}
	objects, err := c.cfg.Client.Objects(ctx, c.token, c.cfg.Space)
	if err != nil {
		return err
	}
	var restored []cfapi.CloudEntry
	rootMatched := false
	for _, o := range objects {
		if o.Path == result.Path && strconv.FormatInt(o.FileId.Int64(), 10) == result.FileId && o.Directory == result.Directory {
			rootMatched = true
		}
		if nativeDeletePathWithin(o.Path, result.Path) {
			full := filepath.Join(c.cfg.RootPath, filepath.FromSlash(o.Path))
			if _, statErr := os.Lstat(full); statErr == nil {
				status, err := c.queryStatus(c.mount, o.Path)
				if err != nil || !status.Placeholder || status.Directory != o.Directory || !o.Directory && status.RemoteID != o.FileId.Int64() {
					return fmt.Errorf("还原位置存在未核实的本机内容，未覆盖 %s", o.Path)
				}
				if baseline, _ := c.engine().PendingForDelete(o.Path); baseline.Pending || status.Modified > 0 {
					return fmt.Errorf("还原位置有未提交编辑，保留本机内容 %s", o.Path)
				}
			} else if !os.IsNotExist(statErr) {
				return statErr
			}
			identity := cfapi.EncodeIdentity(o.FileId.Int64())
			if o.Directory {
				identity = []byte("esd:remote-" + o.Path)
			}
			restored = append(restored, cfapi.CloudEntry{Path: o.Path, Size: o.Size, Version: o.VersionId.Int64(), IsDir: o.Directory, Identity: identity})
		}
	}
	if !rootMatched || len(restored) != result.MemberCount {
		return fmt.Errorf("服务器还原成员数不一致，暂不投影")
	}
	stats := cfapi.BuildTreeProtected(c.cfg.RootPath, restored, nil)
	if len(stats.Failures) != 0 {
		return fmt.Errorf("还原投影失败:%w", stats)
	}
	protected := append([]string(nil), c.store.ActivePaths()...)
	for _, object := range objects {
		if !nativeDeletePathWithin(object.Path, result.Path) {
			protected = append(protected, object.Path)
		}
	}
	if err := c.mount.SyncProtected(cfmount.List(c.cfg), protected); err != nil {
		return err
	}
	for _, record := range c.store.Records() {
		if strconv.FormatInt(record.RemoteID, 10) == result.FileId && record.Path == result.Path {
			record.Status = "restored"
			if err := c.store.Put(record); err != nil {
				return err
			}
		}
	}
	return c.engine().ProtectDeletion(c.store.ActivePaths())
}

func (a *App) deleteNativeCloudKey(key string) (bool, error) {
	a.cfapi.mu.Lock()
	c := a.cfapi.deleteCoordinator
	life := a.cfapi.lifetime
	a.cfapi.mu.Unlock()
	if c == nil || life == nil {
		return false, nil
	}
	return true, c.clientDelete(life.ctx, key)
}
func (a *App) restoreNativeCloudResult(result *drive.RecycleOperationResult) error {
	a.cfapi.mu.Lock()
	c := a.cfapi.deleteCoordinator
	life := a.cfapi.lifetime
	a.cfapi.mu.Unlock()
	if c == nil || life == nil {
		return nil
	}
	return c.remoteRestore(life.ctx, result)
}
