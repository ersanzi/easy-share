//go:build windows

package shellundo

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	menuName    = "EasyShareUndoDelete"
	menuLabel   = "删除（可撤销）"
	classesBase = `Software\Classes`
)

var menuPaths = []string{`*\shell\` + menuName, `Directory\shell\` + menuName}

type menuConfig struct{ Root, Executable, Session string }

// menuStore 的测试实现不接触真实 Shell 注册；生产固定写 HKCU\Software\Classes。
type menuStore interface {
	put(path string, values map[string]string) error
	get(path, name string) (string, error)
	remove(path string) error
}

type userMenuStore struct{ base string }

func (store userMenuStore) put(path string, values map[string]string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, store.base+`\`+path, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开用户菜单 %s: %w", path, err)
	}
	defer key.Close()
	if session, ok := values["EasyShareSession"]; ok {
		if err := key.SetStringValue("EasyShareSession", session); err != nil {
			return fmt.Errorf("设置菜单会话 %s: %w", path, err)
		}
	}
	for name, value := range values {
		if name == "EasyShareSession" {
			continue
		}
		if err := key.SetStringValue(name, value); err != nil {
			return fmt.Errorf("设置菜单 %s/%s: %w", path, name, err)
		}
	}
	return nil
}
func (store userMenuStore) get(path, name string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, store.base+`\`+path, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	return value, err
}
func (store userMenuStore) remove(path string) error {
	// 从不删 shell 父键、AllFilesystemObjects 或别人的 verb；遇到未知子键则失败关闭。
	if path != menuPaths[0] && path != menuPaths[1] {
		return fmt.Errorf("拒绝卸载非 EasyShare 菜单: %q", path)
	}
	for _, suffix := range []string{`\command`, ""} {
		if err := registry.DeleteKey(registry.CURRENT_USER, store.base+`\`+path+suffix); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("删除自身菜单 %s: %w", path+suffix, err)
		}
	}
	return nil
}

func shellCommand(executable string) string { return `"` + executable + `" ` + CommandFlag + ` "%1"` }

func appliesTo(root string) string {
	// canonical AQS 的 contains 仅控制可见性；pipe host 另按 root 边界/实际路径授权。
	return `System.ItemPathDisplay:~="` + root + `\"`
}

func installMenus(store menuStore, cfg menuConfig) error {
	root, err := normalizeLocalPath(cfg.Root)
	if err != nil || len(root) == 3 {
		return fmt.Errorf("菜单云根路径无效: %q", cfg.Root)
	}
	exe, err := normalizeLocalPath(cfg.Executable)
	if err != nil || len(exe) == 3 || cfg.Session == "" {
		return fmt.Errorf("菜单程序或会话无效")
	}
	cfg.Root, cfg.Executable = root, exe
	for _, path := range menuPaths {
		values := map[string]string{
			"MUIVerb": menuLabel, "AppliesTo": appliesTo(root), "Icon": `"` + exe + `",0`,
			// %1 无法可靠传递任意数量选择；显式 Single，避免多选时静默漏删。
			"MultiSelectModel": "Single", "EasyShareCloudRoot": root, "EasyShareExecutable": exe, "EasyShareSession": cfg.Session,
		}
		if err := store.put(path, values); err != nil {
			return errors.Join(err, removeOwnedMenus(store, cfg))
		}
		if err := store.put(path+`\command`, map[string]string{"": shellCommand(exe)}); err != nil {
			return errors.Join(err, removeOwnedMenus(store, cfg))
		}
	}
	return nil
}

func readMenuConfig(store menuStore, executable string) (menuConfig, error) {
	var cfg menuConfig
	for i, path := range menuPaths {
		root, err := store.get(path, "EasyShareCloudRoot")
		if err != nil {
			return cfg, fmt.Errorf("EasyShare 未运行或可撤销删除菜单未注册: %w", err)
		}
		exe, err := store.get(path, "EasyShareExecutable")
		if err != nil {
			return cfg, err
		}
		session, err := store.get(path, "EasyShareSession")
		if err != nil {
			return cfg, err
		}
		command, err := store.get(path+`\command`, "")
		if err != nil {
			return cfg, err
		}
		normalRoot, err := normalizeLocalPath(root)
		if err != nil || len(normalRoot) == 3 {
			return cfg, fmt.Errorf("注册云根无效")
		}
		normalExe, err := normalizeLocalPath(exe)
		if err != nil {
			return cfg, err
		}
		if !strings.EqualFold(normalExe, executable) || command != shellCommand(exe) || session == "" {
			return cfg, fmt.Errorf("菜单程序或会话与当前 EasyShare 不一致")
		}
		next := menuConfig{Root: normalRoot, Executable: normalExe, Session: session}
		if i == 0 {
			cfg = next
		} else if cfg != next {
			return cfg, fmt.Errorf("文件/目录菜单云根或会话不一致")
		}
	}
	return cfg, nil
}

func removeOwnedMenus(store menuStore, cfg menuConfig) error {
	var failures []error
	for _, path := range menuPaths {
		session, err := store.get(path, "EasyShareSession")
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if session != cfg.Session {
			continue
		} // 旧 host 关闭不得删除新 host 的菜单。
		if err := store.remove(path); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// UnregisterMenu 仅供明确卸载，移除本用户的两个 EasyShareUndoDelete verb。
// 不注销云根、不删除任何文件或 Windows 原生菜单；普通退出可用 Start 返回的 Close。
func UnregisterMenu() error {
	store := userMenuStore{base: classesBase}
	var failures []error
	for _, path := range menuPaths {
		if err := store.remove(path); err != nil {
			failures = append(failures, err)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	notifyMenusChanged()
	return nil
}

func notifyMenusChanged() {
	windowsShellNotify.Call(0x08000000 /* SHCNE_ASSOCCHANGED */, 0, 0, 0)
}
