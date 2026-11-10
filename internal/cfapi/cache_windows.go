//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CacheBaseline 是已经在服务器验证过的文件版本，不是仅凭清单采纳的本机内容。
// Confirm 必须再次核实远端 fileId、版本和完整下载哈希；在本机独占句柄期间调用。
// 回调不得重入当前挂载的同步或回收方法。
type CacheBaseline struct {
	RelativePath string
	RemoteFileID int64
	Version      int64
	Size         int64
	SHA256       string
	LocalInfo    os.FileInfo
	// LocalIdentity 在扫描时通过元数据句柄捕获，不能用 Windows 延迟查询的 os.SameFile 代替。
	LocalIdentity DirectoryNTFSIdentity
	Confirm       func(context.Context) error
}

type CacheReleaseResult struct {
	Released        bool
	AlreadyOnline   bool
	LogicalBytes    int64
	AllocatedBefore int64
	AllocatedAfter  int64
}

var ErrCacheFileBusy = errors.New("cfapi: 文件正被使用，缓存保留")
var ErrCacheRetained = errors.New("cfapi: 固定离线或未完成的缓存保留")

// placeholderCacheInfo 对应 CF_PLACEHOLDER_STANDARD_INFO 固定头。
type placeholderCacheInfo struct {
	OnDisk, Validated, Modified, Properties int64
	PinState, InSyncState                   uint32
	FileID, SyncRootFileID                  int64
	Identity                                []byte
}

func queryCachePlaceholder(h windows.Handle) (placeholderCacheInfo, bool, error) {
	p, err := proc("CfGetPlaceholderInfo")
	if err != nil {
		return placeholderCacheInfo{}, false, err
	}
	buf := make([]byte, 60+4096+8)
	var used uint32
	hr, _, _ := p.Call(uintptr(h), 1, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&used)))
	if uint32(hr) == 0x80070178 {
		return placeholderCacheInfo{}, false, nil
	}
	if err = checkHResult(hr, "CfGetPlaceholderInfo cache"); err != nil {
		return placeholderCacheInfo{}, false, err
	}
	if used < 60 || used > uint32(len(buf)) {
		return placeholderCacheInfo{}, false, fmt.Errorf("cfapi: placeholder STANDARD 长度无效")
	}
	n := binary.LittleEndian.Uint32(buf[56:60])
	if n > 4096 || n > used-60 {
		return placeholderCacheInfo{}, false, fmt.Errorf("cfapi: placeholder identity 越界")
	}
	return placeholderCacheInfo{
		OnDisk: int64(binary.LittleEndian.Uint64(buf[:8])), Validated: int64(binary.LittleEndian.Uint64(buf[8:16])),
		Modified: int64(binary.LittleEndian.Uint64(buf[16:24])), Properties: int64(binary.LittleEndian.Uint64(buf[24:32])),
		PinState: binary.LittleEndian.Uint32(buf[32:36]), InSyncState: binary.LittleEndian.Uint32(buf[36:40]),
		FileID: int64(binary.LittleEndian.Uint64(buf[40:48])), SyncRootFileID: int64(binary.LittleEndian.Uint64(buf[48:56])),
		Identity: bytes.Clone(buf[60 : 60+n]),
	}, true, nil
}

func cacheAllocation(path string) (int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	get := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")
	var high uint32
	low, _, callErr := get.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xffffffff && callErr != windows.ERROR_SUCCESS {
		return 0, callErr
	}
	return int64(uint64(high)<<32 | uint64(uint32(low))), nil
}

// ReleaseVerifiedCache 不删除文件、不注销根、不改正文。仅在持有同一文件独占句柄、
// 核实本机内容和服务器版本后释放正文，留下同一 NTFS 文件和 cloud identity。
// 后台独占打开若与编辑器冲突立即返回 busy，绝不等待应用结束或强制关闭应用。
func (m *Mount) ReleaseVerifiedCache(ctx context.Context, baseline CacheBaseline) (CacheReleaseResult, error) {
	return m.applyVerifiedCache(ctx, baseline, cacheAutomatic)
}

// ReleaseVerifiedCacheNow 是显式释放操作；完整核验通过后才取消固定并回收。
func (m *Mount) ReleaseVerifiedCacheNow(ctx context.Context, baseline CacheBaseline) (CacheReleaseResult, error) {
	return m.applyVerifiedCache(ctx, baseline, cacheReleasePinned)
}

// KeepVerifiedCache 是显式离线保留操作；允许主动水合，仍不覆盖未提交正文。
func (m *Mount) KeepVerifiedCache(ctx context.Context, baseline CacheBaseline) error {
	_, err := m.applyVerifiedCache(ctx, baseline, cacheKeep)
	return err
}

type verifiedCacheAction uint8

const (
	cacheAutomatic verifiedCacheAction = iota
	cacheReleasePinned
	cacheKeep
)

func (m *Mount) applyVerifiedCache(ctx context.Context, baseline CacheBaseline, action verifiedCacheAction) (CacheReleaseResult, error) {
	var result CacheReleaseResult
	if baseline.RemoteFileID <= 0 || baseline.Version <= 0 || baseline.Size < 0 || baseline.LocalInfo == nil || baseline.LocalIdentity == (DirectoryNTFSIdentity{}) || baseline.Confirm == nil || !validProtectedEntry(baseline.RelativePath) || len(baseline.SHA256) != 64 {
		return result, fmt.Errorf("cfapi: 缓存回收缺少完整已验证基线")
	}
	if _, err := hex.DecodeString(baseline.SHA256); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// 与建树/索引发布串行；确认期间不能把当前文件映射切换到另一路径。
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// 路径仍是此已连接挂载的索引项；不能只因为名称相同就转换本机文件。
	idText := fmt.Sprint(baseline.RemoteFileID)
	m.mu.RLock()
	indexed, found := m.index[idText]
	m.mu.RUnlock()
	if !found || indexed != baseline.RelativePath {
		return result, fmt.Errorf("cfapi: fileId 不在当前挂载索引或路径不符")
	}
	rootInfo, registered, err := QuerySyncRootStandardInfo(m.rootPath)
	if err != nil {
		return result, err
	}
	if !registered || !bytes.Equal(rootInfo.Identity, m.syncRootIdentity) || rootInfo.ProviderName != m.provider.Name {
		return result, fmt.Errorf("cfapi: 当前同步根归属变化")
	}
	path := filepath.Join(m.rootPath, filepath.FromSlash(baseline.RelativePath))
	// 固定所有父路径且拒绝未知 reparse，防止并发改名把目标换到根外。
	pinned := []windows.Handle{}
	defer func() {
		for i := len(pinned) - 1; i >= 0; i-- {
			windows.CloseHandle(pinned[i])
		}
	}()
	current := filepath.VolumeName(path) + `\`
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(path), current), `\`)
	chain := []string{current}
	for _, part := range parts {
		if part != "" {
			current = filepath.Join(current, part)
			chain = append(chain, current)
		}
	}
	for _, parent := range chain {
		h, err := restoreOpen(parent, windows.FILE_READ_ATTRIBUTES)
		if err != nil {
			return result, err
		}
		pinned = append(pinned, h)
		attrs, tag, err := restoreAttributes(h)
		if err != nil {
			return result, err
		}
		if attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || (attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&0xffff0fff != 0x9000001a) {
			return result, fmt.Errorf("cfapi: 缓存目标父链不安全")
		}
		final, err := restoreFinalPath(h)
		if err != nil || !restoreSamePath(final, parent) {
			return result, fmt.Errorf("cfapi: 缓存目标父链最终路径不匹配")
		}
		if restoreSamePath(parent, m.rootPath) {
			var info windows.ByHandleFileInformation
			if err = windows.GetFileInformationByHandle(h, &info); err != nil {
				return result, err
			}
			if uint64(info.FileIndexHigh)<<32|uint64(info.FileIndexLow) != uint64(rootInfo.SyncRootFileID) {
				return result, fmt.Errorf("cfapi: 根 NTFS 身份不符")
			}
		}
	}
	u, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return result, err
	}
	// 先只打开元数据：对已经仅云端的占位符不能申请数据访问而触发重新下载。
	metadataAccess := uint32(windows.FILE_READ_ATTRIBUTES)
	if action != cacheAutomatic {
		metadataAccess |= windows.WRITE_DAC
	}
	metaHandle, err := windows.CreateFile(u, metadataAccess, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return result, ErrCacheFileBusy
	}
	if err != nil {
		return result, err
	}
	defer windows.CloseHandle(metaHandle)
	attrs, tag, err := restoreAttributes(metaHandle)
	if err != nil {
		return result, err
	}
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || (attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&0xffff0fff != 0x9000001a) {
		return result, fmt.Errorf("cfapi: 缓存目标不是普通文件或可信云文件")
	}
	final, err := restoreFinalPath(metaHandle)
	if err != nil || !restoreSamePath(final, path) {
		return result, fmt.Errorf("cfapi: 缓存目标最终路径不匹配")
	}
	localID, err := restoreIdentity(metaHandle)
	if err != nil || localID != baseline.LocalIdentity {
		return result, fmt.Errorf("cfapi: 扫描后 NTFS 文件身份已变化，保留")
	}
	ph, isPlaceholder, err := queryCachePlaceholder(metaHandle)
	if err != nil {
		return result, err
	}
	identity := EncodeIdentity(baseline.RemoteFileID)
	if isPlaceholder {
		if ph.SyncRootFileID != rootInfo.SyncRootFileID || !bytes.Equal(ph.Identity, identity) {
			return result, fmt.Errorf("cfapi: 云文件身份或根归属不符")
		}
		if ph.PinState == 3 || (ph.PinState == 1 && action == cacheAutomatic) {
			return result, ErrCacheRetained
		}
		if ph.OnDisk == 0 && ph.Modified == 0 && action != cacheKeep {
			if action == cacheReleasePinned && ph.PinState == 1 {
				if err := setVerifiedCachePin(metaHandle, PinStateUnspecified); err != nil {
					return result, err
				}
			}
			allocated, err := cacheAllocation(path)
			if err != nil {
				return result, err
			}
			result.AlreadyOnline = true
			result.LogicalBytes = baseline.Size
			result.AllocatedBefore, result.AllocatedAfter = allocated, allocated
			return result, nil
		}
	}
	// 显式保留才允许主动水合。先核对云端基线并以元数据句柄固定同一文件；
	// 自动回收与显式释放都不能因检查而下载未完整的正文。
	if action == cacheKeep && attrs&(windows.FILE_ATTRIBUTE_OFFLINE|windows.FILE_ATTRIBUTE_RECALL_ON_OPEN|windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS) != 0 {
		if !isPlaceholder || ph.Modified != 0 {
			return result, ErrCacheRetained
		}
		if err := baseline.Confirm(ctx); err != nil {
			return result, err
		}
		hydrate, err := proc("CfHydratePlaceholder")
		if err != nil {
			return result, err
		}
		hr, _, _ := hydrate.Call(uintptr(metaHandle), 0, ^uintptr(0), 0, 0)
		if err := checkHResult(hr, "CfHydratePlaceholder verified keep"); err != nil {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		attrs, tag, err = restoreAttributes(metaHandle)
		if err != nil {
			return result, err
		}
	}
	// 在申请数据访问前拒绝部分水合文件；OPEN_NO_RECALL 不能替代这个检查。
	if attrs&(windows.FILE_ATTRIBUTE_OFFLINE|windows.FILE_ATTRIBUTE_RECALL_ON_OPEN|windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS) != 0 {
		return result, ErrCacheRetained
	}
	h, err := windows.CreateFile(u, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return result, ErrCacheFileBusy
	}
	if err != nil {
		return result, err
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	attrs, tag, err = restoreAttributes(h)
	if err != nil {
		return result, err
	}
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || (attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&0xffff0fff != 0x9000001a) {
		return result, fmt.Errorf("cfapi: 缓存目标不是普通文件或可信云文件")
	}
	actual, err := f.Stat()
	if err != nil {
		return result, err
	}
	localID, err = restoreIdentity(h)
	if err != nil || localID != baseline.LocalIdentity || actual.Size() != baseline.Size || !actual.ModTime().Equal(baseline.LocalInfo.ModTime()) {
		return result, fmt.Errorf("cfapi: 扫描后目标身份、大小或修改时间变化")
	}
	var before windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &before); err != nil {
		return result, err
	}
	if before.NumberOfLinks != 1 {
		return result, fmt.Errorf("cfapi: 多硬链接文件不回收")
	}
	ph, isPlaceholder, err = queryCachePlaceholder(h)
	if err != nil {
		return result, err
	}
	if isPlaceholder {
		if ph.SyncRootFileID != rootInfo.SyncRootFileID || !bytes.Equal(ph.Identity, identity) {
			return result, fmt.Errorf("cfapi: 云文件身份或根归属不符")
		}
		if ph.PinState == 3 || (ph.PinState == 1 && action == cacheAutomatic) {
			return result, ErrCacheRetained
		}
		if ph.OnDisk == 0 && ph.Modified == 0 && action != cacheKeep {
			if action == cacheReleasePinned && ph.PinState == 1 {
				if err := setVerifiedCachePin(metaHandle, PinStateUnspecified); err != nil {
					return result, err
				}
			}
			allocated, err := cacheAllocation(path)
			if err != nil {
				return result, err
			}
			result.AlreadyOnline = true
			result.LogicalBytes = baseline.Size
			result.AllocatedBefore, result.AllocatedAfter = allocated, allocated
			return result, nil
		}
	}
	// 不强制读取未完整水合文件；它可能含部分未提交数据。只回收完整已验证正文。
	if attrs&(0x1000|0x40000|0x400000) != 0 {
		return result, ErrCacheRetained
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, baseline.Size+1))
	if err != nil {
		return result, err
	}
	if n != baseline.Size || hex.EncodeToString(hash.Sum(nil)) != baseline.SHA256 {
		return result, fmt.Errorf("cfapi: 本机正文与已提交基线不符，保留")
	}
	if err = baseline.Confirm(ctx); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if action == cacheKeep {
		if !isPlaceholder {
			convert, err := proc("CfConvertToPlaceholder")
			if err != nil {
				return result, err
			}
			var usn int64
			hr, _, _ := convert.Call(uintptr(h), uintptr(unsafe.Pointer(&identity[0])), uintptr(len(identity)), 1, uintptr(unsafe.Pointer(&usn)), 0)
			runtime.KeepAlive(identity)
			if err := checkHResult(hr, "CfConvertToPlaceholder verified keep"); err != nil {
				return result, err
			}
		}
		if err := setVerifiedCachePin(h, PinStatePinned); err != nil {
			return result, err
		}
		result.LogicalBytes = baseline.Size
		return result, nil
	}
	if action == cacheReleasePinned && isPlaceholder && ph.PinState == 1 {
		if err := setVerifiedCachePin(h, PinStateUnspecified); err != nil {
			return result, err
		}
	}
	result.LogicalBytes = baseline.Size
	result.AllocatedBefore, err = cacheAllocation(path)
	if err != nil {
		return result, err
	}
	if !isPlaceholder {
		convert, err := proc("CfConvertToPlaceholder")
		if err != nil {
			return result, err
		}
		var usn int64
		// 已持独占句柄且刚完成完整本地/云端核验，允许 mark+dehydrate；不是无条件同步。
		hr, _, _ := convert.Call(uintptr(h), uintptr(unsafe.Pointer(&identity[0])), uintptr(len(identity)), 3, uintptr(unsafe.Pointer(&usn)), 0)
		runtime.KeepAlive(identity)
		if err = checkHResult(hr, "CfConvertToPlaceholder verified cache"); err != nil {
			return result, err
		}
	} else {
		setSync, err := proc("CfSetInSyncState")
		if err != nil {
			return result, err
		}
		hr, _, _ := setSync.Call(uintptr(h), 1, 0, 0)
		if err = checkHResult(hr, "CfSetInSyncState locked verified cache"); err != nil {
			return result, err
		}
		dehydrate, err := proc("CfDehydratePlaceholder")
		if err != nil {
			return result, err
		}
		hr, _, _ = dehydrate.Call(uintptr(h), 0, ^uintptr(0), 0, 0)
		if err = checkHResult(hr, "CfDehydratePlaceholder verified cache"); err != nil {
			return result, err
		}
	}
	var after windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &after); err != nil {
		return result, err
	}
	if before.VolumeSerialNumber != after.VolumeSerialNumber || before.FileIndexHigh != after.FileIndexHigh || before.FileIndexLow != after.FileIndexLow {
		return result, fmt.Errorf("cfapi: 缓存释放后 NTFS 身份改变")
	}
	ph, isPlaceholder, err = queryCachePlaceholder(h)
	if err != nil || !isPlaceholder || !bytes.Equal(ph.Identity, identity) || ph.OnDisk != 0 || ph.Modified != 0 {
		return result, fmt.Errorf("cfapi: 缓存释放后云身份或状态无法确认")
	}
	result.AllocatedAfter, err = cacheAllocation(path)
	if err != nil {
		return result, err
	}
	// CFAPI 已确认 OnDisk/Modified 均为零。NTFS 驻留小文件可能仍保留 MFT
	// 内的少量分配；如实返回 AllocatedAfter，不能把元数据占用冒充正文缓存。
	result.Released = true
	return result, nil
}

func setVerifiedCachePin(h windows.Handle, state PinState) error {
	pin, err := proc("CfSetPinState")
	if err != nil {
		return err
	}
	hr, _, _ := pin.Call(uintptr(h), uintptr(state), 0, 0)
	return checkHResult(hr, "CfSetPinState verified cache")
}
