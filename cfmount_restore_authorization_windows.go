//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"golang.org/x/sys/windows"
)

// 独立授权文件不由扫描器、pending.json 或目录名称生成；缺失即保持原保护。
// 部署者须预先建立受限目录（当前用户/SYSTEM/Administrators），显式核验后发布授权。
// 与当前用户同权限的写入者属于信任边界内，JSON 本身不是签名。
const nativeRestoreAuthorizationDirectory = "directory-restore-authorizations"

type nativeRestoreAuthorizations struct {
	Version int                              `json:"version"`
	Sources []cfapi.VerifiedDirectoryRestore `json:"sources"`
}

func loadNativeDirectoryVerifier(stateDir, namespace, root string) (*cfmount.TrustedDirectoryVerifier, error) {
	dir := filepath.Join(stateDir, nativeRestoreAuthorizationDirectory)
	path := filepath.Join(dir, "authorizations.json")
	// 不创建目录、修 ACL 或导入记录；普通写回状态绝不是授权来源。
	h, err := nativeOpenAuthorization(path, false)
	if os.IsNotExist(err) {
		// 无授权时不访问 state 的 ACL；普通写回存储无需满足恢复授权存储的限制。
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("恢复授权文件: %w", err)
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	release, err := nativePinAuthorizationStore(stateDir, root)
	if err != nil {
		return nil, err
	}
	defer release()
	if err = nativeRestrictedAuthorizationACL(h); err != nil {
		return nil, err
	}
	var doc nativeRestoreAuthorizations
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("恢复授权 JSON 无效（原始内容已隐藏）")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("恢复授权有尾随数据")
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("恢复授权版本不支持")
	}
	for i := range doc.Sources {
		s := &doc.Sources[i]
		if s.Namespace != namespace || s.Root != root {
			return nil, fmt.Errorf("恢复授权 namespace/root 不匹配")
		}
		// 固定 digest 的 Windows 路径表示，避免正反斜杠别名产生不同
		// provenance 文件名；写回回调使用的 slash 路径只在边界转换。
		if s.RelativePath != filepath.FromSlash(s.RelativePath) || filepath.Clean(s.RelativePath) != s.RelativePath {
			return nil, fmt.Errorf("恢复授权 relativePath 必须为规范 Windows 相对路径")
		}
		// StateDirectory 禁止由外部 JSON 指定；只读同一受限目录中的 committed provenance。
		s.StateDirectory = dir
	}
	return cfmount.NewTrustedDirectoryVerifier(doc.Sources)
}

func nativeOpenAuthorization(path string, directory bool) (windows.Handle, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return windows.InvalidHandle, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if e != nil {
		return windows.InvalidHandle, e
	}
	var a struct{ Attributes, Tag uint32 }
	e = windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&a)), uint32(unsafe.Sizeof(a)))
	if e == nil && (a.Attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (a.Attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory) {
		e = fmt.Errorf("恢复授权路径不是普通文件/目录: %s", path)
	}
	if e != nil {
		windows.CloseHandle(h)
		return windows.InvalidHandle, e
	}
	return h, nil
}

func nativeRestrictedAuthorizationACL(h windows.Handle) error {
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return e
	}
	trusted := func(s *windows.SID) bool {
		return s.Equals(user.User.Sid) || s.IsWellKnown(windows.WinLocalSystemSid) || s.IsWellKnown(windows.WinBuiltinAdministratorsSid)
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	if owner == nil || !trusted(owner) {
		return fmt.Errorf("恢复授权所有者不受信任")
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	if acl == nil {
		return fmt.Errorf("恢复授权缺少受限 DACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, i, &ace); e != nil {
			return e
		}
		// 保守拒绝所有未知 ACE；仅允许受信任主体，拒绝 Everyone/Users 的读授权也无妨。
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("恢复授权包含不支持的 ACE")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !trusted(sid) {
			return fmt.Errorf("恢复授权 DACL 对非受信任主体开放")
		}
	}
	return nil
}

// nativePinAuthorizationStore 将受限存储固定到本次 provenance 校验结束。
// 不创建、修复或导入任何目录；禁止相对路径、设备路径和根内状态。
func nativePinAuthorizationStore(stateDir, root string) (func(), error) {
	if !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir || len(filepath.VolumeName(stateDir)) != 2 || strings.HasPrefix(stateDir, `\\`) {
		return nil, fmt.Errorf("恢复授权状态路径必须是规范本地绝对路径")
	}
	rel, err := filepath.Rel(root, stateDir)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, `..\`)) {
		return nil, fmt.Errorf("恢复授权状态必须在同步根之外")
	}
	dir := filepath.Join(stateDir, nativeRestoreAuthorizationDirectory)
	handles := []windows.Handle{}
	release := func() {
		for i := len(handles) - 1; i >= 0; i-- {
			windows.CloseHandle(handles[i])
		}
	}
	current := filepath.VolumeName(dir) + `\`
	paths := []string{current}
	for _, part := range strings.Split(strings.TrimPrefix(dir, current), `\`) {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	for _, path := range paths {
		h, e := nativeOpenAuthorization(path, true)
		if e != nil {
			release()
			return nil, e
		}
		handles = append(handles, h)
		if path == stateDir || path == dir {
			if e = nativeRestrictedAuthorizationACL(h); e != nil {
				release()
				return nil, e
			}
		}
	}
	return release, nil
}
