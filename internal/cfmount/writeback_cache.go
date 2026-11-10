package cfmount

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"easyshare/internal/drive"
)

// LocalFileIdentity 是扫描时固定的本机文件身份，不是远端 ID 或文件路径。
// Windows 对应 FILE_ID_INFO；不写入提交日志，不把延迟查询视为扫描快照。
type LocalFileIdentity struct {
	VolumeSerial uint64
	FileID       [16]byte
}

type cacheLocalProof struct {
	Info     os.FileInfo
	Identity LocalFileIdentity
	Hash     string
	Size     int64
}

// CacheCandidate 只携带本轮完整快照与已提交版本匹配的证据，不凭云清单授权。
// 适配器须独占锁定同一 NTFS 文件、核实本机字节，再持锁调用 Confirm；不得重入引擎。
type CacheCandidate struct {
	RelativePath                string
	RemoteFileID, Version, Size int64
	SHA256                      string
	LocalInfo                   os.FileInfo
	LocalIdentity               LocalFileIdentity
	Confirm                     func(context.Context) error
}
type CacheReleaser func(context.Context, CacheCandidate) (bool, error)

// releaseCache 仅在引擎互斥区、保护树同步成功后执行；只使用本轮完整匹配证据。
// 不能重新枚举或打开仅云端正文，也不能仅凭未变的大小/修改时间提交候选。
func (e *WritebackEngine) releaseCache(ctx context.Context, out *WritebackResult, release CacheReleaser) {
	idle := e.cfg.CacheIdleFor
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	paths := make([]string, 0, len(e.cacheProof))
	for p := range e.cacheProof {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if ctx.Err() != nil {
			return
		}
		r := e.state.Records[p]
		obs := e.seen[p]
		proof := e.cacheProof[p]
		if r == nil || r.Directory || r.Pending != nil || r.FileID <= 0 || r.Version <= 0 || len(r.Hash) != 64 || proof.Hash != r.Hash || proof.Size != r.Size || proof.Identity != obs.Identity || time.Since(obs.Since) < idle {
			continue
		}
		blocked := false
		for _, issue := range out.Issues {
			if issue.Path == p || strings.HasPrefix(p, issue.Path+"/") {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		path := filepath.Join(e.cfg.RootPath, filepath.FromSlash(p))
		info, err := os.Lstat(path)
		if err != nil || info.IsDir() || info.Size() != obs.Size || !info.ModTime().Equal(obs.Mod) {
			continue
		}
		safe, err := writebackReadable(path, info)
		if err != nil || !safe {
			continue
		}
		localID, err := writebackFileIdentity(path)
		if err != nil || localID != proof.Identity {
			continue
		}
		token := e.cfg.Token()
		o, err := e.cacheRemote(ctx, token, p)
		if err != nil || o.FileId.Int64() != r.FileID || o.VersionId.Int64() != r.Version || o.Size != r.Size {
			continue
		}
		// 固定提交基线值；回调不能采纳后来出现的同名对象或较新版本。
		c := CacheCandidate{RelativePath: p, RemoteFileID: r.FileID, Version: r.Version, Size: r.Size, SHA256: r.Hash, LocalInfo: proof.Info, LocalIdentity: proof.Identity}
		c.Confirm = func(ctx context.Context) error { return e.confirmCache(ctx, token, c) }
		released, err := release(ctx, c)
		if err != nil {
			out.Issues = append(out.Issues, WritebackIssue{p, "cache-retained", err.Error()})
		}
		if released && err == nil {
			out.Released = append(out.Released, p)
			delete(e.seen, p)
		}
	}
}

func (e *WritebackEngine) cacheRemote(ctx context.Context, token, p string) (drive.Object, error) {
	if token == "" {
		return drive.Object{}, errors.New("cache: not authenticated")
	}
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
		return drive.Object{}, errors.New("cache: missing or ambiguous remote identity")
	}
	return found, nil
}

func (e *WritebackEngine) confirmCache(ctx context.Context, token string, c CacheCandidate) error {
	matches := func(o drive.Object) bool {
		return o.FileId.Int64() == c.RemoteFileID && o.VersionId.Int64() == c.Version && o.Size == c.Size
	}
	before, err := e.cacheRemote(ctx, token, c.RelativePath)
	if err != nil {
		return err
	}
	if !matches(before) {
		return errors.New("cache: remote baseline changed")
	}
	f, _, err := e.cfg.Client.Open(ctx, token, e.cfg.Space, c.RelativePath)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, readErr := io.Copy(h, io.LimitReader(f, c.Size+1))
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != c.Size || hex.EncodeToString(h.Sum(nil)) != c.SHA256 {
		return errors.New("cache: remote readback mismatch")
	}
	after, err := e.cacheRemote(ctx, token, c.RelativePath)
	if err != nil {
		return err
	}
	if !matches(after) {
		return errors.New("cache: remote identity/version changed during readback")
	}
	return ctx.Err()
}

// WithCommittedCache 给前台明确操作提供已提交基线，不在这里上传或隐式采纳正文。
// apply 必须同步执行、独占核对本机身份与全文哈希，持锁 Confirm 后才改变缓存；不可重入引擎。
func (e *WritebackEngine) WithCommittedCache(ctx context.Context, path string, apply func(context.Context, CacheCandidate) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validWritebackPath(path) || apply == nil {
		return errors.New("缓存操作路径无效")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r := e.state.Records[path]
	if r == nil || r.Directory || r.Pending != nil || r.FileID <= 0 || r.Version <= 0 || len(r.Hash) != 64 {
		return errors.New("文件尚无完整上传确认或有待提交修改，保留正文，请等待同步完成")
	}
	full := filepath.Join(e.cfg.RootPath, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Size() != r.Size {
		return errors.New("本机文件发生变化，保留正文")
	}
	id, err := writebackFileIdentity(full)
	if err != nil {
		return err
	}
	token := e.cfg.Token()
	remote, err := e.cacheRemote(ctx, token, path)
	if err != nil {
		return err
	}
	if remote.FileId.Int64() != r.FileID || remote.VersionId.Int64() != r.Version || remote.Size != r.Size {
		return errors.New("远端身份或版本已变化，保留本机正文")
	}
	candidate := CacheCandidate{RelativePath: path, RemoteFileID: r.FileID, Version: r.Version, Size: r.Size, SHA256: r.Hash, LocalInfo: info, LocalIdentity: id}
	candidate.Confirm = func(ctx context.Context) error { return e.confirmCache(ctx, token, candidate) }
	return apply(ctx, candidate)
}
