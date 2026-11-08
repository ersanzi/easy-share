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

// Shell 入口标识按实现版本固定；迁移只影响 Explorer 定义，不改变服务器空间或 CFAPI 根。
const (
	// 长期运行的 Explorer 会缓存旧 FolderShortcut 定义，刷新通知不能清除此缓存。
	// v2 使用固定新标识；注册/注销时明确清理旧标识，不在登录时生成随机入口。
	cloudCLSID       = "{8327AC46-BCC2-45D2-A46F-0737E2CC6557}"
	legacyCloudCLSID = "{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}"
	// lanCLSID 是局域网收件箱（本机目录，无认证，仅回环可达）。
	//
	// 这个 GUID 历史上叫「EasyShare 共享」，指的是局域网收件箱，**不是**云端共享空间。
	// 保留它继续服务局域网功能：改指向会让一个在用的功能凭空消失。
	lanCLSID = "{F6B2A3C4-D5E6-7F8A-9B0C-1D2E3F4A5B6C}"
	// sharedSpaceCLSID 是云端共享空间（RustFS shared/，按 es_space_member 授权）。
	// 新 GUID：它和局域网收件箱是两件不同的事，各占一个条目才说得清。
	sharedSpaceCLSID = "{A7C3D4E5-F6A7-4B8C-9D0E-1F2A3B4C5D6E}"

	shellFolder = "{0E5AAE11-A475-4c5b-AB00-C66DE400274E}" // Shell File System Folder handler

	shcneAssocChanged = 0x08000000
	shcnfIDList       = 0x0000
)

var procSHChangeNotify = windows.NewLazySystemDLL("shell32.dll").NewProc("SHChangeNotify")

// Entry describes a single "此电脑" namespace entry.
type Entry struct {
	CLSID       string
	Name        string
	Description string // hover description (InfoTip)
	IconPath    string
	TargetPath  string // WebDAV UNC path to open on double-click

	// Subtitle 是磁贴上名称下方那行小字。
	//
	// 资源管理器对注册表型条目**不显示标准容量条**——容量取自目标所在的物理磁盘，
	// 跟我们的配额无关，而且改不了。所以配额/用量只能放在这行。
	// 实现上需要 TileInfo 与 System.ItemAuthors 配对写入，只写其一会显示成空或错文本。
	Subtitle string

	// UserID 把条目绑到某个账号。写在 CLSID 根上，换账号时连同 TargetPath 一起改，
	// 使「此电脑」里看到的始终是当前登录账号的空间。
	UserID string

	// SortOrderIndex 控制在「此电脑」里的排序位置，越小越靠前。
	SortOrderIndex uint32

	// writableFilesystem 仅由 SpaceEntries 为已通过上层可写能力闸门的
	// CFAPI 个人同步根设置；不把共享、WebDAV 或任意品牌入口声明成拖放目标。
	writableFilesystem bool
}

// WebDAVUNC 将 WebDAV 端口号转换为 Windows 资源管理器可识别的 UNC 路径。
// 例如端口 19080 → \\127.0.0.1@19080\DavWWWRoot
func WebDAVUNC(port int) string {
	return `\\127.0.0.1@` + strconv.Itoa(port) + `\DavWWWRoot`
}

// LANEntry 是局域网收件箱条目（本机目录，经 Core 的 WebDAV 暴露）。
//
// 与云端空间无关：它不受配额与共享授权约束，也不需要登录。名字里点明「局域网」，
// 避免和云端的「共享」混为一谈——这两者曾共用一个条目名，是混淆的来源。
func LANEntry(iconPath string, port int) Entry {
	return Entry{
		CLSID:          lanCLSID,
		Name:           "EasyShare 局域网",
		Description:    "局域网收件箱：本机目录，收到的文件落在这里",
		IconPath:       iconPath,
		TargetPath:     WebDAVUNC(port),
		Subtitle:       "局域网传输收件箱",
		SortOrderIndex: 66,
	}
}

// DefaultEntries returns the standard EasyShare namespace entries.
// cloudPort 和 sharePort 分别是网盘和局域网收件箱的 WebDAV 服务端口。
//
// 保留此函数是为了兼容既有调用与卸载路径（需要能构造出全部历史条目来清理）。
func DefaultEntries(iconPath string, cloudPort, sharePort int) []Entry {
	return []Entry{
		{
			CLSID:       cloudCLSID,
			Name:        "EasyShare 网盘",
			Description: "双击进入 EasyShare 网盘",
			IconPath:    iconPath,
			TargetPath:  WebDAVUNC(cloudPort),
		},
		LANEntry(iconPath, sharePort),
	}
}

// SpaceMount 描述一个要挂进「此电脑」的云端空间。
type SpaceMount struct {
	// Kind 是空间类型：personal 或 shared。
	Kind string
	// Port 是该空间的本机 WebDAV 端口。
	Port int
	// TargetPath 显式同步根；非空时绝不改写为历史 WebDAV UNC。
	TargetPath string
	// UserID 是当前登录账号，写进 CLSID 做账号绑定。
	UserID string
	// DisplayName 是账号的展示名（昵称优先），用于个人空间的条目名。
	// 空则退回不带账号的通用名。
	DisplayName string
	// Subtitle 是磁贴副标题，通常是「已用 x / 配额 y」。
	Subtitle string
	// ReadOnly 只影响文案；真正的拦截在 WebDAV 层与控制面。
	ReadOnly bool
}

// PersonalCLSID 是个人云盘空间条目的 CLSID。
func PersonalCLSID() string { return cloudCLSID }

// SharedCLSID 是云端共享空间条目的 CLSID。
func SharedCLSID() string { return sharedSpaceCLSID }

// LANCLSID 是局域网收件箱条目的 CLSID。
func LANCLSID() string { return lanCLSID }

// SpaceEntries 由空间列表构造「此电脑」条目。
//
// 只为传进来的空间建条目：没有配额的个人空间、没有授权的共享空间，上层不会传进来，
// 于是它们**不会出现**在「此电脑」里。挂一个点进去就报错的盘，比不挂更糟——用户分不清
// 是自己没权限还是软件坏了。
func SpaceEntries(iconPath string, mounts []SpaceMount) []Entry {
	entries := make([]Entry, 0, len(mounts))
	for _, mount := range mounts {
		target := mount.TargetPath
		if target == "" {
			target = WebDAVUNC(mount.Port)
		}
		entry := Entry{
			IconPath:   iconPath,
			TargetPath: target,
			UserID:     mount.UserID,
			Subtitle:   mount.Subtitle,
			// Windows CFAPI 的个人根由调用方完成写回能力探测后发布。
			// 只接受显式盘符绝对路径：UNC（含 WebDAV）、相对路径及未知类型不升级能力。
			writableFilesystem: mount.Kind == "personal" && !mount.ReadOnly &&
				filepath.IsAbs(mount.TargetPath) && len(filepath.VolumeName(mount.TargetPath)) == 2 &&
				filepath.VolumeName(mount.TargetPath)[1] == ':',
		}
		switch mount.Kind {
		case "shared":
			entry.CLSID = sharedSpaceCLSID
			entry.Name = "EasyShare 共享"
			entry.Description = "团队共享空间"
			if mount.ReadOnly {
				entry.Description = "团队共享空间（只读）"
			}
			// 共享排在个人之后
			entry.SortOrderIndex = 65
		default:
			entry.CLSID = cloudCLSID
			// 个人空间带上账号名：换账号时「此电脑」里看到的就是当前账号的盘，
			// 不至于两个账号的盘长得一模一样、分不清此刻登录的是谁。
			if mount.DisplayName != "" {
				entry.Name = mount.DisplayName + " 的网盘"
			} else {
				entry.Name = "EasyShare 网盘"
			}
			entry.Description = "我的云盘空间"
			entry.SortOrderIndex = 64
		}
		entries = append(entries, entry)
	}
	return entries
}

// Shell folder capability bits (SFGAO), not NTFS access rights.
const (
	sfgaoCanCopy         uint32 = 0x00000001
	sfgaoCanMove         uint32 = 0x00000002
	sfgaoCanLink         uint32 = 0x00000004
	sfgaoStorage         uint32 = 0x00000008
	sfgaoCanRename       uint32 = 0x00000010
	sfgaoCanDelete       uint32 = 0x00000020
	sfgaoHasPropSheet    uint32 = 0x00000040
	sfgaoReadOnly        uint32 = 0x00040000
	sfgaoStorageAncestor uint32 = 0x00800000
	sfgaoDropTarget      uint32 = 0x00000100
	sfgaoFileSysAncestor uint32 = 0x10000000
	sfgaoFolder          uint32 = 0x20000000
	sfgaoFileSystem      uint32 = 0x40000000
	sfgaoHasSubFolder    uint32 = 0x80000000
)

func shellFolderAttributes(entry Entry) uint32 {
	attributes := sfgaoFolder | sfgaoFileSystem
	if entry.writableFilesystem {
		attributes |= sfgaoCanCopy | sfgaoCanLink | sfgaoStorage | sfgaoHasPropSheet |
			sfgaoDropTarget | sfgaoStorageAncestor | sfgaoFileSysAncestor | sfgaoHasSubFolder
	}
	// 品牌根不声明 CANMOVE / CANDELETE / CANRENAME；拖入能力与移动根是两回事。
	return attributes
}

// Register adds EasyShare entries to "此电脑" in Windows Explorer.
// It is idempotent — calling it multiple times simply overwrites the values.
func Register(entries []Entry) error {
	for _, entry := range entries {
		obsolete := ""
		if entry.CLSID == cloudCLSID {
			obsolete = legacyCloudCLSID
		}
		if err := registerWithObsolete(entry, obsolete); err != nil {
			return fmt.Errorf("register %q: %w", entry.Name, err)
		}
	}
	// Notify Explorer to refresh the namespace (best-effort).
	notifyExplorer()
	return nil
}

func registerWithObsolete(entry Entry, obsolete string) error {
	if err := registerOne(entry); err != nil {
		return err
	}
	if obsolete != "" {
		if err := removeNamespaceEntry(obsolete); err != nil {
			unregisterOne(entry.CLSID)
			return fmt.Errorf("migrate personal namespace: %w", err)
		}
	}
	return nil
}

// Unregister removes EasyShare entries from "此电脑".
func Unregister(entries []Entry) error {
	var firstErr error
	for _, entry := range entries {
		for _, clsid := range namespaceCleanupCLSIDs(entry.CLSID) {
			if err := removeNamespaceEntry(clsid); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	notifyExplorer()
	return firstErr
}

// 所有个人入口清理路径（启动、登出、退出、旧版调用）覆盖新旧两种固定身份。
func namespaceCleanupCLSIDs(clsid string) []string {
	if clsid == cloudCLSID || clsid == legacyCloudCLSID {
		return []string{cloudCLSID, legacyCloudCLSID}
	}
	return []string{clsid}
}

func removeNamespaceEntry(clsid string) error {
	unregisterOne(clsid)
	for _, path := range []string{
		`Software\Classes\CLSID\` + clsid,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\` + clsid,
	} {
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err == registry.ErrNotExist {
			continue
		}
		if err == nil {
			key.Close()
			return fmt.Errorf("namespace key remains after removal: %s", path)
		}
		return fmt.Errorf("verify namespace removal: %w", err)
	}
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

	// 副标题：TileInfo 指向哪个属性，System.ItemAuthors 提供该属性的值，两者必须配对。
	// 单写 TileInfo 会让磁贴显示空白或错文本——这是之前把 TileInfo 判为「不可靠」的真因。
	if entry.Subtitle != "" {
		_ = clsidKey.SetStringValue("System.ItemAuthors", entry.Subtitle)
		_ = clsidKey.SetStringValue("TileInfo", "prop:System.ItemAuthors")
	} else {
		_ = clsidKey.DeleteValue("System.ItemAuthors")
		_ = clsidKey.DeleteValue("TileInfo")
	}

	// 账号绑定：条目属于哪个登录账号。换账号时随 TargetPath 一并更新。
	if entry.UserID != "" {
		err = clsidKey.SetStringValue("UserId", entry.UserID)
	} else {
		err = deleteOptionalValue(clsidKey, "UserId")
	}
	if err != nil {
		return fmt.Errorf("configure UserId: %w", err)
	}

	if entry.SortOrderIndex > 0 {
		_ = clsidKey.SetDWordValue("SortOrderIndex", entry.SortOrderIndex)
	}

	// 2. InProcServer32 — tells Explorer to use shell32.dll as the handler.
	// Must be REG_EXPAND_SZ with the full system path for Explorer to load it.
	//
	// 这三处曾经写成 `if err == nil { ... }` 静默吞掉错误：CreateKey 失败时
	// CLSID 定义整段缺失，而第 5 步仍然把 CLSID 登记进 NameSpace，于是「此电脑」
	// 里留下一个**指向不存在 CLSID 的空壳条目**——双击必然卡死/报错，且日志显示
	// 「已挂载」，排查时完全看不出问题（实测见 known-issues）。
	// 现在一律上抛：宁可这次不挂，也不要挂出一个坏条目。
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	ipsKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\InProcServer32`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create InProcServer32 key: %w", err)
	}
	err = ipsKey.SetExpandStringValue("", systemRoot+`\system32\shell32.dll`)
	if err == nil {
		err = ipsKey.SetStringValue("ThreadingModel", "Apartment")
	}
	ipsKey.Close()
	if err != nil {
		return fmt.Errorf("configure InProcServer32: %w", err)
	}

	// 3. ShellFolder attributes — marks the entry as an openable folder.
	sfKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\ShellFolder`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create ShellFolder key: %w", err)
	}
	// 完整文件系统委托配置仅用于可写 CFAPI 个人根；切回旧模式删除专用值。
	err = sfKey.SetDWordValue("Attributes", shellFolderAttributes(entry))
	if err == nil {
		if entry.writableFilesystem {
			err = sfKey.SetDWordValue("FolderValueFlags", 0x28)
		} else {
			err = deleteOptionalValue(sfKey, "FolderValueFlags")
		}
	}
	sfKey.Close()
	if err != nil {
		return fmt.Errorf("configure ShellFolder: %w", err)
	}

	// 3. Set icon.
	if entry.IconPath != "" {
		iconKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\DefaultIcon`, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("create DefaultIcon key: %w", err)
		}
		_ = iconKey.SetStringValue("", entry.IconPath+",0")
		iconKey.Close()
	}

	// 4. Set Instance to Shell File System Folder with target path.
	instanceKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\Instance`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create Instance key: %w", err)
	}
	err = instanceKey.SetStringValue("CLSID", shellFolder)
	instanceKey.Close()
	if err != nil {
		return fmt.Errorf("configure Instance CLSID: %w", err)
	}

	bagKey, _, err := registry.CreateKey(registry.CURRENT_USER, clsidPath+`\Instance\InitPropertyBag`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create InitPropertyBag key: %w", err)
	}
	if entry.writableFilesystem {
		err = bagKey.SetDWordValue("Attributes", 0x11)
		if err == nil {
			err = bagKey.SetExpandStringValue("TargetFolderPath", entry.TargetPath)
		}
	} else {
		err = deleteOptionalValue(bagKey, "Attributes")
		if err == nil {
			err = bagKey.SetStringValue("TargetFolderPath", entry.TargetPath)
		}
	}
	bagKey.Close()
	if err != nil {
		return fmt.Errorf("configure InitPropertyBag: %w", err)
	}

	// 5. Add to MyComputer NameSpace.
	//
	// **必须是最后一步**：NameSpace 里的 CLSID 是「可见性开关」，一旦登记，资源管理器
	// 就会去加载对应 CLSID 定义。若定义还没写全（或前面任何一步失败），用户看到的
	// 就是一个打不开的空壳条目——比不显示更糟。所以先把定义全部写完整，最后才让它可见。
	//
	// 失败时回滚已写的 CLSID 定义，避免留下不完整的残骸。
	nsPath := `Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\` + entry.CLSID
	nsKey, _, err := registry.CreateKey(registry.CURRENT_USER, nsPath, registry.SET_VALUE)
	if err != nil {
		unregisterOne(entry.CLSID)
		return fmt.Errorf("create NameSpace key: %w", err)
	}
	err = nsKey.SetStringValue("", entry.Name)
	clearStaleDisplayOverrides(nsKey)
	nsKey.Close()
	if err != nil {
		unregisterOne(entry.CLSID)
		return fmt.Errorf("configure NameSpace: %w", err)
	}

	return nil
}

// Missing optional values are already clean; all other deletion failures matter.
func deleteOptionalValue(key registry.Key, name string) error {
	err := key.DeleteValue(name)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
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

// clearStaleDisplayOverrides 清掉会劫持磁贴**主标题**的值。
//
// LocalizedString 的优先级高于 CLSID 默认值，早期实验留下的残值会让条目显示错文本
// （例如把悬浮说明显示成名称）。System.ItemTypeText 同理。
//
// 注意这里**不再删 TileInfo**：它是副标题的正确机制（配对 System.ItemAuthors 使用，
// 见 registerOne），由调用方按 Entry.Subtitle 显式管理。早前把它一并删掉，是因为当时
// 只写了 TileInfo 没写对应属性值，副标题显示异常，结论下反了。
func clearStaleDisplayOverrides(key registry.Key) {
	_ = key.DeleteValue("LocalizedString")
	_ = key.DeleteValue("System.Category")
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
