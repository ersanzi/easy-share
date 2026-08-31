package plugin

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 安装防护上限：插件包本身轻量（HTML/JS/CSS/图标），给足余量同时防压缩炸弹。
const (
	maxPluginZipBytes  = 50 << 20  // zip 文件最大 50MB
	maxUnpackedBytes   = 120 << 20 // 解压总大小上限
	maxUnpackedFiles   = 2000      // 解压文件数上限
	maxSingleFileBytes = 64 << 20  // 单文件上限
)

// InstallZip 从 zip 文件路径安装/更新插件。
// 流程：解压到临时 staging 目录 → 解析并校验 manifest → 原子换入 plugins/{id}/ → 登记。
func (m *Manager) InstallZip(zipPath string) (Manifest, error) {
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("读插件包: %w", err)
	}
	return m.install(data, "")
}

// InstallBytes 从内存安装（商城下载流走这里，可带期望 SHA256 校验）。
func (m *Manager) InstallBytes(data []byte, expectedSHA256 string) (Manifest, error) {
	return m.install(data, expectedSHA256)
}

func (m *Manager) install(data []byte, expectedSHA256 string) (Manifest, error) {
	if expectedSHA256 != "" {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != strings.ToLower(expectedSHA256) {
			return Manifest{}, fmt.Errorf("插件包 SHA256 校验失败")
		}
	}
	if len(data) > maxPluginZipBytes {
		return Manifest{}, fmt.Errorf("插件包超过 %dMB 上限", maxPluginZipBytes>>20)
	}
	man, staging, err := m.unpackToStaging(data)
	if err != nil {
		return Manifest{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// 同名内置插件拒绝被外部包覆盖（内置身份只随宿主分发）。
	if e, ok := m.entries[man.ID]; ok && e.Builtin {
		_ = os.RemoveAll(staging)
		return Manifest{}, fmt.Errorf("插件 ID %s 与内置插件冲突", man.ID)
	}
	if err := swapIn(m.pluginDir(man.ID), staging); err != nil {
		_ = os.RemoveAll(staging)
		return Manifest{}, err
	}
	m.upsertLocked(man, false)
	if err := m.persistLocked(); err != nil {
		return man, err
	}
	return man, nil
}

// stagingName 生成 staging 目录名。
func stagingName(id string) string {
	return fmt.Sprintf(".staging-%s-%d-%d", id, time.Now().UnixNano(), rand.Int31())
}

// unpackToStaging 把 zip 解压到 plugins/.staging-{id}-.../ 并校验 manifest，
// 返回 manifest 与 staging 目录路径；失败时清理 staging 目录。
func (m *Manager) unpackToStaging(data []byte) (man Manifest, staging string, err error) {
	if err := os.MkdirAll(m.PluginsDir(), 0o700); err != nil {
		return man, "", fmt.Errorf("创建插件目录: %w", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return man, "", fmt.Errorf("打开插件包: %w", err)
	}
	staging = filepath.Join(m.PluginsDir(), stagingName("zip"))
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staging)
		}
	}()
	if err = extractZip(reader, staging); err != nil {
		return man, "", err
	}
	man, err = readManifest(staging)
	if err != nil {
		return man, "", fmt.Errorf("插件包校验: %w", err)
	}
	// 入口文件必须真实存在于包内。
	if _, statErr := os.Stat(filepath.Join(staging, man.EntryFile())); statErr != nil {
		err = fmt.Errorf("插件包缺少入口文件 %s", man.EntryFile())
		return man, "", err
	}
	// 重命名为以插件 ID 标识的 staging（install 侧再做原子换入与登记）。
	final := filepath.Join(m.PluginsDir(), stagingName(man.ID))
	if err = os.Rename(staging, final); err != nil {
		return man, "", fmt.Errorf("预备插件目录: %w", err)
	}
	return man, final, nil
}

// extractZip 安全解压：zip-slip 防护 + 解压总量/文件数/单文件大小限制。
func extractZip(reader *zip.Reader, dest string) error {
	var totalFiles int
	var totalBytes int64
	for _, f := range reader.File {
		totalFiles++
		if totalFiles > maxUnpackedFiles {
			return fmt.Errorf("插件包文件数超过 %d", maxUnpackedFiles)
		}
		name := filepath.ToSlash(f.Name)
		// 防路径逃逸：拒绝绝对路径、盘符与 .. 段。
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "./")
		if name == "" || strings.HasPrefix(name, "/") || clean != name || strings.Contains(clean, "../") ||
			filepath.IsAbs(name) || (len(name) > 1 && name[1] == ':') {
			return fmt.Errorf("插件包含非法路径 %q", f.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("创建目录 %s: %w", clean, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("创建目录: %w", err)
		}
		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("打开包内文件 %s: %w", clean, err)
		}
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			_ = src.Close()
			return fmt.Errorf("写文件 %s: %w", clean, err)
		}
		written, err := io.Copy(dst, src)
		if err != nil {
			_ = src.Close()
			_ = dst.Close()
			return fmt.Errorf("解压文件 %s: %w", clean, err)
		}
		totalBytes += written
		if totalBytes > maxUnpackedBytes {
			_ = src.Close()
			_ = dst.Close()
			return fmt.Errorf("插件包解压总量超过 %dMB", maxUnpackedBytes>>20)
		}
		if written > maxSingleFileBytes {
			_ = src.Close()
			_ = dst.Close()
			return fmt.Errorf("包内单文件超过 %dMB", maxSingleFileBytes>>20)
		}
		_ = src.Close()
		if err := dst.Close(); err != nil {
			return fmt.Errorf("写文件 %s: %w", clean, err)
		}
	}
	return nil
}

// swapIn 把 staging 目录原子换入 final 路径（已有目录先挪到 .trash 再删）。
func swapIn(final, staging string) error {
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return fmt.Errorf("创建插件根目录: %w", err)
	}
	if _, err := os.Stat(final); err == nil {
		trash := final + ".trash-" + fmt.Sprintf("%d", time.Now().UnixNano())
		if err := os.Rename(final, trash); err != nil {
			return fmt.Errorf("移开旧插件目录: %w", err)
		}
		defer os.RemoveAll(trash)
	}
	if err := os.Rename(staging, final); err != nil {
		return fmt.Errorf("放入插件目录: %w", err)
	}
	return nil
}

// EnsureBuiltin 释放/升级内置插件（embed FS，随宿主二进制分发）。
// 与外部安装同路径（plugins/{id}/），版本变化或目录缺失时重写——
// 这保证内置插件「目录被删重启即恢复」。每次启动调用。
func (m *Manager) EnsureBuiltin(builtinFS fs.FS) error {
	// builtinFS 根下每个子目录是一个内置插件
	entries, err := fs.ReadDir(builtinFS, ".")
	if err != nil {
		return fmt.Errorf("读内置插件资源: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if err := ValidateID(id); err != nil {
			return fmt.Errorf("内置插件目录名: %w", err)
		}
		manData, err := fs.ReadFile(builtinFS, id+"/manifest.json")
		if err != nil {
			return fmt.Errorf("读内置插件 %s 清单: %w", id, err)
		}
		var man Manifest
		if err := json.Unmarshal(manData, &man); err != nil {
			return fmt.Errorf("解析内置插件 %s 清单: %w", id, err)
		}
		if man.ID != id {
			return fmt.Errorf("内置插件目录 %s 与清单 ID %s 不一致", id, man.ID)
		}
		if err := man.Validate(); err != nil {
			return fmt.Errorf("内置插件 %s: %w", id, err)
		}

		m.mu.Lock()
		if existing, registered := m.entries[id]; registered && existing.Version == man.Version {
			if _, statErr := os.Stat(m.pluginDir(id)); statErr == nil {
				// 已登记同版本且目录完好，无需重写
				m.mu.Unlock()
				continue
			}
		}
		m.mu.Unlock()

		staging := filepath.Join(m.PluginsDir(), stagingName(id))
		if err := os.RemoveAll(staging); err != nil {
			return fmt.Errorf("清理 staging: %w", err)
		}
		if err := copyFSToDir(builtinFS, id, staging); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("释放内置插件 %s: %w", id, err)
		}
		if err := swapIn(m.pluginDir(id), staging); err != nil {
			return fmt.Errorf("放入内置插件 %s: %w", id, err)
		}

		m.mu.Lock()
		m.upsertLocked(man, true)
		err = m.persistLocked()
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// copyFSToDir 把 embed FS 的子树拷到目标目录。
func copyFSToDir(fsys fs.FS, sub, dest string) error {
	return fs.WalkDir(fsys, sub, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, sub), "/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}
