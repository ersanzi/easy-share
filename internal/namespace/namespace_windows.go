// Package namespace registers EasyShare entries in Windows Explorer's
// "此电脑" (This PC) via the Shell NameSpace registry mechanism, similar to
// how WPS网盘 and 百度网盘 appear as branded entries.
package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Fixed CLSIDs for EasyShare's two namespace entries.
// These are stable identifiers — changing them would orphan old registry entries.
const (
	cloudCLSID  = "{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}"
	shareCLSID  = "{F6B2A3C4-D5E6-7F8A-9B0C-1D2E3F4A5B6C}"
	shellFolder = "{0E5AAE11-A475-4c5b-AB00-C66DE400274E}" // Shell File System Folder handler

	shcneAssocChanged = 0x08000000
	shcnfIDList       = 0x0000
)

var procSHChangeNotify = windows.NewLazySystemDLL("shell32.dll").NewProc("SHChangeNotify")

// Entry describes a single "此电脑" namespace entry.
type Entry struct {
	CLSID       string
	Name        string
	Description string // hover description; Explorer does not guarantee a second tile line
	IconPath    string
	TargetPath  string // WebDAV UNC path to open on double-click
}

// WebDAVUNC 将 WebDAV 端口号转换为 Windows 资源管理器可识别的 UNC 路径。
// 例如端口 19080 → \\127.0.0.1@19080\DavWWWRoot
func WebDAVUNC(port int) string {
	return `\\127.0.0.1@` + strconv.Itoa(port) + `\DavWWWRoot`
}

// DefaultEntries returns the standard EasyShare namespace entries.
// cloudPort 和 sharePort 分别是网盘和共享的 WebDAV 服务端口。
func DefaultEntries(iconPath string, cloudPort, sharePort int) []Entry {
	return []Entry{
		{
			CLSID:       cloudCLSID,
			Name:        "EasyShare 网盘",
			Description: "双击进入 EasyShare 网盘",
			IconPath:    iconPath,
			TargetPath:  WebDAVUNC(cloudPort),
		},
		{
			CLSID:       shareCLSID,
			Name:        "EasyShare 共享",
			Description: "双击进入局域网共享",
			IconPath:    iconPath,
			TargetPath:  WebDAVUNC(sharePort),
		},
	}
}

// Register adds EasyShare entries to "此电脑" in Windows Explorer.
// It is idempotent — calling it multiple times simply overwrites the values.
func Register(entries []Entry) error {
	for _, entry := range entries {
		if err := registerOne(entry); err != nil {
			return fmt.Errorf("register %q: %w", entry.Name, err)
		}
	}
	// Notify Explorer to refresh the namespace (best-effort).
	notifyExplorer()
	return nil
}

// Unregister removes EasyShare entries from "此电脑".
func Unregister(entries []Entry) error {
	for _, entry := range entries {
		unregisterOne(entry.CLSID)
	}
	notifyExplorer()
	return nil
}

func registerOne(entry Entry) error {
	// 1. Create CLSID key with display name.
	clsidPath := `Software\Classes\CLSID\` + entry.CLSID
	clsidKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath, registry.SET_VALUE|registry.CREATE_SUB_KEY)
	if err != nil {
		return fmt.Errorf("create CLSID key: %w", err)
	}
	defer clsidKey.Close()

	_ = clsidKey.SetStringValue("", entry.Name)
	_ = clsidKey.SetDWordValue("System.IsPinnedToNameSpaceTree", 1)
	clearStaleDisplayOverrides(clsidKey)
	if entry.Description != "" {
		_ = clsidKey.SetStringValue("InfoTip", entry.Description)
	} else {
		_ = clsidKey.DeleteValue("InfoTip")
	}

	// 2. InProcServer32 — tells Explorer to use shell32.dll as the handler.
	// Must be REG_EXPAND_SZ with the full system path for Explorer to load it.
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	ipsKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\InProcServer32`, registry.SET_VALUE)
	if err == nil {
		_ = ipsKey.SetExpandStringValue("", systemRoot+`\system32\shell32.dll`)
		_ = ipsKey.SetStringValue("ThreadingModel", "Apartment")
		ipsKey.Close()
	}

	// 3. ShellFolder attributes — marks the entry as an openable folder.
	sfKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\ShellFolder`, registry.SET_VALUE)
	if err == nil {
		// SFGAO_FOLDER | SFGAO_HASSUBFOLDER | SFGAO_FILESYSANCESTOR
		_ = sfKey.SetDWordValue("Attributes", 0x60000000)
		sfKey.Close()
	}

	// 3. Set icon.
	if entry.IconPath != "" {
		iconKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\DefaultIcon`, registry.SET_VALUE)
		if err == nil {
			_ = iconKey.SetStringValue("", entry.IconPath+",0")
			iconKey.Close()
		}
	}

	// 4. Set Instance to Shell File System Folder with target path.
	instanceKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\Instance`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create Instance key: %w", err)
	}
	_ = instanceKey.SetStringValue("CLSID", shellFolder)
	instanceKey.Close()

	bagKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\Instance\InitPropertyBag`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create InitPropertyBag key: %w", err)
	}
	_ = bagKey.SetStringValue("TargetFolderPath", entry.TargetPath)
	bagKey.Close()

	// 5. Add to MyComputer NameSpace.
	nsPath := `Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\` + entry.CLSID
	nsKey, _, err := registry.CreateKey(registry.CURRENT_USER, nsPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create NameSpace key: %w", err)
	}
	_ = nsKey.SetStringValue("", entry.Name)
	clearStaleDisplayOverrides(nsKey)
	nsKey.Close()

	return nil
}

func unregisterOne(clsid string) {
	// Remove from NameSpace first, then the CLSID definition.
	nsPath := `Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\` + clsid
	_ = registry.DeleteKey(registry.CURRENT_USER, nsPath)

	// Delete CLSID tree (subkeys first).
	base := `Software\Classes\CLSID\` + clsid
	_ = registry.DeleteKey(registry.CURRENT_USER, base+`\Instance\InitPropertyBag`)
	_ = registry.DeleteKey(registry.CURRENT_USER, base+`\Instance`)
	_ = registry.DeleteKey(registry.CURRENT_USER, base+`\DefaultIcon`)
	_ = registry.DeleteKey(registry.CURRENT_USER, base+`\InProcServer32`)
	_ = registry.DeleteKey(registry.CURRENT_USER, base+`\ShellFolder`)
	_ = registry.DeleteKey(registry.CURRENT_USER, base)
}

// clearStaleDisplayOverrides removes registry values that can hijack the tile
// display name of a registry-backed namespace item. LocalizedString takes
// precedence over the CLSID default value as the displayed name, so a stale
// value left over from an earlier experiment makes the entry show the wrong
// text (e.g. the hover description instead of the clean name). TileInfo and
// System.ItemTypeText are likewise unreliable subtitle slots that can cause
// Explorer to surface the wrong main tile text. Deleting all of these ensures
// the CLSID default value is used as the tile name.
func clearStaleDisplayOverrides(key registry.Key) {
	_ = key.DeleteValue("LocalizedString")
	_ = key.DeleteValue("System.Category")
	_ = key.DeleteValue("TileInfo")
	_ = key.DeleteValue("System.ItemTypeText")
}

// notifyExplorer asks the Shell to discard cached namespace metadata.
// This is best-effort; Explorer will also pick up changes on next navigation.
func notifyExplorer() {
	procSHChangeNotify.Call(shcneAssocChanged, shcnfIDList, 0, 0)
}

// IconPath returns the path to the EasyShare icon for namespace entries.
func IconPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// Use the exe itself as the icon source (it has an embedded icon resource).
	return exe
}

// IconFromBuild returns the icon path relative to the build directory.
func IconFromBuild() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	ico := filepath.Join(dir, "icon.ico")
	if _, err := os.Stat(ico); err == nil {
		return ico
	}
	return exe
}
