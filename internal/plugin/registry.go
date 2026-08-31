package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Capability 是一个能力 API 的实现：入参为插件传来的 JSON 参数，出参为任意可序列化值。
type Capability func(args json.RawMessage) (any, error)

// Registry 是能力 API 注册表：宿主各子系统（clipboard/notification/drive…）
// 在启动时把自己的能力注册进来，插件经 App.PluginInvoke 动态调用。
// 每个能力声明所需权限；调用时按插件 manifest 的 permissions 快照鉴权。
type Registry struct {
	mu    sync.RWMutex
	caps  map[string]Capability
	perms map[string]string // API 名 → 所需权限（空串表示无需权限）
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{caps: map[string]Capability{}, perms: map[string]string{}}
}

// Register 注册能力。permission 传空串表示该能力无需额外权限。
// 重复注册同名 API 视为编码错误，直接 panic（启动期暴露）。
func (r *Registry) Register(api, permission string, fn Capability) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.caps[api]; exists {
		panic(fmt.Sprintf("插件能力 %s 重复注册", api))
	}
	if fn == nil {
		panic(fmt.Sprintf("插件能力 %s 实现为空", api))
	}
	r.caps[api] = fn
	r.perms[api] = permission
}

// Invoke 按插件权限执行能力调用。perms 为该插件 manifest 声明的权限列表。
func (r *Registry) Invoke(perms []string, api string, args json.RawMessage) (any, error) {
	r.mu.RLock()
	fn, ok := r.caps[api]
	perm := r.perms[api]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("未知能力 %q", api)
	}
	if perm != "" && !containsString(perms, perm) {
		return nil, fmt.Errorf("插件未获授权：%s（需要权限 %s）", api, perm)
	}
	return fn(args)
}

// Capabilities 返回已注册能力清单（API 名 → 所需权限），供调试与文档。
func (r *Registry) Capabilities() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.caps))
	for api := range r.caps {
		out[api] = r.perms[api]
	}
	return out
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

// --- storage 能力：插件私有 KV（需要 pluginID 上下文做隔离，不走 Registry）---
//
// 数据文件：{root}/plugins-data/{pluginId}.json，按插件 ID 天然隔离。
// 上限：单值 256KB / 单插件总量 10MB / 键数 500，防无界写盘。

const (
	kvMaxValueBytes = 256 << 10 // 单值上限 256KB
	kvMaxTotalBytes = 10 << 20  // 单插件总数据上限 10MB
	kvMaxKeys       = 500
)

// InvokeFor 是 PluginInvoke 的核心：带上插件身份执行能力调用。
// storage.* 因需要 pluginID 做数据隔离而在这里分派（同样先鉴权）；其余能力直通 Registry。
func (m *Manager) InvokeFor(r *Registry, pluginID, api string, args json.RawMessage) (any, error) {
	perms := m.Permissions(pluginID)

	var req struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	switch api {
	case "storage.get", "storage.set", "storage.remove":
		if !containsString(perms, PermStorage) {
			return nil, fmt.Errorf("插件未获授权：%s（需要权限 %s）", api, PermStorage)
		}
		if err := json.Unmarshal(args, &req); err != nil || req.Key == "" {
			return nil, fmt.Errorf("%s 需要 {key}", api)
		}
	}

	switch api {
	case "storage.get":
		store, err := m.loadKV(pluginID)
		if err != nil {
			return nil, err
		}
		if raw, ok := store[req.Key]; ok {
			return json.RawMessage(raw), nil
		}
		return nil, nil
	case "storage.set":
		if len(req.Value) > kvMaxValueBytes {
			return nil, fmt.Errorf("单值超过 %dKB 上限", kvMaxValueBytes>>10)
		}
		store, err := m.loadKV(pluginID)
		if err != nil {
			return nil, err
		}
		if _, exists := store[req.Key]; !exists && len(store) >= kvMaxKeys {
			return nil, fmt.Errorf("键数量超过 %d 上限", kvMaxKeys)
		}
		store[req.Key] = json.RawMessage(req.Value)
		if err := m.saveKV(pluginID, store); err != nil {
			return nil, err
		}
		return true, nil
	case "storage.remove":
		store, err := m.loadKV(pluginID)
		if err != nil {
			return nil, err
		}
		delete(store, req.Key)
		if err := m.saveKV(pluginID, store); err != nil {
			return nil, err
		}
		return true, nil
	case "storage.keys":
		if !containsString(perms, PermStorage) {
			return nil, fmt.Errorf("插件未获授权：%s（需要权限 %s）", api, PermStorage)
		}
		store, err := m.loadKV(pluginID)
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(store))
		for k := range store {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, nil
	default:
		return r.Invoke(perms, api, args)
	}
}

// loadKV 读取插件 KV；文件不存在或损坏时返回空表（插件私有数据，重置不拖死宿主）。
func (m *Manager) loadKV(pluginID string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(m.kvPath(pluginID))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]json.RawMessage{}, nil
		}
		return nil, fmt.Errorf("读插件数据: %w", err)
	}
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]json.RawMessage{}, nil
	}
	return out, nil
}

// saveKV 写回插件 KV（原子写惯例：临时文件 + Rename）。
func (m *Manager) saveKV(pluginID string, store map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("编码插件数据: %w", err)
	}
	if len(data) > kvMaxTotalBytes {
		return fmt.Errorf("插件数据总量超过 %dMB 上限", kvMaxTotalBytes>>20)
	}
	dir := filepath.Join(m.root, "plugins-data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建插件数据目录: %w", err)
	}
	tmp := m.kvPath(pluginID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写插件数据: %w", err)
	}
	if err := os.Rename(tmp, m.kvPath(pluginID)); err != nil {
		return fmt.Errorf("替换插件数据: %w", err)
	}
	return nil
}

// kvPath 插件数据文件路径。
func (m *Manager) kvPath(pluginID string) string {
	return filepath.Join(m.root, "plugins-data", pluginID+".json")
}
