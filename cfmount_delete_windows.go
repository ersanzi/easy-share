//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

// 仅接受带稳定身份、版本和操作号的回收协议，绝不退回按同名路径删除。
type nativeDeleteClient interface {
	DeleteOperation(context.Context, string, string, int64, string, *int64, string, bool) (*drive.RecycleOperationResult, error)
	QueryRecycleOperation(context.Context, string, string, int64, string) (*drive.RecycleOperationResult, error)
	RestoreOperation(context.Context, string, string, int64) (*drive.RecycleOperationResult, error)
}

type nativeDeleteProofMember struct {
	Member                cfmount.DeleteMember
	LocalHash, RemoteHash string
	LocalSize             int64
	FullContent           bool
	// 原写回状态只留证；上传快照始终由原引擎保管，不重新提交或删除。
	Writeback json.RawMessage `json:",omitempty"`
}

type nativeDeleteProof struct {
	Format                              int
	Namespace, Root, Space, OperationID string
	RootIdentity                        cfapi.DirectoryNTFSIdentity
	Members                             []nativeDeleteProofMember
}

// 同一运行域只有一个 store；不启动后台线程、不扫描同步根的无关子树。
// 主线须用同一调度互斥区串行 Before、BeforeReconcile 和 engine.Tick。
// engine getter 必须只读当前指针，不能重入上述互斥区或引擎回调。
type nativeDeleteCoordinator struct {
	menuMu                     sync.Mutex
	menuRoots                  []string
	mu                         sync.Mutex
	cfg                        cfmount.Config
	stateDir, namespace, token string
	rootIdentity               cfapi.DirectoryNTFSIdentity
	store                      *cfmount.DeleteStateStore
	client                     nativeDeleteClient
	engine                     func() *cfmount.WritebackEngine
	mount                      *cfapi.Mount
	queryStatus                func(*cfapi.Mount, string) (cfapi.PlaceholderStatus, error)
	candidates                 map[string]bool
	issues                     map[string]error
	onChanged                  func()          // 仅在已核实服务器生命周期改变后发布刷新信号
	projectionPaths            map[string]bool // 已确认服务器删除的本机投影操作，不再回环请求服务器
}

func newNativeDeleteCoordinator(cfg cfmount.Config, stateDir, namespace string, engine func() *cfmount.WritebackEngine) (*nativeDeleteCoordinator, error) {
	if cfg.Client == nil || cfg.Token == nil || engine == nil || namespace == "" || cfg.RootPath == "" {
		return nil, fmt.Errorf("原生删除配置不完整")
	}
	client, ok := cfg.Client.(nativeDeleteClient)
	if !ok {
		return nil, fmt.Errorf("客户端缺少安全回收/查询/还原能力")
	}
	root, err := filepath.Abs(cfg.RootPath)
	if err != nil {
		return nil, err
	}
	cfg.RootPath = root
	if err := nativeDeleteSafeRoot(root); err != nil {
		return nil, err
	}
	id, err := cfapi.ReadDirectoryNTFSIdentity(root)
	if err != nil {
		return nil, fmt.Errorf("读取删除同步根身份: %w", err)
	}
	token := cfg.Token()
	if token == "" {
		return nil, fmt.Errorf("原生删除未登录")
	}
	store, err := cfmount.NewDeleteStateStore(root, stateDir, namespace)
	if err != nil {
		return nil, err
	}
	stateDir, err = filepath.Abs(stateDir)
	if err != nil {
		return nil, err
	}
	return &nativeDeleteCoordinator{cfg: cfg, stateDir: stateDir, namespace: namespace, token: token, rootIdentity: id, store: store, client: client, engine: engine,
		queryStatus: func(m *cfapi.Mount, path string) (cfapi.PlaceholderStatus, error) {
			if m == nil {
				return cfapi.PlaceholderStatus{}, fmt.Errorf("删除同步根尚未完成挂载")
			}
			return m.QueryDeletionStatus(path)
		}, candidates: map[string]bool{}, issues: map[string]error{}, projectionPaths: map[string]bool{}}, nil
}

// 可在 Create 前取得回调；挂载后以 Callbacks(m) 或 BeforeReconcile(ctx,m) 绑定。
// 未绑定 mount/未初始化 engine 时明确拒绝 Before，不执行任何云端写操作。
func (c *nativeDeleteCoordinator) Callbacks(m ...*cfapi.Mount) *cfapi.OperationCallbacks {
	if len(m) != 0 {
		c.mu.Lock()
		c.mount = m[0]
		c.mu.Unlock()
	}
	return &cfapi.OperationCallbacks{Before: c.Before, Completed: c.Completed, Result: c.Result}
}

func (c *nativeDeleteCoordinator) guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.cfg.Token() == "" || c.cfg.Token() != c.token {
		return fmt.Errorf("删除会话令牌已更换；请重新绑定当前账号挂载")
	}
	if err := nativeDeleteSafeRoot(c.cfg.RootPath); err != nil {
		return err
	}
	id, err := cfapi.ReadDirectoryNTFSIdentity(c.cfg.RootPath)
	if err != nil || id != c.rootIdentity {
		return fmt.Errorf("删除同步根 NTFS 身份已变化")
	}
	return nil
}

func nativeDeleteSafeRoot(root string) error {
	if !filepath.IsAbs(root) || len(filepath.VolumeName(root)) != 2 || strings.HasPrefix(root, `\\`) || filepath.Dir(root) == root {
		return fmt.Errorf("删除仅支持非卷根的本机绝对同步根")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("删除同步根不是安全真实目录")
	}
	return nil
}

func nativeDeletePathWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func nativeDeleteOperationID() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return "nd-" + hex.EncodeToString(nonce[:]), nil
}

// Before在原生删除等待阶段确认服务器结果；undelete也出现在Shell回收准备中，不能直接还原云端。
func (c *nativeDeleteCoordinator) Before(ctx context.Context, req cfapi.OperationRequest) error {
	return c.before(ctx, req, false)
}

func (c *nativeDeleteCoordinator) before(ctx context.Context, req cfapi.OperationRequest, clientProjection bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.guard(ctx); err != nil {
		return err
	}
	if req.Kind == cfapi.CallbackTypeNotifyRename {
		return c.beforeRename(req)
	}
	if req.Kind != cfapi.CallbackTypeNotifyDelete || req.Flags&cfapi.OperationIsUndelete != 0 {
		return nil
	}
	path, ok := cfapi.RelativeOperationPath(c.cfg.RootPath, req.Path)
	if !ok {
		return fmt.Errorf("拒绝删除同步根或根外路径")
	}
	for root := range c.projectionPaths {
		if nativeDeletePathWithin(path, root) {
			return nil
		}
	}
	for _, prior := range c.store.Records() {
		if prior.Status == "recycled" && nativeDeletePathWithin(path, prior.Path) {
			return nil
		}
	}
	engine := c.engine()
	if engine == nil || c.mount == nil {
		return fmt.Errorf("删除适配尚未完成初始化")
	}
	// 先与上传串行并保护目标，才读取远端版本；晚到的上传不能复活删除对象。
	protected := append(c.store.ActivePaths(), path)
	if err := engine.ProtectDeletion(protected); err != nil {
		return err
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = engine.ProtectDeletion(c.store.ActivePaths())
		}
	}()
	objects, err := c.cfg.Client.Objects(ctx, c.token, c.cfg.Space)
	if err != nil {
		return fmt.Errorf("删除前读取服务器失败:%w", err)
	}
	var target *drive.Object
	for i := range objects {
		if objects[i].Path == path {
			if target != nil {
				return fmt.Errorf("服务器文件身份不唯一")
			}
			target = &objects[i]
		}
	}
	if target == nil {
		// 未上传普通文件没有cloud回调；仅有登记却未提交正文时不得误删远端对象。
		return fmt.Errorf("服务器文件尚未确认；保留本机文件后重试")
	}
	if req.Flags&cfapi.OperationIsDirectory == 0 {
		for _, root := range c.menuRoots {
			if nativeDeletePathWithin(path, root) && root != path {
				for i := range objects {
					if objects[i].Path == root && objects[i].Directory {
						target = &objects[i]
						path = root
						break
					}
				}
			}
		}
	}
	if !target.Directory && !bytes.Equal(req.CloudIdentity, cfapi.EncodeIdentity(target.FileId.Int64())) {
		return fmt.Errorf("删除通知的文件身份与服务器不一致")
	}
	if target.FileId <= 0 || target.VersionId < 0 {
		return fmt.Errorf("删除目标缺少服务器身份或版本")
	}
	local, err := c.queryStatus(c.mount, path)
	if err != nil {
		return fmt.Errorf("删除前本机元数据确认:%w", err)
	}
	if local.Directory != target.Directory {
		return fmt.Errorf("删除目标的文件/目录类型变化")
	}
	same, err := cfapi.ReadDirectoryNTFSIdentity(filepath.Join(c.cfg.RootPath, filepath.FromSlash(path)))
	if err != nil || same != local.LocalIdentity {
		return fmt.Errorf("删除句柄与本机目标不一致")
	}
	var announced [16]byte
	binary.LittleEndian.PutUint64(announced[:8], uint64(req.FileID))
	if req.FileID != 0 && req.Path == filepath.Join(c.cfg.RootPath, filepath.FromSlash(path)) && announced != local.LocalIdentity.FileID {
		return fmt.Errorf("删除通知NTFS身份不符，未删除")
	}
	operation, err := nativeDeleteOperationID()
	if err != nil {
		return err
	}
	record := cfmount.DeleteRecord{OperationID: operation, Path: path, RemoteID: target.FileId.Int64(), Version: target.VersionId.Int64(),
		Directory: target.Directory, LocalIdentity: nativeDeleteLocalIdentity(local.LocalIdentity), RemoteCommitted: true, Status: "preparing", ClientProjection: clientProjection}
	proof := nativeDeleteProof{Format: 1, Namespace: c.namespace, Root: c.cfg.RootPath, Space: c.cfg.Space, OperationID: operation, RootIdentity: c.rootIdentity}
	for _, object := range objects {
		if !nativeDeletePathWithin(object.Path, path) {
			continue
		}
		status, err := c.queryStatus(c.mount, object.Path)
		if err != nil {
			return fmt.Errorf("删除子项身份确认 %s:%w", object.Path, err)
		}
		member := cfmount.DeleteMember{Path: object.Path, RemoteID: object.FileId.Int64(), Version: object.VersionId.Int64(), Size: object.Size,
			Directory: object.Directory, LocalIdentity: nativeDeleteLocalIdentity(status.LocalIdentity)}
		evidence := nativeDeleteProofMember{Member: member, FullContent: status.FullContent}
		if !object.Directory {
			remoteHash, _, err := c.hashRemote(ctx, object)
			if err != nil {
				return err
			}
			evidence.RemoteHash = remoteHash
			if status.FullContent || !status.Placeholder {
				evidence.LocalHash, evidence.LocalSize, err = nativeDeleteHashLocal(c.cfg.RootPath, object.Path, member.LocalIdentity)
				if err != nil {
					return err
				}
			}
		}
		evidence.Writeback, err = engine.DeletionStateCopy(object.Path)
		if err != nil {
			return err
		}
		record.Members = append(record.Members, member)
		proof.Members = append(proof.Members, evidence)
	}
	if len(record.Members) == 0 {
		return fmt.Errorf("删除批次没有明确成员")
	}
	if err := c.saveProof(proof); err != nil {
		return err
	}
	if err := c.store.Put(record); err != nil {
		return err
	}
	prepared = true
	if err := engine.ProtectDeletion(c.store.ActivePaths()); err != nil {
		return err
	}
	result, err := c.client.DeleteOperation(ctx, c.token, c.cfg.Space, record.RemoteID, path, &record.Version, operation, target.Directory)
	if err != nil {
		checked, checkErr := c.client.QueryRecycleOperation(ctx, c.token, c.cfg.Space, record.RemoteID, operation)
		if checkErr != nil || checked.Status != "recycled" {
			c.issues[path] = err
			return err
		}
		result = checked
	}
	if result == nil || result.Status != "recycled" || result.FileId != strconv.FormatInt(record.RemoteID, 10) || result.Path != path || result.Directory != record.Directory {
		return fmt.Errorf("服务器未返回准确删除确认")
	}
	record.BatchID = result.BatchId
	record.Status = "recycled"
	if err := c.store.Put(record); err != nil {
		return err
	}
	delete(c.issues, path)
	if c.onChanged != nil {
		c.onChanged()
	}
	return nil
}

func nativeDeleteLocalIdentity(id cfapi.DirectoryNTFSIdentity) cfmount.LocalFileIdentity {
	return cfmount.LocalFileIdentity{VolumeSerial: id.VolumeSerial, FileID: id.FileID}
}

func (c *nativeDeleteCoordinator) beforeRename(req cfapi.OperationRequest) error {
	source, sourceOK := cfapi.RelativeOperationPath(c.cfg.RootPath, req.Path)
	_, targetOK := cfapi.RelativeOperationPath(c.cfg.RootPath, req.TargetPath)
	if sourceOK && !targetOK {
		if strings.Contains(strings.ToLower(req.TargetPath), `\$recycle.bin\`) {
			return nil
		}
		return fmt.Errorf("云盘移出属于移动操作，本轮未接入；请复制后删除")
	}
	if sourceOK && targetOK {
		return nil
	}
	if !sourceOK && targetOK {
		return nil
	}
	_ = source
	return fmt.Errorf("原生移动通知不属于当前同步根")
}

func (c *nativeDeleteCoordinator) Completed(req cfapi.OperationRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path, ok := cfapi.RelativeOperationPath(c.cfg.RootPath, req.Path)
	movedToRecycle := req.Kind == cfapi.CallbackTypeNotifyRenameCompletion && strings.Contains(strings.ToLower(req.Path), `\$recycle.bin\`)
	if !ok && movedToRecycle {
		path, ok = cfapi.RelativeOperationPath(c.cfg.RootPath, req.SourcePath)
	}
	if ok {
		c.candidates[path] = true
		if record, found := c.store.Get(path); found && record.Status == "recycled" {
			_, statErr := os.Lstat(filepath.Join(c.cfg.RootPath, filepath.FromSlash(path)))
			if req.Kind == cfapi.CallbackTypeNotifyDeleteCompletion || movedToRecycle || os.IsNotExist(statErr) {
				record.LocalRemoved = true
				if err := c.store.Put(record); err != nil {
					c.issues[path] = err
				}
			}
		}
	}
}
func (c *nativeDeleteCoordinator) Result(req cfapi.OperationRequest, err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if path, ok := cfapi.RelativeOperationPath(c.cfg.RootPath, req.Path); ok {
		c.issues[path] = err
		if record, found := c.store.Get(path); found && record.Status != "restored" {
			record.LocalFailure = true
			if saveErr := c.store.Put(record); saveErr != nil {
				c.issues[path] = errors.Join(err, saveErr)
			}
		}
	}
}

func (c *nativeDeleteCoordinator) saveProof(proof nativeDeleteProof) error {
	b, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(c.stateDir, "delete-proof-"+proof.OperationID+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(b)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	return errors.Join(err, closeErr)
}

func (c *nativeDeleteCoordinator) hashRemote(ctx context.Context, object drive.Object) (string, int64, error) {
	stream, n, err := c.cfg.Client.Open(ctx, c.token, c.cfg.Space, object.Path)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	read, readErr := io.Copy(h, io.LimitReader(stream, object.Size+1))
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || read != object.Size || n >= 0 && n != object.Size {
		return "", 0, fmt.Errorf("删除前服务器正文核验失败 %s:%w", object.Path, errors.Join(readErr, closeErr))
	}
	return hex.EncodeToString(h.Sum(nil)), read, nil
}

func nativeDeleteHashLocal(root, path string, identity cfmount.LocalFileIdentity) (string, int64, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	name, err := windows.UTF16PtrFromString(full)
	if err != nil {
		return "", 0, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_NO_RECALL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", 0, fmt.Errorf("删除前文件占用，保留本机内容:%w", err)
	}
	file := os.NewFile(uintptr(h), full)
	defer file.Close()
	var actual cfapi.DirectoryNTFSIdentity
	if err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&actual)), uint32(unsafe.Sizeof(actual))); err != nil {
		return "", 0, err
	}
	if nativeDeleteLocalIdentity(actual) != identity {
		return "", 0, fmt.Errorf("删除前本机文件身份已变化")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, file)
	return hex.EncodeToString(hash.Sum(nil)), n, err
}

// BeforeReconcile必须先于普通写回扫描运行；精确原NTFS文件恢复才允许同服务器身份还原。
func (c *nativeDeleteCoordinator) BeforeReconcile(ctx context.Context, m *cfapi.Mount) error {
	var projectionIssues []error
	for _, record := range c.store.Records() {
		if record.ClientProjection && record.Status == "recycled" && !record.LocalRemoved {
			// 原生移除会同步调用 Before；不能持协调器锁跨文件删除。
			if err := c.finishClientProjection(ctx, record); err != nil {
				projectionIssues = append(projectionIssues, err)
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mount = m
	if err := c.guard(ctx); err != nil {
		return err
	}
	engine := c.engine()
	if engine == nil {
		return fmt.Errorf("删除写回引擎未初始化")
	}
	issues := projectionIssues
	for _, record := range c.store.Records() {
		if record.Status == "restored" || record.Status == "cancelled" || record.ClientProjection && !record.LocalRemoved {
			continue
		}
		full := filepath.Join(c.cfg.RootPath, filepath.FromSlash(record.Path))
		info, statErr := os.Lstat(full)
		if os.IsNotExist(statErr) {
			if !record.LocalRemoved {
				record.LocalRemoved = true
				if err := c.store.Put(record); err != nil {
					return err
				}
			}
			continue
		}
		if statErr != nil {
			issues = append(issues, statErr)
			continue
		}
		if !record.LocalRemoved && record.Status != "preparing" && !record.LocalFailure && c.issues[record.Path] == nil {
			continue // 本机回收尚在执行；失败/超时记录即使本机未移除也必须补偿。
		}
		if info.IsDir() != record.Directory {
			issues = append(issues, fmt.Errorf("恢复目标类型变化 %s", record.Path))
			continue
		}
		actual, err := cfapi.ReadDirectoryNTFSIdentity(full)
		if err != nil || nativeDeleteLocalIdentity(actual) != record.LocalIdentity {
			issues = append(issues, fmt.Errorf("同名新文件不是原删除对象 %s", record.Path))
			continue
		}
		proofPath := filepath.Join(c.stateDir, "delete-proof-"+record.OperationID+".json")
		b, err := os.ReadFile(proofPath)
		if err != nil {
			issues = append(issues, err)
			continue
		}
		var proof nativeDeleteProof
		if err = json.Unmarshal(b, &proof); err != nil || proof.Namespace != c.namespace || proof.Root != c.cfg.RootPath || proof.RootIdentity != c.rootIdentity || proof.OperationID != record.OperationID || proof.Space != c.cfg.Space || len(proof.Members) != len(record.Members) {
			issues = append(issues, fmt.Errorf("删除恢复证据身份不一致 %s", record.Path))
			continue
		}
		ready := true
		for _, evidence := range proof.Members {
			member := evidence.Member
			id, err := cfapi.ReadDirectoryNTFSIdentity(filepath.Join(c.cfg.RootPath, filepath.FromSlash(member.Path)))
			if err != nil || nativeDeleteLocalIdentity(id) != member.LocalIdentity {
				ready = false
				break
			}
			if evidence.LocalHash != "" && !member.Directory {
				hash, n, err := nativeDeleteHashLocal(c.cfg.RootPath, member.Path, member.LocalIdentity)
				if err != nil || hash != evidence.LocalHash || n != evidence.LocalSize {
					ready = false
					break
				}
			}
		}
		if !ready {
			issues = append(issues, fmt.Errorf("等待Windows恢复完整原目录/正文 %s", record.Path))
			continue
		}
		if record.Status == "preparing" {
			checked, err := c.client.QueryRecycleOperation(ctx, c.token, c.cfg.Space, record.RemoteID, record.OperationID)
			if err != nil {
				issues = append(issues, err)
				continue
			}
			if checked == nil || (checked.Status != "recycled" && checked.Status != "restored") || checked.FileId != strconv.FormatInt(record.RemoteID, 10) || checked.Path != record.Path || checked.Directory != record.Directory || checked.OperationId != record.OperationID || checked.MemberCount != len(record.Members) {
				issues = append(issues, fmt.Errorf("删除操作仍待服务器确认 %s", record.Path))
				continue
			}
			record.BatchID = checked.BatchId
		}
		record.Status = "restoring"
		if err := c.store.Put(record); err != nil {
			return err
		}
		result, err := c.client.RestoreOperation(ctx, c.token, c.cfg.Space, record.RemoteID)
		if err != nil {
			issues = append(issues, err)
			continue
		}
		if result == nil || result.Status != "restored" || result.FileId != strconv.FormatInt(record.RemoteID, 10) || result.Path != record.Path || result.Directory != record.Directory || result.MemberCount != len(record.Members) {
			issues = append(issues, fmt.Errorf("服务器还原身份不一致 %s", record.Path))
			continue
		}
		objects, err := c.cfg.Client.Objects(ctx, c.token, c.cfg.Space)
		if err != nil {
			issues = append(issues, err)
			continue
		}
		remote := map[string]drive.Object{}
		for _, o := range objects {
			remote[o.Path] = o
		}
		for _, evidence := range proof.Members {
			member := evidence.Member
			o, ok := remote[member.Path]
			if !ok || o.FileId.Int64() != member.RemoteID || o.VersionId.Int64() != member.Version || o.Directory != member.Directory {
				ready = false
				break
			}
			if !member.Directory {
				hash, _, err := c.hashRemote(ctx, o)
				if err != nil || hash != evidence.RemoteHash {
					ready = false
					break
				}
			}
		}
		if !ready {
			issues = append(issues, fmt.Errorf("服务器原批次正文尚未完整恢复 %s", record.Path))
			continue
		}
		record.Status = "restored"
		if err := c.store.Put(record); err != nil {
			return err
		}
		for _, member := range record.Members {
			if err := engine.RetainRestoredBaseline(member.Path); err != nil {
				return err
			}
		}
		delete(c.issues, record.Path)
		if c.onChanged != nil {
			c.onChanged()
		}
	}
	if err := engine.ProtectDeletion(c.store.ActivePaths()); err != nil {
		return err
	}
	for _, err := range c.issues {
		issues = append(issues, err)
	}
	return errors.Join(issues...)
}
