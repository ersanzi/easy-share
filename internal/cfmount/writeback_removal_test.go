package cfmount

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/drive"
)

// wbRemovalFake 在 wbFake 之上补齐删除/移动回传能力、远端目录标记与行为记录。
type wbRemovalFake struct {
	wbFake
	moves      [][2]string
	deletes    []string
	trees      []string
	remoteDirs []string // 远端目录标记（wbFake 的清单只含文件）
	failMove   bool
	failDelete bool
}

func (f *wbRemovalFake) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	out, err := f.wbFake.Objects(ctx, token, space)
	if err != nil {
		return nil, err
	}
	for _, d := range f.remoteDirs {
		out = append(out, drive.Object{FileId: 123, Path: d, Directory: true, VersionId: drive.FlexInt64(1)})
	}
	return out, nil
}

func (f *wbRemovalFake) Move(_ context.Context, _, _, from, to string) error {
	if f.failMove {
		return errors.New("move offline")
	}
	f.moves = append(f.moves, [2]string{from, to})
	f.data[to] = f.data[from]
	delete(f.data, from)
	return nil
}

func (f *wbRemovalFake) Delete(_ context.Context, _, _, p string) error {
	if f.failDelete {
		return errors.New("delete offline")
	}
	f.deletes = append(f.deletes, p)
	delete(f.data, p)
	return nil
}

func (f *wbRemovalFake) DeleteTree(_ context.Context, _, _, p string) error {
	if f.failDelete {
		return errors.New("delete offline")
	}
	f.trees = append(f.trees, p)
	for key := range f.data {
		if key == p || strings.HasPrefix(key, p+"/") {
			delete(f.data, key)
		}
	}
	for i, d := range f.remoteDirs {
		if d == p || strings.HasPrefix(d, p+"/") {
			f.remoteDirs = append(f.remoteDirs[:i], f.remoteDirs[i+1:]...)
			break
		}
	}
	return nil
}

// testIdentity 构造可区分的 NTFS 身份。
func testIdentity(n byte) LocalFileIdentity {
	var id LocalFileIdentity
	id.VolumeSerial = 0xABCD
	id.FileID[0] = n
	return id
}

// removalSetup 建引擎并注入身份映射（相对路径 → NTFS 身份）。
func removalSetup(t *testing.T, identities map[string]LocalFileIdentity) (*WritebackEngine, *wbRemovalFake, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	f := &wbRemovalFake{wbFake: wbFake{data: map[string][]byte{}, versions: map[string]int64{}}}
	e, err := NewWritebackEngine(WritebackConfig{
		RootPath: root, StateDir: filepath.Join(base, "state"), Namespace: "server/account", Space: "personal",
		Client: f, Token: func() string { return "test" }, StableFor: time.Nanosecond,
		IdentityFor: func(p string) (LocalFileIdentity, error) {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return LocalFileIdentity{}, err
			}
			return identities[filepath.ToSlash(rel)], nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e, f, root
}

// seedCommittedFile 放一个已提交基线的文件：本机正文 + 远端清单 + 记录（哈希取真实内容）。
func seedCommittedFile(t *testing.T, e *WritebackEngine, f *wbRemovalFake, root, rel, content string, id LocalFileIdentity) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	f.data[rel] = []byte(content)
	f.versions[rel] = 1
	e.state.Records[rel] = &writebackRecord{FileID: 123, Version: 1, Hash: hex.EncodeToString(sum[:]), Size: int64(len(content)), Identity: localIdentityOf(id)}
}

func issueKinds(res WritebackResult) []string {
	kinds := []string{}
	for _, is := range res.Issues {
		kinds = append(kinds, is.Kind)
	}
	return kinds
}

// expireMissing 把已登记的缺席计时拨到过去：Windows 单调时钟粒度约 0.5ms，
// 测试里两轮 Tick 间隔可能小于 1ns 档的 StableFor，确认窗口会随机吞轮次。
func expireMissing(e *WritebackEngine, paths ...string) {
	for _, p := range paths {
		if _, ok := e.missing[p]; ok {
			e.missing[p] = time.Now().Add(-time.Minute)
		}
	}
}

func hasKind(res WritebackResult, kind string) bool {
	for _, k := range issueKinds(res) {
		if k == kind {
			return true
		}
	}
	return false
}

// 资源管理器删除 → 连续两轮缺席后软删进云端回收站；第一轮只登记不删除。
func TestRemovalDeletePropagatesAfterTwoTicks(t *testing.T) {
	id := testIdentity(1)
	e, f, root := removalSetup(t, map[string]LocalFileIdentity{"doc.txt": id})
	seedCommittedFile(t, e, f, root, "doc.txt", "hello", id)
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "doc.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	expireMissing(e, "doc.txt")
	if len(f.deletes) != 0 {
		t.Fatalf("第一轮不应回传删除: %v", f.deletes)
	}
	res, err := e.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 1 || f.deletes[0] != "doc.txt" {
		t.Fatalf("第二轮应回传一次删除, got %v", f.deletes)
	}
	if e.state.Records["doc.txt"] != nil {
		t.Fatal("删除回传后记录应被丢弃")
	}
	for _, p := range res.ProtectedPaths {
		if p == "doc.txt" {
			t.Fatal("已回传删除的路径不应继续被保护")
		}
	}
}

// 远端同路径文件身份与基线不符时绝不代删（另一客户端删了又传新文件的场景）。
func TestRemovalDeleteGuardedByIdentity(t *testing.T) {
	id := testIdentity(2)
	e, f, root := removalSetup(t, map[string]LocalFileIdentity{"doc.txt": id})
	seedCommittedFile(t, e, f, root, "doc.txt", "hello", id)
	e.state.Records["doc.txt"].FileID = 999 // 模拟基线身份过期
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "doc.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	expireMissing(e, "doc.txt")
	res, err := e.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 0 {
		t.Fatalf("身份不符时不得回传删除: %v", f.deletes)
	}
	if e.state.Records["doc.txt"] == nil {
		t.Fatal("身份不符时应保留记录")
	}
	if !hasKind(res, "delete-identity") {
		t.Fatalf("应报 delete-identity 问题: %+v", res.Issues)
	}
}

// 改名回传：身份连续 + 大小一致 → 走 Move 保 fileId，不重新上传。
func TestRemovalRenamePropagatesKeepsFileID(t *testing.T) {
	id := testIdentity(3)
	idents := map[string]LocalFileIdentity{"old.txt": id, "new.txt": id}
	e, f, root := removalSetup(t, idents)
	seedCommittedFile(t, e, f, root, "old.txt", "payload", id)
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "old.txt"), filepath.Join(root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	res, err := e.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.moves) != 1 || f.moves[0] != [2]string{"old.txt", "new.txt"} {
		t.Fatalf("应回传一次移动, got %v", f.moves)
	}
	if f.uploads != 0 {
		t.Fatal("改名目标不应被当作新文件重新上传")
	}
	r := e.state.Records["new.txt"]
	if r == nil || r.FileID != 123 || r.Identity == nil || *r.Identity != *localIdentityOf(id) {
		t.Fatalf("记录应改挂到 new.txt 并保留身份: %+v", r)
	}
	if e.state.Records["old.txt"] != nil {
		t.Fatal("旧路径记录应被丢弃")
	}
	for _, k := range issueKinds(res) {
		if k == "upload" || k == "conflict" {
			t.Fatalf("改名不应触发上传类问题: %+v", res.Issues)
		}
	}
}

// 改名目标被云端占用：不移动也不删除（连续多轮也不删），保守报 issue 等用户处理。
func TestRemovalRenameTargetOccupied(t *testing.T) {
	id := testIdentity(4)
	idents := map[string]LocalFileIdentity{"old.txt": id, "new.txt": id}
	e, f, root := removalSetup(t, idents)
	seedCommittedFile(t, e, f, root, "old.txt", "mine", id)
	seedCommittedFile(t, e, f, root, "new.txt", "theirs", testIdentity(5))
	// new.txt 退回「本机不存在、远端被占」的冲突起点：删本机文件与记录，远端清单保留
	if err := os.Remove(filepath.Join(root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	delete(e.state.Records, "new.txt")
	delete(f.data, "new.txt") // 远端占位单独放回，身份与本机无关
	f.data["new.txt"] = []byte("theirs")
	f.versions["new.txt"] = 1
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "old.txt"), filepath.Join(root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		res, err := e.Tick(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && !hasKind(res, "rename-target-conflict") {
			t.Fatalf("应报 rename-target-conflict: %+v", res.Issues)
		}
	}
	if len(f.moves) != 0 || len(f.deletes) != 0 {
		t.Fatalf("目标被占时不得移动或删除: moves=%v deletes=%v", f.moves, f.deletes)
	}
	if e.state.Records["old.txt"] == nil {
		t.Fatal("old.txt 记录应保留")
	}
}

// 同步根为空而基线还在（重启后同步根丢失/被清空）：首轮拒绝一切删除推断，
// 之后按正常两轮确认回传——守卫挡的是挂载时刻，不是永久豁免。
func TestRemovalEmptyRootGuard(t *testing.T) {
	id := testIdentity(8)
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	f := &wbRemovalFake{wbFake: wbFake{data: map[string][]byte{}, versions: map[string]int64{}}}
	e, err := NewWritebackEngine(WritebackConfig{
		RootPath: root, StateDir: filepath.Join(base, "state"), Namespace: "server/account", Space: "personal",
		Client: f, Token: func() string { return "test" }, StableFor: time.Nanosecond,
		IdentityFor: func(string) (LocalFileIdentity, error) { return id, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	fakeHash := strings.Repeat("a", 64)
	e.state.Records["a.txt"] = &writebackRecord{FileID: 123, Version: 1, Hash: fakeHash, Size: 1, Identity: localIdentityOf(id)}
	e.state.Records["b.txt"] = &writebackRecord{FileID: 123, Version: 1, Hash: fakeHash, Size: 1, Identity: localIdentityOf(id)}
	// 远端清单里两个文件都还在（守卫场景＝本机根丢失，而非远端已删）
	f.data["a.txt"] = []byte("1")
	f.data["b.txt"] = []byte("2")
	f.versions["a.txt"] = 1
	f.versions["b.txt"] = 1
	if err := e.save(); err != nil {
		t.Fatal(err)
	}
	res1, err := e.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(res1, "sync-root-empty") || len(f.deletes) != 0 || len(f.trees) != 0 {
		t.Fatalf("首轮应报 sync-root-empty 且不删除: kinds=%v deletes=%v trees=%v", issueKinds(res1), f.deletes, f.trees)
	}
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 0 || len(f.trees) != 0 {
		t.Fatalf("确认窗口内不得删除: deletes=%v trees=%v", f.deletes, f.trees)
	}
	expireMissing(e, "a.txt", "b.txt")
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 2 || len(f.trees) != 0 {
		t.Fatalf("守卫后按两轮确认回传删除: deletes=%v trees=%v", f.deletes, f.trees)
	}
}

// 目录删除走 DeleteTree 且丢弃子树记录，子文件不再单独推断。
func TestRemovalDirectoryDeleteDropsDescendants(t *testing.T) {
	id := testIdentity(9)
	e, f, root := removalSetup(t, map[string]LocalFileIdentity{"d": id, "d/f.txt": testIdentity(10)})
	if err := os.MkdirAll(filepath.Join(root, "d"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "f.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("x"))
	f.data["d/f.txt"] = []byte("x")
	f.versions["d/f.txt"] = 1
	f.remoteDirs = []string{"d"}
	e.state.Records["d"] = &writebackRecord{Directory: true, FileID: 123, Version: 1, Identity: localIdentityOf(id)}
	e.state.Records["d/f.txt"] = &writebackRecord{FileID: 123, Version: 1, Hash: hex.EncodeToString(sum[:]), Size: 1}
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	expireMissing(e, "d", "d/f.txt")
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.trees) != 1 || f.trees[0] != "d" {
		t.Fatalf("目录应走一次 DeleteTree, got %v", f.trees)
	}
	if len(f.deletes) != 0 {
		t.Fatalf("子文件不应被单独推断删除: %v", f.deletes)
	}
	if e.state.Records["d"] != nil || e.state.Records["d/f.txt"] != nil {
		t.Fatal("子树记录应全部丢弃")
	}
}

// 目录改名：整棵子树记录改前缀，子文件不被重复上传。
func TestRemovalDirectoryRenameRepathsSubtree(t *testing.T) {
	id := testIdentity(11)
	child := testIdentity(12)
	idents := map[string]LocalFileIdentity{"d": id, "d/f.txt": child, "e": id, "e/f.txt": child}
	e, f, root := removalSetup(t, idents)
	if err := os.MkdirAll(filepath.Join(root, "d"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "f.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("x"))
	f.data["d/f.txt"] = []byte("x")
	f.versions["d/f.txt"] = 1
	f.remoteDirs = []string{"d"}
	e.state.Records["d"] = &writebackRecord{Directory: true, FileID: 123, Version: 1, Identity: localIdentityOf(id)}
	e.state.Records["d/f.txt"] = &writebackRecord{FileID: 123, Version: 1, Hash: hex.EncodeToString(sum[:]), Size: 1, Identity: localIdentityOf(child)}
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "d"), filepath.Join(root, "e")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.moves) != 1 || f.moves[0] != [2]string{"d", "e"} {
		t.Fatalf("应回传目录移动, got %v", f.moves)
	}
	if e.state.Records["e"] == nil || e.state.Records["e/f.txt"] == nil || e.state.Records["d"] != nil || e.state.Records["d/f.txt"] != nil {
		t.Fatalf("子树记录应改挂到 e/: %+v", e.state.Records)
	}
	if f.uploads != 0 {
		t.Fatalf("子文件不应重新上传: %d", f.uploads)
	}
}

// 无 DeleteMover 的调用方保持既有行为：只报 issue，不动远端。
func TestRemovalNoMoverKeepsLegacyBehavior(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	f := &wbFake{data: map[string][]byte{}, versions: map[string]int64{}}
	e, err := NewWritebackEngine(WritebackConfig{RootPath: root, StateDir: filepath.Join(base, "state"), Namespace: "n", Space: "personal", Client: f, Token: func() string { return "t" }, StableFor: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	e.state.Records["gone.txt"] = &writebackRecord{FileID: 123, Version: 1, Hash: strings.Repeat("d", 64), Size: 3}
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := e.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if e.state.Records["gone.txt"] == nil {
		t.Fatal("无回传能力时记录应保留")
	}
	if !hasKind(res, "missing-or-unreadable") {
		t.Fatalf("应保留既有 missing-or-unreadable 语义: %+v", res.Issues)
	}
}

// 移动失败不升级为删除：记录与保护保留，下一轮重试成功。
func TestRemovalMoveFailureNotUpgradedToDelete(t *testing.T) {
	id := testIdentity(13)
	idents := map[string]LocalFileIdentity{"old.txt": id, "new.txt": id}
	e, f, root := removalSetup(t, idents)
	seedCommittedFile(t, e, f, root, "old.txt", "data", id)
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.failMove = true
	if err := os.Rename(filepath.Join(root, "old.txt"), filepath.Join(root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := e.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.deletes) != 0 {
		t.Fatalf("移动失败不得升级为删除: %v", f.deletes)
	}
	if e.state.Records["old.txt"] == nil {
		t.Fatal("old.txt 记录应保留等待重试")
	}
	f.failMove = false
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.moves) != 1 {
		t.Fatalf("恢复后应完成移动: %v", f.moves)
	}
}

// 改名回传跨重启：记录持久化 NTFS 身份，重建引擎后仍能推断移动。
func TestRemovalRenameAcrossRestart(t *testing.T) {
	id := testIdentity(14)
	idents := map[string]LocalFileIdentity{"old.txt": id, "new.txt": id}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	newEngine := func() (*WritebackEngine, *wbRemovalFake) {
		t.Helper()
		f := &wbRemovalFake{wbFake: wbFake{data: map[string][]byte{}, versions: map[string]int64{}}}
		e, err := NewWritebackEngine(WritebackConfig{
			RootPath: root, StateDir: filepath.Join(base, "state"), Namespace: "server/account", Space: "personal",
			Client: f, Token: func() string { return "test" }, StableFor: time.Nanosecond,
			IdentityFor: func(p string) (LocalFileIdentity, error) {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return LocalFileIdentity{}, err
				}
				return idents[filepath.ToSlash(rel)], nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return e, f
	}
	e1, f1 := newEngine()
	seedCommittedFile(t, e1, f1, root, "old.txt", "keep", id)
	if _, err := e1.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e1.save(); err != nil { // 生产路径由身份补齐/基线变更触发落盘；种子记录需显式保存
		t.Fatal(err)
	}
	// 模拟重启：状态已落盘，用同一 StateDir 重建引擎；远端清单按已提交内容重建
	e2, f2 := newEngine()
	f2.data["old.txt"] = []byte("keep")
	f2.versions["old.txt"] = 1
	if e2.state.Records["old.txt"] == nil || e2.state.Records["old.txt"].Identity == nil {
		t.Fatal("重启后记录应含持久化身份")
	}
	if err := os.Rename(filepath.Join(root, "old.txt"), filepath.Join(root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f2.moves) != 1 {
		t.Fatalf("重启后应回传移动: %v", f2.moves)
	}
}
