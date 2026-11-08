package cfmount

import (
	"context"
	"crypto/sha256"
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
}

type writebackRecord struct {
	FileID    int64             `json:"fileId,omitempty"`
	Version   int64             `json:"version"`
	Hash      string            `json:"hash"`
	Size      int64             `json:"size"`
	Directory bool              `json:"directory,omitempty"`
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

// WritebackEngine 持久化意图和不可变上传快照。仅支持新增、编辑和目录创建；
// 不推断删除/改名，不删除或改写任何用户文件。不支持多个进程共享同一 StateDir。
type WritebackEngine struct {
	mu         sync.Mutex
	cfg        WritebackConfig
	state      writebackState
	seen       map[string]localObservation
	cacheProof map[string]cacheLocalProof // 仅本轮完整快照匹配已提交版本的证据
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
	e := &WritebackEngine{cfg: cfg, seen: map[string]localObservation{}}
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
	return replaceWritebackState(name, filepath.Join(e.cfg.StateDir, "pending.json"))
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
	// 失去可读连续性（包括正文释放）后重置空闲计时。
	for p := range e.seen {
		if _, ok := local[p]; !ok {
			delete(e.seen, p)
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
		issue := func(kind string, x error) { out.Issues = append(out.Issues, WritebackIssue{p, kind, x.Error()}) }
		if info.IsDir() {
			if r != nil && r.Directory {
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
			e.state.Records[p] = &writebackRecord{Directory: true}
			if err = e.save(); err != nil {
				return out, err
			}
			continue
		}
		if o, exists := remote[p]; exists && o.Directory {
			issue("conflict", errors.New("远端同名路径是目录，禁止替换为文件"))
			continue
		}
		localID, x := writebackFileIdentity(filepath.Join(e.cfg.RootPath, filepath.FromSlash(p)))
		if x != nil {
			issue("unstable", x)
			continue
		}
		old, ok := e.seen[p]
		now := time.Now()
		if !ok || old.Size != info.Size() || !old.Mod.Equal(info.ModTime()) || old.Identity != localID {
			e.seen[p] = localObservation{Size: info.Size(), Mod: info.ModTime(), Since: now, Identity: localID}
			continue
		}
		if now.Sub(old.Since) < e.cfg.StableFor {
			continue
		}
		if r != nil && r.Directory {
			issue("conflict", errors.New("已知目录变成文件，不支持类型替换"))
			continue
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
				continue
			}
			if r == nil {
				if o, exists := remote[p]; exists {
					// 只有完整读回相同字节且身份/版本稳定才采纳初始基线。
					verified, x := e.verifyObject(ctx, token, p, pending.Hash, pending.Size)
					os.Remove(filepath.Join(e.cfg.StateDir, pending.Snapshot))
					if x != nil || o.VersionId.Int64() <= 0 || o.FileId.Int64() != verified.FileId.Int64() || o.VersionId != verified.VersionId {
						issue("conflict", errors.New("已有本地内容没有可信基线，远端不同或无法校验；保留本地文件"))
						continue
					}
					e.state.Records[p] = &writebackRecord{FileID: verified.FileId.Int64(), Version: verified.VersionId.Int64(), Hash: pending.Hash, Size: pending.Size}
					if err = e.save(); err != nil {
						return out, err
					}
					out.Verified = append(out.Verified, p)
					e.cacheProof[p] = cacheLocalProof{Info: info, Identity: localID, Hash: pending.Hash, Size: pending.Size}
					continue
				}
				r = &writebackRecord{}
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
			if verified, x := e.verifyObject(ctx, token, p, q.Hash, q.Size); x == nil && (r.FileID == 0 || verified.FileId.Int64() == r.FileID) {
				if err = e.commit(p, r, verified); err != nil {
					return out, err
				}
				out.Verified = append(out.Verified, p)
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
		uploadErr := e.cfg.Client.UploadFileIfVersion(ctx, token, e.cfg.Space, p, snap, &q.Expected, nil)
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
		if matched != nil {
			e.cacheProof[p] = cacheLocalProof{Info: info, Identity: localID, Hash: matched.Hash, Size: matched.Size}
		}
	}
	for p := range e.state.Records {
		if !present[p] {
			out.ProtectedPaths = append(out.ProtectedPaths, p)
			out.Issues = append(out.Issues, WritebackIssue{p, "missing-or-unreadable", "不推断本地删除或改名；不删除远端，也不按远端缺失删除本地"})
		}
	}
	sort.Strings(out.ProtectedPaths)
	return out, nil
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
	return os.Remove(filepath.Join(e.cfg.StateDir, q.Snapshot))
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
