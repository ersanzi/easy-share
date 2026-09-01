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

	"easyshare/internal/update"
)

// 安装防护上限：插件包本身轻量（HTML/JS/CSS/图标），给足余量同时防压缩炸弹。
const (
	maxPluginZipBytes  = 50 << 20  // zip 文件最大 50MB
	maxUnpackedBytes   = 120 << 20 // 解压总大小上限
	maxUnpackedFiles   = 2000      // 解压文件数上限
	maxSingleFileBytes = 64 << 20  // 单文件上限
)

// InstallZip 从 zip 文件路径安装/更新插件（本地高级路径，无权限确认——
// 用户主动选择的文件；商城路径走 PreviewInstall + InstallWithConsent）。
// 流程：解压到临时 staging 目录 → 解析并校验 manifest → 原子换入 plugins/{id}/ → 登记。
func (m *Manager) InstallZip(zipPath string) (Manifest, error) {
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("读插件包: %w", err)
	}
	return m.install(data, "", nil)
}

// InstallBytes 从内存安装（无权限确认，供本地 zip 高级路径与既有调用方使用）。
func (m *Manager) InstallBytes(data []byte, expectedSHA256 string) (Manifest, error) {
	return m.install(data, expectedSHA256, nil)
}

// PreviewResult 是安装预览结果：安装/更新前给用户看的新版本概要与需确认的权限。
type PreviewResult struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Version          string   `json:"version"`      // 待安装版本
	InstalledVersion string   `json:"installedVersion"` // 本地已装版本（首装为空）
	IsUpdate         bool     `json:"isUpdate"`
	NewPermissions   []string `json:"newPermissions"` // 需用户确认的权限：首装=全部声明，更新=相对本地新增
}

// PreviewInstall 校验并解压插件包（不落成安装），返回预览信息。
// 商城安装的第一步：前端展示权限清单，用户确认后再走 InstallWithConsent。
// 预览会丢弃 staging 目录——两次调用各自下载/解压，插件包很小，无状态最简单。
func (m *Manager) PreviewInstall(data []byte, expectedSHA256 string) (PreviewResult, error) {
	if expectedSHA256 != "" {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != strings.ToLower(expectedSHA256) {
			return PreviewResult{}, fmt.Errorf("插件包 SHA256 校验失败")
		}
	}
	if len(data) > maxPluginZipBytes {
		return PreviewResult{}, fmt.Errorf("插件包超过 %dMB 上限", maxPluginZipBytes>>20)
	}
	man, staging, err := m.unpackToStaging(data)
	if err != nil {
		return PreviewResult{}, err
	}
	defer os.RemoveAll(staging)

	m.mu.Lock()
	e, installed := m.entries[man.ID]
	if installed && e.Builtin {
		m.mu.Unlock()
		return PreviewResult{}, fmt.Errorf("插件 ID %s 与内置插件冲突", man.ID)
	}
	localPerms := append([]string(nil), e.Permissions...)
	localVersion := e.Version
	m.mu.Unlock()

	return PreviewResult{
		ID:               man.ID,
		Name:             man.Name,
		Version:          man.Version,
		InstalledVersion: localVersion,
		IsUpdate:         installed,
		NewPermissions:   diffNewPermissions(localPerms, man.Permissions),
	}, nil
}

// InstallWithConsent 安装/更新并执行权限同意检查：包内 manifest 相对本地的
// 新增权限必须全部在 acceptedPerms 中（即用户刚在确认框里看过并同意的集合），
// 否则拒绝安装——防止插件借升级静默扩大权限面。
func (m *Manager) InstallWithConsent(data []byte, expectedSHA256 string, acceptedPerms []string) (Manifest, error) {
	return m.install(data, expectedSHA256, acceptedPerms)
}

// diffNewPermissions 返回 next 相对 installed 新增的权限（installed 为空时为 next 全部）。
func diffNewPermissions(installed, next []string) []string {
	set := make(map[string]bool, len(installed))
	for _, p := range installed {
		set[p] = true
	}
	var out []string
	for _, p := range next {
		if !set[p] {
			out = append(out, p)
		}
	}
	return out
}

func (m *Manager) install(data []byte, expectedSHA256 string, acceptedPerms []string) (Manifest, error) {
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
	// 权限同意检查：新增权限必须都在用户确认过的集合里（acceptedPerms 为 nil
	// 表示调用方明确选择了不做同意检查的直装路径，如本地 zip 高级安装）。
	if acceptedPerms != nil {
		local := append([]string(nil), m.entries[man.ID].Permissions...)
		for _, p := range diffNewPermissions(local, man.Permissions) {
			if !containsString(acceptedPerms, p) {
				_ = os.RemoveAll(staging)
				return Manifest{}, fmt.Errorf("插件申请了新权限 %q，需在确认后重试安装", p)
			}
		}
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
		sub, err := fs.Sub(builtinFS, e.Name())
		if err != nil {
			return fmt.Errorf("取内置插件 %s 子树: %w", e.Name(), err)
		}
		if err := m.EnsureBuiltinFS(e.Name(), sub); err != nil {
			return err
		}
	}
	return nil
}

// EnsureBuiltinFS 释放/升级单个内置插件：fsys 的根即插件目录，id 显式给定。
// 除标准 embed 布局外，也供主仓直读插件工程目录（plugins/{id}）作为内置插件
// 源的场景（剪切板旗舰插件即此形态：源码归插件工程，分发身份仍是内置）。
func (m *Manager) EnsureBuiltinFS(id string, fsys fs.FS) error {
	if err := ValidateID(id); err != nil {
		return fmt.Errorf("内置插件目录名: %w", err)
	}
	manData, err := fs.ReadFile(fsys, "manifest.json")
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
			return nil
		}
	}
	m.mu.Unlock()

	staging := filepath.Join(m.PluginsDir(), stagingName(id))
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("清理 staging: %w", err)
	}
	if err := copyFSToDir(fsys, ".", staging); err != nil {
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
	return err
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

// seededMarkerFile 记录已种子过的插件 ID：卸载后不复活靠它（登记表条目随卸载
// 消失，不能作为「是否种子过」的依据）。
const seededMarkerFile = "plugins-seeded.json"

// SeedPlugin 首次运行把随宿主分发的插件落成「普通插件」——可卸载、可禁用，
// 后续更新走商城的正常流程（含权限确认），宿主不再包办。
//
// 与 EnsureBuiltin 的内置身份的关键差异：
//   - 登记 builtin=false，用户可卸载；
//   - 卸载后不复活（seeded 标记持久化，删掉 plugins-seeded.json 里的 ID 可重新种子）；
//   - 历史版本曾把剪切板登记成 builtin，这里顺带降级迁移，并在 embed 版本更新时
//     原地重写一次（老版本宿主承诺过「随宿主分发即升级」，降级那一次继续兑现）。
func (m *Manager) SeedPlugin(id string, fsys fs.FS) error {
	manData, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return fmt.Errorf("读种子插件 %s 清单: %w", id, err)
	}
	var man Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		return fmt.Errorf("解析种子插件 %s 清单: %w", id, err)
	}
	if man.ID != id {
		return fmt.Errorf("种子插件目录 %s 与清单 ID %s 不一致", id, man.ID)
	}
	if err := man.Validate(); err != nil {
		return fmt.Errorf("种子插件 %s: %w", id, err)
	}

	m.mu.Lock()
	e, registered := m.entries[id]
	m.mu.Unlock()

	if registered {
		// 已有登记：商城装的（或历史 builtin）一律不再种子，只做 builtin 降级迁移。
		if e.Builtin {
			rewrite := update.CompareVersions(man.Version, e.Version) > 0
			if rewrite {
				staging := filepath.Join(m.PluginsDir(), stagingName(id))
				if err := os.RemoveAll(staging); err != nil {
					return fmt.Errorf("清理 staging: %w", err)
				}
				if err := copyFSToDir(fsys, ".", staging); err != nil {
					_ = os.RemoveAll(staging)
					return fmt.Errorf("重写种子插件 %s: %w", id, err)
				}
				if err := swapIn(m.pluginDir(id), staging); err != nil {
					return fmt.Errorf("放入种子插件 %s: %w", id, err)
				}
			}
			m.mu.Lock()
			e := m.entries[id] // map 里是值类型：改副本后必须写回，否则降级不落盘
			e.Builtin = false
			if rewrite {
				e.Version = man.Version
				e.Permissions = append([]string(nil), man.Permissions...)
			}
			m.entries[id] = e
			err = m.persistLocked()
			m.mu.Unlock()
			if err != nil {
				return err
			}
		}
		return m.markSeeded(id)
	}

	// 未登记但种子标记已在：用户卸载过，尊重选择不复活。
	if m.seeded(id) {
		return nil
	}

	staging := filepath.Join(m.PluginsDir(), stagingName(id))
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("清理 staging: %w", err)
	}
	if err := copyFSToDir(fsys, ".", staging); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("释放种子插件 %s: %w", id, err)
	}
	if err := swapIn(m.pluginDir(id), staging); err != nil {
		return fmt.Errorf("放入种子插件 %s: %w", id, err)
	}

	m.mu.Lock()
	m.upsertLocked(man, false)
	err = m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return m.markSeeded(id)
}

// seeded 查询种子标记。
func (m *Manager) seeded(id string) bool {
	data, err := os.ReadFile(filepath.Join(m.root, seededMarkerFile))
	if err != nil {
		return false
	}
	var ids []string
	if json.Unmarshal(data, &ids) != nil {
		return false
	}
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// markSeeded 落种子标记（幂等）。
func (m *Manager) markSeeded(id string) error {
	data, err := os.ReadFile(filepath.Join(m.root, seededMarkerFile))
	if err == nil {
		var ids []string
		if json.Unmarshal(data, &ids) == nil {
			for _, v := range ids {
				if v == id {
					return nil
				}
			}
			ids = append(ids, id)
			data, _ = json.Marshal(ids)
		}
	}
	if data == nil {
		data, _ = json.Marshal([]string{id})
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}
	tmp := filepath.Join(m.root, seededMarkerFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写种子标记: %w", err)
	}
	return os.Rename(tmp, filepath.Join(m.root, seededMarkerFile))
}
