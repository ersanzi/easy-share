//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
)

// 生产和集成测试共用配置入口，空授权仍安装拒绝一切的 verifier。
func nativeWritebackConfig(cfg cfmount.Config, stateDir, namespace string, client cfmount.WritebackClient) (cfmount.WritebackConfig, error) {
	out := cfmount.WritebackConfig{RootPath: cfg.RootPath, StateDir: stateDir, Namespace: namespace, Space: cfg.Space, Client: client, Token: cfg.Token, CacheIdleFor: cfg.CacheIdleFor}
	verifier, err := loadNativeDirectoryVerifier(stateDir, namespace, cfg.RootPath)
	if err != nil {
		return out, err
	}
	out.TrustedDirectory = func(ctx context.Context, ns, root, rel string, remote drive.Object) (bool, error) {
		if verifier == nil || ns != namespace || root != cfg.RootPath {
			return false, nil
		}
		// 每次验签式核验都重新固定授权存储及 ACL；启动时的一次检查不能
		// 保障随后 provenance 的安全。授权快照保持不变，不热加载新授权。
		release, err := nativePinAuthorizationStore(stateDir, root)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		defer release()
		// provenance 的显式 ACL 可能不同于父目录；逐文件检查并固定。
		key := sha256.Sum256([]byte(ns + "\x00" + strings.ToLower(root) + "\x00" + strings.ToLower(filepath.FromSlash(rel))))
		record := filepath.Join(stateDir, nativeRestoreAuthorizationDirectory, "directory-restore-v1-"+hex.EncodeToString(key[:])+".json")
		h, err := nativeOpenAuthorization(record, false)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		defer windows.CloseHandle(h)
		if err = nativeRestrictedAuthorizationACL(h); err != nil {
			return false, err
		}
		return verifier.Verify(ctx, ns, root, rel, remote)
	}
	return out, nil
}
