package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Manager 管理插件登记表（plugins.json）与插件包目录的生命周期。
//
// 目录布局（root 为 EasyShare 数据目录，与 config.json 同级）：
//
//	{root}/plugins/{id}/          插件包解压目录（serve 为 /plugins/{id}/）
//	{root}/plugins.json           登记表
//	{root}/plugins-data/{id}.json 插件私有 KV 数据（storage 能力）
type Manager struct {
	root string // EasyShare 数据目录

	mu      sync.Mutex
	entries map[string]Entry // 以插件 ID 为键
}

// NewManager 创建管理器并加载（或初始化）登记表。
func NewManager(root string) (*Manager, error) {
	m := &Manager{root: root, entries: map[string]Entry{}}
	if err := m.loadEntries(); err != nil {
		return nil, err
	}
	return m, nil
}

// PluginsDir 返回插件包根目录。
func (m *Manager) PluginsDir() string { return filepath.Join(m.root, "plugins") }

// pluginDir 返回单个插件的目录。
func (m *Manager) pluginDir(id string) string { return filepath.Join(m.PluginsDir(), id) }

// registryPath 登记表文件路径。
func (m *Manager) registryPath() string { return filepath.Join(m.root, "plugins.json") }

// loadEntries 读取登记表；文件不存在时初始化空表。
// 同时做一次对账：目录已删的非内置插件清出登记表（内置插件由 EnsureBuiltin 重建）。
func (m *Manager) loadEntries() error {
	data, err := os.ReadFile(m.registryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读插件登记表: %w", err)
	}
	var list []Entry
	if err := json.Unmarshal(data, &list); err != nil {
		// 登记表损坏不致命：备份后从空表开始，插件目录仍在即可重建。
		_ = os.Rename(m.registryPath(), m.registryPath()+".corrupt")
		return nil
	}
	for _, e := range list {
		m.entries[e.ID] = e
	}
	return m.reconcileLocked()
}

// reconcileLocked 清理登记表中目录已消失的非内置插件。
func (m *Manager) reconcileLocked() error {
	dirty := false
	for id, e := range m.entries {
		if e.Builtin {
			continue // 内置插件允许目录暂时缺失，EnsureBuiltin 会恢复
		}
		if _, err := os.Stat(m.pluginDir(id)); os.IsNotExist(err) {
			delete(m.entries, id)
			dirty = true
		}
	}
	if dirty {
		return m.persistLocked()
	}
	return nil
}

// persistLocked 把登记表原子写回磁盘（调用方须持 mu）。
func (m *Manager) persistLocked() error {
	list := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("编码插件登记表: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}
	// 原子写惯例：同目录临时文件 + Rename（与 config/task 持久化一致）
	tmp := m.registryPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写插件登记表: %w", err)
	}
	if err := os.Rename(tmp, m.registryPath()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换插件登记表: %w", err)
	}
	return nil
}

// upsertLocked 写入/更新登记条目（安装与内置释放共用）。
func (m *Manager) upsertLocked(man Manifest, builtin bool) Entry {
	old, exists := m.entries[man.ID]
	e := Entry{
		ID:          man.ID,
		Builtin:     builtin || old.Builtin, // 一旦内置永远内置，防止同名外部包顶掉内置插件
		Disabled:    false,
		Version:     man.Version,
		Permissions: append([]string(nil), man.Permissions...),
		InstalledAt: time.Now(),
	}
	if exists {
		e.Disabled = old.Disabled
		e.InstalledAt = old.InstalledAt
	}
	m.entries[man.ID] = e
	return e
}

// List 返回全部已登记插件的信息（含禁用状态），按名称排序。
// 以目录中实际 manifest 为准（登记表只保存运行状态），目录缺失的条目会被跳过。
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	infos := make([]Info, 0, len(m.entries))
	for id, e := range m.entries {
		man, err := readManifest(m.pluginDir(id))
		if err != nil {
			continue
		}
		infos = append(infos, Info{
			ID:          man.ID,
			Name:        man.Name,
			Version:     man.Version,
			Description: man.Description,
			Icon:        man.Icon,
			Entry:       man.EntryFile(),
			Builtin:     e.Builtin,
			Disabled:    e.Disabled,
			Permissions: man.Permissions,
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}

// Get 返回单个插件的信息。
func (m *Manager) Get(id string) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return Info{}, false
	}
	man, err := readManifest(m.pluginDir(id))
	if err != nil {
		return Info{}, false
	}
	return Info{
		ID: man.ID, Name: man.Name, Version: man.Version, Description: man.Description,
		Icon: man.Icon, Entry: man.EntryFile(), Builtin: e.Builtin, Disabled: e.Disabled,
		Permissions: man.Permissions,
	}, true
}

// Permissions 返回插件当前授权的权限列表（Invoke 鉴权用）。
func (m *Manager) Permissions(id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.entries[id].Permissions...)
}

// SetDisabled 启用/禁用插件。内置插件不可禁用。
func (m *Manager) SetDisabled(id string, disabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return fmt.Errorf("插件 %s 未安装", id)
	}
	if e.Builtin && disabled {
		return fmt.Errorf("内置插件 %s 不可禁用", id)
	}
	e.Disabled = disabled
	m.entries[id] = e
	return m.persistLocked()
}

// Uninstall 卸载插件：删目录 + 移除登记。内置插件不可卸载。
func (m *Manager) Uninstall(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return fmt.Errorf("插件 %s 未安装", id)
	}
	if e.Builtin {
		return fmt.Errorf("内置插件 %s 不可卸载", id)
	}
	if err := os.RemoveAll(m.pluginDir(id)); err != nil {
		return fmt.Errorf("删除插件目录: %w", err)
	}
	delete(m.entries, id)
	// 插件私有数据一并清除（KV 存储），避免残留。
	_ = os.Remove(filepath.Join(m.root, "plugins-data", id+".json"))
	return m.persistLocked()
}

// readManifest 读取插件目录中的 manifest.json。
func readManifest(dir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Manifest{}, fmt.Errorf("读 manifest.json: %w", err)
	}
	var man Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return Manifest{}, fmt.Errorf("解析 manifest.json: %w", err)
	}
	if err := man.Validate(); err != nil {
		return Manifest{}, err
	}
	return man, nil
}
