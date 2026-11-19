//go:build windows

package cfapi

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PlaceholderStatus 来自只读元数据句柄；FullContent使用实际数据区间而非分配字节猜测。
// 普通历史文件返回Placeholder=false，不能因此被标为可信云占位符。
type PlaceholderStatus struct {
	Placeholder, Directory            bool
	Size, OnDisk, Validated, Modified int64
	// RemoteID 来自已验证的占位身份，目录没有可下载的文件身份。
	RemoteID, RemoteVersion int64
	PinState, InSyncState   uint32
	CloudIdentity           []byte
	SyncRootFileID          int64
	LocalIdentity           DirectoryNTFSIdentity
	FullContent             bool
}

func (m *Mount) QueryPlaceholderStatus(rel string) (PlaceholderStatus, error) {
	return m.queryPlaceholderStatus(rel, false)
}

// QueryDeletionStatus用于系统已持DELETE句柄的回调；仅只读元数据，允许分享删除句柄。
// 操作调用方仍须核对通知中的原NTFS/云端身份，不能用此查询授权按路径删除。
func (m *Mount) QueryDeletionStatus(rel string) (PlaceholderStatus, error) {
	return m.queryPlaceholderStatus(rel, true)
}

func (m *Mount) queryPlaceholderStatus(rel string, deleting bool) (PlaceholderStatus, error) {
	open := restoreOpen
	if deleting {
		open = deletionStatusOpen
	}
	var out PlaceholderStatus
	if !validProtectedEntry(rel) {
		return out, fmt.Errorf("cfapi: 不安全的状态查询路径")
	}
	root, registered, err := QuerySyncRootStandardInfo(m.rootPath)
	if err != nil {
		return out, err
	}
	if !registered || root.ProviderName != m.provider.Name || !bytes.Equal(root.Identity, m.syncRootIdentity) {
		return out, fmt.Errorf("cfapi: 状态查询的同步根归属变化")
	}
	full := filepath.Join(m.rootPath, filepath.FromSlash(rel))
	parent := filepath.Dir(full)
	chain := []string{}
	for {
		chain = append(chain, parent)
		if filepath.Dir(parent) == parent {
			break
		}
		parent = filepath.Dir(parent)
	}
	held := []windows.Handle{}
	defer func() {
		for _, h := range held {
			windows.CloseHandle(h)
		}
	}()
	for i := len(chain) - 1; i >= 0; i-- {
		h, err := open(chain[i], windows.FILE_READ_ATTRIBUTES)
		if err != nil {
			return out, err
		}
		held = append(held, h)
		attributes, tag, err := restoreAttributes(h)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || (attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&0xffff0fff != 0x9000001a) {
			return out, fmt.Errorf("cfapi: 状态查询父路径不安全")
		}
		final, err := restoreFinalPath(h)
		if err != nil || !restoreSamePath(final, chain[i]) {
			return out, fmt.Errorf("cfapi: 状态查询父路径被重定向")
		}
		if restoreSamePath(chain[i], m.rootPath) {
			var identity windows.ByHandleFileInformation
			if err := windows.GetFileInformationByHandle(h, &identity); err != nil {
				return out, err
			}
			if uint64(identity.FileIndexHigh)<<32|uint64(identity.FileIndexLow) != uint64(root.SyncRootFileID) {
				return out, fmt.Errorf("cfapi: 状态查询根身份不符")
			}
		}
	}
	h, err := open(full, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return out, err
	}
	defer windows.CloseHandle(h)
	final, err := restoreFinalPath(h)
	if err != nil || !restoreSamePath(final, full) {
		return out, fmt.Errorf("cfapi: 状态查询目标最终路径不符")
	}
	attributes, tag, err := restoreAttributes(h)
	if err != nil || (attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&0xffff0fff != 0x9000001a) {
		return out, fmt.Errorf("cfapi: 状态查询目标不是普通文件或云占位符")
	}
	out.Directory = attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	out.LocalIdentity, err = restoreIdentity(h)
	if err != nil {
		return out, err
	}
	var standard windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &standard); err != nil {
		return out, err
	}
	out.Size = int64(uint64(standard.FileSizeHigh)<<32 | uint64(standard.FileSizeLow))
	info, placeholder, err := queryCachePlaceholder(h)
	if err != nil || !placeholder {
		return out, err
	}
	if info.SyncRootFileID != root.SyncRootFileID {
		return out, fmt.Errorf("cfapi: 文件不属于当前状态查询同步根")
	}
	out.Placeholder = true
	out.CloudIdentity = append([]byte(nil), info.Identity...)
	out.OnDisk, out.Validated, out.Modified = info.OnDisk, info.Validated, info.Modified
	out.PinState, out.InSyncState, out.SyncRootFileID = info.PinState, info.InSyncState, info.SyncRootFileID
	if out.Directory {
		return out, nil
	}
	id, ok := decodeIdentity(info.Identity)
	m.mu.RLock()
	indexed, exists := m.index[id]
	out.RemoteVersion = m.versions[id]
	m.mu.RUnlock()
	if !ok || !deleting && (!exists || !strings.EqualFold(indexed, rel)) {
		return out, fmt.Errorf("cfapi: 占位文件身份不在当前云端索引")
	}
	// 删除收尾时远端已进入回收站，活动索引可以缺项；仅返回本机元数据，
	// 调用方仍必须用持久删除账的原NTFS身份/云端身份核验，不能按路径授权。
	out.RemoteID, err = strconv.ParseInt(id, 10, 64)
	if err != nil || out.RemoteID <= 0 {
		return out, fmt.Errorf("cfapi: 状态查询文件身份无效")
	}
	if out.Size == 0 {
		out.FullContent = true
		return out, nil
	}
	out.FullContent, err = fullOnDiskRanges(h, out.Size)
	return out, err
}

func fullOnDiskRanges(h windows.Handle, size int64) (bool, error) {
	get, err := proc("CfGetPlaceholderRangeInfo")
	if err != nil {
		return false, err
	}
	buffer := make([]byte, 4096)
	var used uint32
	hr, _, _ := get.Call(uintptr(h), 1, 0, uintptr(size), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&used)))
	if uint32(hr) == 0x800700ea { // ERROR_MORE_DATA：区间过多，不凭不完整数据说已全部可用。
		return false, nil
	}
	if err := checkHResult(hr, "CfGetPlaceholderRangeInfo ONDISK"); err != nil {
		return false, err
	}
	if used > uint32(len(buffer)) || used%16 != 0 {
		return false, fmt.Errorf("cfapi: 状态数据区间返回长度无效")
	}
	covered := int64(0)
	for offset := uint32(0); offset < used; offset += 16 {
		start := int64(binary.LittleEndian.Uint64(buffer[offset : offset+8]))
		length := int64(binary.LittleEndian.Uint64(buffer[offset+8 : offset+16]))
		if start < 0 || length <= 0 || start > covered || length > size-start {
			if start >= 0 && start <= covered && length >= size-start {
				return true, nil
			}
			return false, nil
		}
		covered = max(covered, start+length)
	}
	return covered >= size, nil
}

func deletionStatusOpen(path string, access uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
}
