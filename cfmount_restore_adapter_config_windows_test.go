//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

func TestNativeRestoreAdapterNoAuthorization(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	// 系统 TEMP 与用户 TEMP 的继承 ACL 不同；无授权必须在宽 ACL 下
	// 也直接拒绝，而不是先验证只适用于显式恢复授权的受限存储。
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(state, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := nativeWritebackConfig(cfmount.Config{RootPath: root}, state, "account-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustedDirectory == nil {
		t.Fatal("生产配置未安装 TrustedDirectory")
	}
	for _, ns := range []string{"account-a", "account-b"} {
		ok, err := cfg.TrustedDirectory(context.Background(), ns, root, "docs", drive.Object{Path: "docs", Directory: true, FileId: 42})
		if ok || err != nil {
			t.Fatalf("无显式授权不得采纳同名目录: namespace=%q trusted=%v err=%v", ns, ok, err)
		}
	}
}

// 仅在测试临时目录配置 ACL，不操作用户 state、同步根、进程或 CFAPI 注册。
func TestNativeRestoreAdapterRejectsWrongNamespaceAccount(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	dir := filepath.Join(state, nativeRestoreAuthorizationDirectory)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{state, dir} {
		if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
	}
	ns := nativeSpaceIdentity("https://server", "user-a", "personal")
	id := cfapi.DirectoryNTFSIdentity{VolumeSerial: 1, FileID: [16]byte{1}}
	source := cfapi.VerifiedDirectoryRestore{Namespace: ns, Root: root, RelativePath: "docs", RemoteDirectoryFileID: "42", PlaceholderIdentity: []byte("docs"), ExpectedRoot: id, ExpectedDirectory: id}
	doc := nativeRestoreAuthorizations{Version: 1, Sources: []cfapi.VerifiedDirectoryRestore{source}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "authorizations.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{nativeSpaceIdentity("https://other", "user-a", "personal"), nativeSpaceIdentity("https://server", "user-b", "personal")} {
		if _, err := nativeWritebackConfig(cfmount.Config{RootPath: root}, state, wrong, nil); err == nil {
			t.Fatal("跨 namespace/账号授权未拒绝")
		}
	}
	cfg, err := nativeWritebackConfig(cfmount.Config{RootPath: root}, state, ns, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustedDirectory == nil {
		t.Fatal("verifier 缺失")
	}
	ok, err := cfg.TrustedDirectory(context.Background(), ns, root, "docs", drive.Object{Path: "docs", Directory: true, FileId: 42})
	if ok || err != nil {
		t.Fatal("缺少 committed provenance 不得放行")
	}
}

// 用临时 NTFS 目录和精确 committed 记录验证生产回调；不进行元数据转换。
func TestNativeRestoreAdapterCommittedScope(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	// verifier 故意精确比较最终路径；TempDir 的环境变量大小写不一定
	// 等于卷上名称，授权必须使用 Windows 实际返回的规范路径。
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	windows.CloseHandle(h)
	if err != nil || n >= uint32(len(buf)) {
		t.Fatalf("canonical root: %v", err)
	}
	root = strings.TrimPrefix(windows.UTF16ToString(buf[:n]), `\\?\`)
	dir := filepath.Join(state, nativeRestoreAuthorizationDirectory)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{state, dir} {
		if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
	}
	rid, err := cfapi.ReadDirectoryNTFSIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	did, err := cfapi.ReadDirectoryNTFSIdentity(filepath.Join(root, "docs"))
	if err != nil {
		t.Fatal(err)
	}
	ns := nativeSpaceIdentity("https://server", "account", "personal")
	source := cfapi.VerifiedDirectoryRestore{Namespace: ns, Root: root, RelativePath: "docs", RemoteDirectoryFileID: "42", PlaceholderIdentity: []byte("docs"), ExpectedRoot: rid, ExpectedDirectory: did}
	save := func(path string, value any) {
		t.Helper()
		b, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	save(filepath.Join(dir, "authorizations.json"), nativeRestoreAuthorizations{Version: 1, Sources: []cfapi.VerifiedDirectoryRestore{source}})
	key := sha256.Sum256([]byte(ns + "\x00" + strings.ToLower(root) + "\x00docs"))
	record := filepath.Join(dir, "directory-restore-v1-"+hex.EncodeToString(key[:])+".json")
	save(record, map[string]any{"version": 1, "kind": "verified-directory-metadata-conversion", "source": source, "actual": did, "usn": int64(0)})
	cfg, err := nativeWritebackConfig(cfmount.Config{RootPath: root}, state, ns, nil)
	if err != nil {
		t.Fatal(err)
	}
	remote := drive.Object{Path: "docs", Directory: true, FileId: 42}
	verify := func(want bool) {
		t.Helper()
		ok, e := cfg.TrustedDirectory(context.Background(), ns, root, "docs", remote)
		if ok != want || (want && e != nil) {
			t.Fatalf("trusted=%v error=%v want=%v", ok, e, want)
		}
	}
	verify(true)
	for _, scope := range []struct{ namespace, root string }{{ns + "-other", root}, {ns, state}} {
		ok, e := cfg.TrustedDirectory(context.Background(), scope.namespace, scope.root, "docs", remote)
		if ok || e != nil {
			t.Fatalf("wrong namespace/root accepted: trusted=%v error=%v", ok, e)
		}
	}
	remote.FileId = 43
	verify(false)
	remote.FileId = 42
	// 已发布授权不允许因同名、Directory=true 或 NTFS 身份替换而被继承。
	if err := os.Rename(filepath.Join(root, "docs"), filepath.Join(root, "old-docs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	verify(false)
	if err := os.Remove(filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "old-docs"), filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	verify(true)
	// provenance 单独扩大权限，不能仅信任父目录的 ACL。
	public, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	pa, _, err := public.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{state, dir, record} {
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, pa, nil); err != nil {
			t.Fatal(err)
		}
		verify(false)
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
		verify(true)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	verify(false)
}

func TestNativeRestoreAuthorizationStoreRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	for _, state := range []string{"relative", `\\server\share\state`, filepath.Join(root, "state"), root} {
		release, err := nativePinAuthorizationStore(state, root)
		if release != nil {
			release()
		}
		if err == nil {
			t.Fatalf("unsafe authorization state accepted: %q", state)
		}
	}
}
