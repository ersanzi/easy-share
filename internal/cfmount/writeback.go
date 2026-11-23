package cfmount

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"easyshare/internal/drive"
)

// WritebackClient 只使用带版本条件的上传；绝不退回无条件覆盖。
type WritebackClient interface {
	Objects(context.Context, string, string) ([]drive.Object, error)
	Open(context.Context, string, string, string) (io.ReadCloser, int64, error)
	UploadFileIfVersion(context.Context, string, string, string, string, *int64, drive.ProgressFunc) error
}

// DirectoryCreator 是可选目录能力。适配方须保证同名文件不能被目录覆盖。
type DirectoryCreator interface {
	CreateDirectory(context.Context, string, string, string) (*drive.Object, error)
}

// WritebackConfig 的 StateDir 必须位于同步根外，并由当前账号独占。
// Namespace 应包含服务器、账号的稳定 ID；不能使用登录令牌（不持久化凭据）。
type WritebackConfig struct {
	RootPath, StateDir, Space, Namespace string
	Client                               WritebackClient
	Token                                func() string
	StableFor                            time.Duration
	// CacheIdleFor 默认为五分钟；连续观察到可读且未变的文件才自动回收。
	CacheIdleFor time.Duration
	// TrustedDirectory is optional, fail-closed per-directory provenance verification.
	// Called only for readable ordinary directories with a unique exact remote
	// directory object. True removes ONLY that directory's subtree protection;
	// every existing file and unknown subdirectory retains its own protection.
	// Implementations must bind namespace/root, NTFS identity and committed provenance.
	TrustedDirectory func(context.Context, string, string, string, drive.Object) (bool, error)
	// OnStatus 只接收写回快照；不能重入引擎或阻塞上传与服务器校验。
	OnStatus func(WritebackStatus)
	// IdentityFor 可注入本机身份读取（默认 writebackFileIdentity）。仅测试使用。
	IdentityFor func(string) (LocalFileIdentity, error)
}

// DeleteMover 是可选的删除/移动回传能力（对标钉盘 renameDentry/moveDentry/deleteDentry）。
// 实现方必须保证：删除为软删（进云端回收站，可还原）、移动保留 fileId。
// drive.Client 的 Move/Delete/DeleteTree 与本接口签名一致，天然满足。
// 未实现时引擎保持既有「不推断删除/改名」行为，只报 issue。
type DeleteMover interface {
	Move(ctx context.Context, token, space, from, to string) error
	Delete(ctx context.Context, token, space, path string) error
	DeleteTree(ctx context.Context, token, space, path string) error
}

// localIdentity 是 NTFS 身份的可持久化形式（hex），改名/移动推断跨重启依赖它。
// nil 表示该记录尚无可信本机身份（旧记录或身份读取失败）。
type localIdentity struct {
	VolumeSerial string `json:"volumeSerial,omitempty"`
	FileID       string `json:"fileId,omitempty"`
}

func localIdentityOf(id LocalFileIdentity) *localIdentity {
	if id.VolumeSerial == 0 && id.FileID == ([16]byte{}) {
		return nil
	}
	return &localIdentity{VolumeSerial: hex.EncodeToString(binary.BigEndian.AppendUint64(nil, id.VolumeSerial)), FileID: hex.EncodeToString(id.FileID[:])}
}

type writebackRecord struct {
	FileID    int64             `json:"fileId,omitempty"`
	Version   int64             `json:"version"`
	Hash      string            `json:"hash"`
	Size      int64             `json:"size"`
	Directory bool              `json:"directory,omitempty"`
	Identity  *localIdentity    `json:"identity,omitempty"`
	Pending   *writebackPending `json:"pending,omitempty"`
}
type writebackPending struct {
	Snapshot  string `json:"snapshot"`
	Hash      string `json:"hash"`
	Size      int64  `json:"size"`
	Expected  int64  `json:"expected"`
	Attempted bool   `json:"attempted"`
}
type writebackState struct {
	Format                 int `json:"format"`
	Namespace, Root, Space string
	Records                map[string]*writebackRecord `json:"records"`
}
type localObservation struct {
	Size     int64
	Mod      time.Time
	Since    time.Time
	Identity LocalFileIdentity
}

// WritebackIssue 不会丢弃冲突文件；Path 始终指向原有本地内容。
type WritebackIssue struct{ Path, Kind, Message string }

// WritebackResult.Verified 只表示远端已读回校验；不等于可以无条件设置 CF_IN_SYNC。
// 上层设置占位符同步状态之前仍须锁住文件并核实其内容未继续变化。
type WritebackResult struct {
	Released       []string
	Verified       []string
	Issues         []WritebackIssue
	ProtectedPaths []string
}

// WritebackEngine 持久化意图和不可变上传快照。支持新增、编辑、目录创建，以及
// 基于 NTFS 身份连续性的删除/改名/移动回传（删除一律软删进云端回收站）；
// 不删除或改写任何用户本地文件。不支持多个进程共享同一 StateDir。
type WritebackEngine struct {
	mu                sync.Mutex
	cfg               WritebackConfig
	state             writebackState
	seen              map[string]localObservation
	missing           map[string]time.Time       // 连续缺席观察：路径 → 首次缺席时刻
	cacheProof        map[string]cacheLocalProof // 仅本轮完整快照匹配已提交版本的证据
	scannedOnce       bool                       // 首轮扫描是否完成（空根守卫只挡首轮）
	deletionProtected []string                   // 明确删除/还原未决路径，扫描差分不得接管
}

func NewWritebackEngine(cfg WritebackConfig) (*WritebackEngine, error) {
	if cfg.Client == nil || cfg.Token == nil || cfg.Namespace == "" {
		return nil, errors.New("cfmount writeback: client/token/namespace 必填")
	}
	root, err := filepath.Abs(cfg.RootPath)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	if cfg.RootPath == "" || cfg.StateDir == "" || pathWithin(root, dir) {
		return nil, errors.New("cfmount writeback: 状态目录必须在同步根之外")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("cfmount writeback: 同步根必须为真实目录")
	}
	cfg.RootPath, cfg.StateDir = root, dir
	if cfg.StableFor <= 0 {
		cfg.StableFor = 2 * time.Second
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	e := &WritebackEngine{cfg: cfg, seen: map[string]localObservation{}, missing: map[string]time.Time{}}
	e.state = writebackState{Format: 1, Namespace: cfg.Namespace, Root: root, Space: cfg.Space, Records: map[string]*writebackRecord{}}
	b, err := os.ReadFile(filepath.Join(dir, "pending.json"))
	if err == nil {
		if err = json.Unmarshal(b, &e.state); err != nil {
			return nil, fmt.Errorf("cfmount writeback: 状态损坏，禁止重建基线: %w", err)
		}
		if e.state.Format != 1 || e.state.Namespace != cfg.Namespace || e.state.Root != root || e.state.Space != cfg.Space || e.state.Records == nil {
			return nil, errors.New("cfmount writeback: 状态身份或格式不匹配")
		}
		for p, r := range e.state.Records {
			if !validWritebackPath(p) || r == nil {
				return nil, errors.New("cfmount writeback: 非法持久化路径")
			}
			if r.Pending != nil && filepath.Base(r.Pending.Snapshot) != r.Pending.Snapshot {
				return nil, errors.New("cfmount writeback: 非法快照路径")
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return e, nil
}

func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func validWritebackPath(p string) bool {
	return p != "" && p != "." && !strings.Contains(p, "\\") && !strings.Contains(p, ":") && !strings.HasPrefix(p, "/") && filepath.ToSlash(filepath.Clean(p)) == p && p != ".." && !strings.HasPrefix(p, "../")
}

// save 先 fsync 再原子替换；失败时停止本轮，不发起未持久化的上传。
func (e *WritebackEngine) save() error {
	b, err := json.MarshalIndent(e.state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(e.cfg.StateDir, "state-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := replaceWritebackState(name, filepath.Join(e.cfg.StateDir, "pending.json")); err != nil {
		return fmt.Errorf("cfmount writeback: 原子替换pending状态失败: %w", err)
	}
	return nil
}

// Reconcile 是桌面定时循环的唯一调度入口：先扫描/写回，再在同一互斥区内同步树。
// tree 必须保护 ProtectedPaths（包含不稳定/冲突/未水合文件），不得覆盖、删除或
// 标记这些路径为同步。即使云清单缺项也不能据此删除本地内容。tree 不得重入本引擎。
// 首次接入务必先调用本方法，不能先按远端清单重建树。
func (e *WritebackEngine) Reconcile(ctx context.Context, tree func(context.Context, []string) error, release ...CacheReleaser) (WritebackResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	result, err := e.tick(ctx)
	if err != nil {
		return result, err
	}
	if tree != nil {
		err = tree(ctx, append([]string(nil), result.ProtectedPaths...))
	}
	if err == nil && tree != nil && len(release) == 1 && release[0] != nil {
		e.releaseCache(ctx, &result, release[0])
	}
	return result, err
}

// Tick 适用于本轮不需要占位符树同步的调用方。
func (e *WritebackEngine) Tick(ctx context.Context) (WritebackResult, error) {
	return e.Reconcile(ctx, nil)
}

func (e *WritebackEngine) tick(ctx context.Context) (out WritebackResult, err error) {
	e.cacheProof = map[string]cacheLocalProof{}
	token := e.cfg.Token()
	if token == "" {
		return out, errors.New("cfmount writeback: 未登录")
	}
	local := map[string]os.FileInfo{}
	present := map[string]bool{}
	ordinaryDirectories := map[string]bool{}
	// 明确删除/还原保护在目标已缺席时仍有效；不可枚举目录的后代也不能算作删除。
	skippedSubtrees := append([]string(nil), e.deletionProtected...)
	out.ProtectedPaths = append(out.ProtectedPaths, e.deletionProtected...)
	err = filepath.WalkDir(e.cfg.RootPath, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == e.cfg.RootPath {
			return nil
		}
		rel, x := filepath.Rel(e.cfg.RootPath, p)
		if x != nil {
			return x
		}
		rel = filepath.ToSlash(rel)
		if !validWritebackPath(rel) {
			return fmt.Errorf("cfmount writeback: 不安全路径 %q", rel)
		}
		for _, blocked := range e.deletionProtected {
			if rel == blocked || strings.HasPrefix(rel, blocked+"/") {
				present[rel] = true
				out.ProtectedPaths = append(out.ProtectedPaths, blocked)
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		info, x := d.Info()
		if x != nil {
			return x
		}
		safe, cloudDirectory, x := writebackScanEntry(p, info)
		if x != nil {
			return x
		}
		present[rel] = true
		// 完整填充的 Cloud 目录由远端投影，不保护整棵子树：逐个保护其
		// 已存在文件，允许远端新增子项。普通本地目录及不可枚举目录仍整树保护。
		if !safe || !cloudDirectory {
			out.ProtectedPaths = append(out.ProtectedPaths, rel)
		}
		if safe && info.IsDir() && !cloudDirectory {
			ordinaryDirectories[rel] = true
		}
		if !safe {
			out.Issues = append(out.Issues, WritebackIssue{rel, "skipped", "占位符未完整水合、重解析点或非普通文件，未读取"})
			if d.IsDir() {
				skippedSubtrees = append(skippedSubtrees, rel)
				return filepath.SkipDir
			}
			return nil
		}
		local[rel] = info
		return nil
	})
	if err != nil {
		return out, fmt.Errorf("cfmount writeback: 扫描失败: %w", err)
	}
	firstScan := !e.scannedOnce
	e.scannedOnce = true
	for p, r := range e.state.Records {
		for _, blocked := range skippedSubtrees {
			if p == blocked || strings.HasPrefix(p, blocked+"/") || r.Directory && strings.HasPrefix(blocked, p+"/") {
				present[p] = true // 未观察不等于缺席，禁止移动或删除这些基线及受保护祖先。
				break
			}
		}
	}
	// 失去可读连续性（包括正文释放）后重置空闲计时。
	for p := range e.seen {
		if _, ok := local[p]; !ok {
			delete(e.seen, p)
		}
	}
	// 重新出现的路径清掉缺席计时；缺席计时只在引擎互斥区内使用。
	for p := range e.missing {
		if present[p] {
			delete(e.missing, p)
		}
	}
	objects, err := e.cfg.Client.Objects(ctx, token, e.cfg.Space)
	if err != nil {
		return out, err
	}
	remote := map[string]drive.Object{}
	remoteCount := map[string]int{}
	for _, o := range objects {
		remote[o.Path] = o
		remoteCount[o.Path]++
	}
	if verify := e.cfg.TrustedDirectory; verify != nil {
		protected := out.ProtectedPaths[:0]
		for _, p := range out.ProtectedPaths {
			o, exists := remote[p]
			trusted := false
			if ordinaryDirectories[p] && exists && remoteCount[p] == 1 && o.Directory && o.FileId > 0 {
				var verifyErr error
				trusted, verifyErr = verify(ctx, e.cfg.Namespace, e.cfg.RootPath, p, o)
				if verifyErr != nil {
					trusted = false
					out.Issues = append(out.Issues, WritebackIssue{p, "directory-provenance", verifyErr.Error()})
				}
			}
			if !trusted {
				protected = append(protected, p)
			}
		}
		out.ProtectedPaths = protected
	}
	// 删除/改名/移动回传（对标钉盘 renameDentry/moveDentry/deleteDentry）。
	// 改名解析必须先于上传循环：让目标 B 继承 A 的 fileId/版本，而不是把 B 当新文件重传。
	mover, hasMover := e.cfg.Client.(DeleteMover)
	// 空根守卫（只挡首轮扫描）：挂载时刻本地为空而基线还在，可能是同步根整体丢失或
	// 占位符尚未建起，此时任何删除推断都可能演变成整盘误删，全部拒绝并提示。
	// 首轮见到过内容之后的空根视为用户清空（可还原），按正常两轮确认回传。
	if firstScan && len(present) == 0 && len(e.state.Records) > 0 {
		out.Issues = append(out.Issues, WritebackIssue{".", "sync-root-empty",
			fmt.Sprintf("同步根为空但存在 %d 条基线，拒绝推断删除/移动；若确已清空请在网盘页操作", len(e.state.Records))})
		hasMover = false
	}
	noDelete := map[string]bool{}
	if hasMover {
		if err = e.propagateRenames(ctx, token, present, local, remote, mover, noDelete, &out); err != nil {
			return out, err
		}
	}
	paths := make([]string, 0, len(local))
	for p := range local {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		info := local[p]
		r := e.state.Records[p]
		issue := func(kind string, x error) {
			out.Issues = append(out.Issues, WritebackIssue{p, kind, x.Error()})
			phase := WritebackFailed
			if kind == "conflict" {
				phase = WritebackConflict
			}
			e.publishStatus(p, phase, info.Size(), 0, x.Error())
		}
		if info.IsDir() {
			if r != nil && r.Directory {
				// 目录记录补齐 NTFS 身份（只算一次；目录身份是改名回传的匹配键）。
				if r.Identity == nil {
					if dirID, idErr := e.identityFor(filepath.Join(e.cfg.RootPath, filepath.FromSlash(p))); idErr == nil {
						if id := localIdentityOf(dirID); id != nil {
							r.Identity = id
							if err = e.save(); err != nil {
								return out, err
							}
						}
					}
				}
				continue
			}
			// 显式目录或清单后代均可证明目录存在；同名文件必须报冲突。
			existing, exists := remote[p]
			if exists && !existing.Directory {
				issue("conflict", errors.New("远端同名路径是文件，禁止替换为目录"))
				continue
			}
			for rp := range remote {
				if strings.HasPrefix(rp, p+"/") {
					exists = true
					break
				}
			}
			if !exists {
				dc, ok := e.cfg.Client.(DirectoryCreator)
				if !ok {
					issue("unsupported-directory", errors.New("客户端未适配 CreateDirectory"))
					continue
				}
				e.publishStatus(p, WritebackPending, 0, 0, "等待创建云端目录")
				created, x := dc.CreateDirectory(ctx, token, e.cfg.Space, p)
				if x != nil {
					issue("directory", x)
					continue
				}
				if created == nil || !created.Directory || created.Path != p || created.FileId <= 0 {
					issue("directory", errors.New("CreateDirectory 未返回匹配路径及有效身份的持久化目录"))
					continue
				}
				remote[p] = *created
			}
			rec := &writebackRecord{Directory: true}
			if o, ok := remote[p]; ok && o.Directory && o.FileId > 0 {
				rec.FileID = o.FileId.Int64()
			}
			if dirID, idErr := e.identityFor(filepath.Join(e.cfg.RootPath, filepath.FromSlash(p))); idErr == nil {
				rec.Identity = localIdentityOf(dirID)
			}
			e.state.Records[p] = rec
			if err = e.save(); err != nil {
				return out, err
			}
			e.publishStatus(p, WritebackSynced, 0, 0, "云端目录已确认")
			continue
		}
		if o, exists := remote[p]; exists && o.Directory {
			issue("conflict", errors.New("远端同名路径是目录，禁止替换为文件"))
			continue
		}
		localID, x := e.identityFor(filepath.Join(e.cfg.RootPath, filepath.FromSlash(p)))
		if x != nil {
			issue("unstable", x)
			continue
		}
		old, ok := e.seen[p]
		now := time.Now()
		if !ok || old.Size != info.Size() || !old.Mod.Equal(info.ModTime()) || old.Identity != localID {
			e.seen[p] = localObservation{Size: info.Size(), Mod: info.ModTime(), Since: now, Identity: localID}
			e.publishStatus(p, WritebackPending, info.Size(), 0, "等待文件写入稳定")
			continue
		}
		if now.Sub(old.Since) < e.cfg.StableFor {
			e.publishStatus(p, WritebackPending, info.Size(), 0, "等待文件写入稳定")
			continue
		}
		if r != nil && r.Directory {
			issue("conflict", errors.New("已知目录变成文件，不支持类型替换"))
			continue
		}
		// 记录补齐 NTFS 身份：改名/移动回传跨重启依赖它；旧记录首个观察周期后补齐。
		if r != nil {
			if id := localIdentityOf(localID); id != nil && (r.Identity == nil || *r.Identity != *id) {
				r.Identity = id
				if err = e.save(); err != nil {
					return out, err
				}
			}
		}
		if remoteCount[p] > 1 {
			issue("conflict", errors.New("远端路径身份不唯一，保留本地内容"))
			continue
		}
		if r != nil && r.FileID == 0 && r.Version > 0 {
			// 旧记录即使已有新编辑，也先核实旧云版本，不能用路径/版本号猜同一文件。
			verified, verifyErr := e.verifyObject(ctx, token, p, r.Hash, r.Size)
			if verifyErr != nil || verified.VersionId.Int64() != r.Version {
				issue("conflict", errors.New("旧提交基线的远端身份无法重新确认，保留编辑与快照"))
				continue
			}
			r.FileID = verified.FileId.Int64()
			if err = e.save(); err != nil {
				r.FileID = 0
				return out, err
			}
		}
		if r != nil && r.FileID > 0 {
			o, exists := remote[p]
			if !exists || o.FileId.Int64() != r.FileID {
				issue("conflict", errors.New("远端文件身份已变化或缺失，保留原文件与基线"))
				continue
			}
		}
		var matched *writebackPending
		if r == nil || r.Pending == nil {
			pending, x := e.snapshot(p, info, localID)
			if x != nil {
				issue("unstable", x)
				continue
			}
			if r != nil && r.Hash == pending.Hash && r.Size == pending.Size {
				os.Remove(filepath.Join(e.cfg.StateDir, pending.Snapshot))
				o, exists := remote[p]
				if !exists || o.VersionId.Int64() != r.Version || (r.FileID > 0 && o.FileId.Int64() != r.FileID) {
					issue("conflict", errors.New("远端版本或身份与已提交基线不符，保留本地内容"))
					continue
				}
				e.cacheProof[p] = cacheLocalProof{Info: info, Identity: localID, Hash: pending.Hash, Size: pending.Size}
				e.publishStatus(p, WritebackSynced, pending.Size, pending.Size, "已确认的云端版本与本机正文一致")
				continue
			}
			if r == nil {
				if o, exists := remote[p]; exists {
					// 只有完整读回相同字节且身份/版本稳定才采纳初始基线。
					e.publishStatus(p, WritebackVerifying, pending.Size, pending.Size, "正在确认已有云端内容")
					verified, x := e.verifyObject(ctx, token, p, pending.Hash, pending.Size)
					os.Remove(filepath.Join(e.cfg.StateDir, pending.Snapshot))
					if x != nil || o.VersionId.Int64() <= 0 || o.FileId.Int64() != verified.FileId.Int64() || o.VersionId != verified.VersionId {
						issue("conflict", errors.New("已有本地内容没有可信基线，远端不同或无法校验；保留本地文件"))
						continue
					}
					e.state.Records[p] = &writebackRecord{FileID: verified.FileId.Int64(), Version: verified.VersionId.Int64(), Hash: pending.Hash, Size: pending.Size, Identity: localIdentityOf(localID)}
					if err = e.save(); err != nil {
						return out, err
					}
					out.Verified = append(out.Verified, p)
					e.cacheProof[p] = cacheLocalProof{Info: info, Identity: localID, Hash: pending.Hash, Size: pending.Size}
					e.publishStatus(p, WritebackSynced, pending.Size, pending.Size, "云端正文与版本已确认")
					continue
				}
				r = &writebackRecord{Identity: localIdentityOf(localID)}
				e.state.Records[p] = r
			}
			pending.Expected = r.Version
			r.Pending = pending
			matched = pending
			if err = e.save(); err != nil {
				return out, err
			}
		}
		q := r.Pending
		if q.Attempted {
			// 上次上传结果不明时先读回；不能因重启重试而覆盖更新的远端版本。
			e.publishStatus(p, WritebackVerifying, q.Size, q.Size, "重新确认上次上传结果")
			if verified, x := e.verifyObject(ctx, token, p, q.Hash, q.Size); x == nil && (r.FileID == 0 || verified.FileId.Int64() == r.FileID) {
				if err = e.commit(p, r, verified); err != nil {
					return out, err
				}
				out.Verified = append(out.Verified, p)
				e.publishStatus(p, WritebackSynced, q.Size, q.Size, "云端正文与版本已确认")
				continue
			}
		}
		o, exists := remote[p]
		if (q.Expected == 0 && exists) || (q.Expected > 0 && (!exists || o.VersionId.Int64() != q.Expected)) {
			issue("conflict", errors.New("远端版本已改变或缺失；保留原文件和待上传快照"))
			continue
		}
		snap := filepath.Join(e.cfg.StateDir, q.Snapshot)
		h, n, x := hashWritebackFile(snap)
		if x != nil || h != q.Hash || n != q.Size {
			issue("snapshot", errors.New("待上传快照缺失或损坏，禁止上传"))
			continue
		}
		q.Attempted = true
		if err = e.save(); err != nil {
			return out, err
		}
		e.publishStatus(p, WritebackUploading, q.Size, 0, "正在上传")
		uploadErr := e.cfg.Client.UploadFileIfVersion(ctx, token, e.cfg.Space, p, snap, &q.Expected, func(done, total int64) {
			e.publishStatus(p, WritebackUploading, total, done, "正在上传")
		})
		e.publishStatus(p, WritebackVerifying, q.Size, q.Size, "正在确认云端正文与版本")
		verified, verifyErr := e.verifyObject(ctx, token, p, q.Hash, q.Size)
		if verifyErr == nil && r.FileID > 0 && verified.FileId.Int64() != r.FileID {
			verifyErr = errors.New("上传确认的远端身份与原提交基线不符")
		}
		if verifyErr != nil {
			if uploadErr != nil {
				issue("upload", fmt.Errorf("上传失败: %v；远端校验: %w", uploadErr, verifyErr))
			} else {
				issue("verification", verifyErr)
			}
			continue
		}
		if err = e.commit(p, r, verified); err != nil {
			return out, err
		}
		out.Verified = append(out.Verified, p)
		e.publishStatus(p, WritebackSynced, q.Size, q.Size, "云端正文与版本已确认")
		if matched != nil {
			e.cacheProof[p] = cacheLocalProof{Info: info, Identity: localID, Hash: matched.Hash, Size: matched.Size}
		}
	}
	// 删除回传：缺席连续两轮才推断（第一轮只登记并保持保护），先核对远端身份再软删。
	// 一切失败都保留记录与保护，下一轮重试；远端清单是幂等自愈锚点（已删则远端缺席）。
	missingAfter := make([]string, 0, len(e.state.Records))
	pendingDeletion := map[string]bool{}
	for p, r := range e.state.Records {
		if r.Pending != nil {
			// 删除目录也不能顺带丢掉子项快照；与原生删除的 Pending 护栏保持一致。
			for q := p; q != "."; q = filepath.ToSlash(filepath.Dir(q)) {
				pendingDeletion[q] = true
			}
		}
		if !present[p] {
			missingAfter = append(missingAfter, p)
		}
	}
	sort.Strings(missingAfter)
	now := time.Now()
	for _, a := range missingAfter {
		r := e.state.Records[a]
		if r == nil {
			continue // 已被目录树删除/改名吸收
		}
		keep := true // 记录保留即保持保护，树同步不得据此重建或删除
		switch {
		case !hasMover:
			out.Issues = append(out.Issues, WritebackIssue{a, "missing-or-unreadable", "不推断本地删除或改名；不删除远端，也不按远端缺失删除本地"})
		case noDelete[a]:
			out.Issues = append(out.Issues, WritebackIssue{a, "rename-pending", "改名回传失败，本轮不升级为删除"})
		case pendingDeletion[a]:
			out.Issues = append(out.Issues, WritebackIssue{a, "delete-pending", "删除目标或子项存在未提交内容，保留基线与快照"})
		default:
			since, observed := e.missing[a]
			switch {
			case !observed:
				e.missing[a] = now
				out.Issues = append(out.Issues, WritebackIssue{a, "missing-observed", "本地缺席第 1 轮：下一轮仍缺席才回传删除"})
			case now.Sub(since) < e.cfg.StableFor:
				// 确认窗口内，先保持保护
			default:
				o, exists := remote[a]
				switch {
				case exists && r.FileID > 0 && o.FileId.Int64() != r.FileID:
					// 身份护栏：远端同路径已是别的文件（另一客户端删了 A 又传了新文件）时绝不代删。
					out.Issues = append(out.Issues, WritebackIssue{a, "delete-identity", "远端同路径文件身份与基线不符，拒绝回传删除"})
				case exists && r.FileID == 0:
					out.Issues = append(out.Issues, WritebackIssue{a, "delete-baseline", "记录没有已确认的远端基线，拒绝回传删除"})
				default:
					if exists {
						var delErr error
						if r.Directory {
							delErr = mover.DeleteTree(ctx, token, e.cfg.Space, a)
						} else {
							delErr = mover.Delete(ctx, token, e.cfg.Space, a)
						}
						if delErr != nil {
							out.Issues = append(out.Issues, WritebackIssue{a, "delete", fmt.Errorf("删除回传失败: %w", delErr).Error()})
							break
						}
						if r.Directory {
							// 服务端递归软删已覆盖子树；本地子树记录一并丢弃，防止下轮重复推断。
							prefix := a + "/"
							for p, cr := range e.state.Records {
								if strings.HasPrefix(p, prefix) {
									e.dropRecord(p, cr)
								}
							}
						}
					}
					// 远端已缺席（上次删除已生效、或从未上传成功）＝自愈：丢弃记录，停止保护。
					e.dropRecord(a, r)
					keep = false
					if err = e.save(); err != nil {
						return out, err
					}
				}
			}
		}
		if keep {
			out.ProtectedPaths = append(out.ProtectedPaths, a)
		}
	}
	sort.Strings(out.ProtectedPaths)
	return out, nil
}

// identityFor 读取本机身份；测试可注入。
func (e *WritebackEngine) identityFor(p string) (LocalFileIdentity, error) {
	if e.cfg.IdentityFor != nil {
		return e.cfg.IdentityFor(p)
	}
	return writebackFileIdentity(p)
}

// propagateRenames 用 NTFS 身份连续性把「记录 A 缺席 + 新路径 B 同身份同大小」解析为远端移动。
// 在上传循环之前执行；目标被占或移动失败时不动远端，交给删除路径的保守语义。
func (e *WritebackEngine) propagateRenames(ctx context.Context, token string, present map[string]bool, local map[string]os.FileInfo, remote map[string]drive.Object, mover DeleteMover, noDelete map[string]bool, out *WritebackResult) error {
	missing := make([]string, 0, len(e.state.Records))
	for p := range e.state.Records {
		if !present[p] {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	claimed := map[string]bool{}
	for _, a := range missing {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r := e.state.Records[a]
		if r == nil || r.Identity == nil {
			continue
		}
		b := e.matchRenameTarget(present, local, r, claimed)
		if b == "" {
			continue
		}
		o, aExists := remote[a]
		_, bExists := remote[b]
		if aExists && r.FileID > 0 && o.FileId.Int64() != r.FileID {
			// 远端同路径已是别的文件：这不是我们的移动，删除交给删除路径的身份护栏。
			continue
		}
		if bExists {
			// 目标被另一客户端占用：既不移动也不删除（B 以新文件身份上传会被 CAS 拒绝，
			// 删掉 A 只会让局面更糟）。保留 A 与保护，提示用户在网盘页解决同名冲突。
			out.Issues = append(out.Issues, WritebackIssue{a, "rename-target-conflict",
				fmt.Sprintf("改名目标 %s 在云端已存在同名文件，保留双方，请在网盘页处理", b)})
			noDelete[a] = true
			continue
		}
		if aExists {
			if err := mover.Move(ctx, token, e.cfg.Space, a, b); err != nil {
				out.Issues = append(out.Issues, WritebackIssue{a, "move", fmt.Errorf("改名回传失败: %w", err).Error()})
				noDelete[a] = true // 移动失败不升级为删除
				continue
			}
		}
		// 远端源已缺席＝上次移动已生效但本机状态未及持久化，以远端清单为锚自愈采纳。
		e.adoptRenamed(a, b, r, remote, claimed)
		if err := e.save(); err != nil {
			return err
		}
	}
	return nil
}

// matchRenameTarget 在本轮新出现的路径里找身份连续的改名目标。
// 要求类型一致、文件大小一致——大小不同说明改名后已编辑，退化为删+建更诚实。
func (e *WritebackEngine) matchRenameTarget(present map[string]bool, local map[string]os.FileInfo, r *writebackRecord, claimed map[string]bool) string {
	candidates := make([]string, 0, len(present))
	for p := range present {
		if _, known := e.state.Records[p]; known || claimed[p] {
			continue
		}
		candidates = append(candidates, p)
	}
	sort.Strings(candidates)
	for _, p := range candidates {
		info, ok := local[p]
		if !ok || info.IsDir() != r.Directory {
			continue
		}
		if !r.Directory && r.Size != info.Size() {
			continue
		}
		id, err := e.identityFor(filepath.Join(e.cfg.RootPath, filepath.FromSlash(p)))
		if err != nil {
			continue
		}
		if cand := localIdentityOf(id); cand != nil && *cand == *r.Identity {
			return p
		}
	}
	return ""
}

// adoptRenamed 把 A 的记录改挂到 B，并同步改写本轮远端映射、缺席计时、seen 与子树前缀，
// 让本轮后续处理与保护树同步看到一致的移动结果。
func (e *WritebackEngine) adoptRenamed(a, b string, r *writebackRecord, remote map[string]drive.Object, claimed map[string]bool) {
	delete(e.state.Records, a)
	e.state.Records[b] = r
	delete(e.missing, a)
	delete(e.seen, a)
	claimed[b] = true
	if o, ok := remote[a]; ok {
		delete(remote, a)
		remote[b] = o
	}
	if !r.Directory {
		return
	}
	prefix, newPrefix := a+"/", b+"/"
	for p, cr := range e.state.Records {
		if strings.HasPrefix(p, prefix) {
			delete(e.state.Records, p)
			e.state.Records[newPrefix+p[len(prefix):]] = cr
		}
	}
	for p, o := range remote {
		if strings.HasPrefix(p, prefix) {
			delete(remote, p)
			remote[newPrefix+p[len(prefix):]] = o
		}
	}
	for p, t := range e.missing {
		if strings.HasPrefix(p, prefix) {
			delete(e.missing, p)
			e.missing[newPrefix+p[len(prefix):]] = t
		}
	}
	for p, obs := range e.seen {
		if strings.HasPrefix(p, prefix) {
			delete(e.seen, p)
			e.seen[newPrefix+p[len(prefix):]] = obs
		}
	}
}

// dropRecord 丢弃一条记录及其缺席计时；待上传快照是引擎自己的临时文件，一并清理。
func (e *WritebackEngine) dropRecord(p string, r *writebackRecord) {
	if r != nil && r.Pending != nil {
		os.Remove(filepath.Join(e.cfg.StateDir, r.Pending.Snapshot))
	}
	delete(e.state.Records, p)
	delete(e.missing, p)
}

func (e *WritebackEngine) commit(p string, r *writebackRecord, verified drive.Object) error {
	q := r.Pending
	old := *r
	r.FileID = verified.FileId.Int64()
	r.Version = verified.VersionId.Int64()
	r.Hash = q.Hash
	r.Size = q.Size
	r.Pending = nil
	if err := e.save(); err != nil {
		*r = old
		return err
	}
	// 仅清理我们自己的快照，绝不删除用户文件。
	if err := os.Remove(filepath.Join(e.cfg.StateDir, q.Snapshot)); err != nil {
		return fmt.Errorf("cfmount writeback: 清理已确认快照失败: %w", err)
	}
	return nil
}

func hashWritebackFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func (e *WritebackEngine) snapshot(p string, before os.FileInfo, identity LocalFileIdentity) (*writebackPending, error) {
	src := filepath.Join(e.cfg.RootPath, filepath.FromSlash(p))
	safe, err := writebackReadable(src, before)
	if err != nil {
		return nil, err
	}
	if !safe {
		return nil, errors.New("文件不可安全读取")
	}
	f, err := openWritebackLocal(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return nil, err
	}
	actualID, err := writebackHandleIdentity(f)
	if err != nil {
		return nil, err
	}
	if actualID != identity || !os.SameFile(before, actual) {
		return nil, errors.New("扫描后文件身份改变")
	}
	dst, err := os.CreateTemp(e.cfg.StateDir, "payload-*")
	if err != nil {
		return nil, err
	}
	name := dst.Name()
	keep := false
	defer func() {
		dst.Close()
		if !keep {
			os.Remove(name)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), f)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(src)
	if err != nil {
		return nil, err
	}
	currentID, err := writebackFileIdentity(src)
	if err != nil {
		return nil, err
	}
	if currentID != identity || n != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !os.SameFile(after, current) || !current.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("复制期间文件改变")
	}
	if err = dst.Sync(); err != nil {
		return nil, err
	}
	if err = dst.Close(); err != nil {
		return nil, err
	}
	keep = true
	return &writebackPending{Snapshot: filepath.Base(name), Hash: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

// verifyObject 总是读回完整内容，并核对读回前后的稳定 fileId/版本；清单哈希不能代替正文。
func (e *WritebackEngine) verifyObject(ctx context.Context, token, p, hash string, size int64) (drive.Object, error) {
	find := func() (drive.Object, error) {
		objects, err := e.cfg.Client.Objects(ctx, token, e.cfg.Space)
		if err != nil {
			return drive.Object{}, err
		}
		var found drive.Object
		count := 0
		for _, o := range objects {
			if o.Path == p {
				found = o
				count++
			}
		}
		if count != 1 || found.Directory || found.FileId <= 0 || found.VersionId.Int64() <= 0 {
			return drive.Object{}, errors.New("远端对象不存在、身份不唯一或无有效版本")
		}
		return found, nil
	}
	a, err := find()
	if err != nil {
		return drive.Object{}, err
	}
	if a.Size != size {
		return drive.Object{}, errors.New("远端大小不符")
	}
	f, _, err := e.cfg.Client.Open(ctx, token, e.cfg.Space, p)
	if err != nil {
		return drive.Object{}, err
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, size+1))
	closeErr := f.Close()
	if err != nil {
		return drive.Object{}, err
	}
	if closeErr != nil {
		return drive.Object{}, closeErr
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		return drive.Object{}, errors.New("远端内容读回哈希不符")
	}
	b, err := find()
	if err != nil {
		return drive.Object{}, err
	}
	if b.FileId != a.FileId || b.VersionId != a.VersionId || b.Size != a.Size {
		return drive.Object{}, errors.New("远端校验期间身份或版本改变")
	}
	return b, ctx.Err()
}
