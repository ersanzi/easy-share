//go:build darwin

// Package namespace 在 macOS 上通过 Finder 挂载 WebDAV 卷，作为 Windows
// 「此电脑」品牌入口的等价物。挂载后的卷会出现在 Finder 侧边栏与桌面，
// 用户可像在资源管理器里双击「此电脑」条目一样直接进入 EasyShare 文件空间。
package namespace

import (
	"fmt"
	"os/exec"
	"strconv"
)

// Entry 描述一个要挂载的 WebDAV 卷。CLSID 字段在 macOS 上无注册表含义，
// 仅保留以对齐跨平台 API，这里用作卷的逻辑标识。
type Entry struct {
	CLSID       string
	Name        string
	Description string
	IconPath    string
	TargetPath  string // 要挂载的 WebDAV URL
}

// WebDAVUNC 在 macOS 上返回可挂载的 WebDAV URL（Windows 上则是 UNC 路径）。
// 例如端口 19080 → http://127.0.0.1:19080/
func WebDAVUNC(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/"
}

// DefaultEntries 返回标准的 EasyShare 挂载卷：网盘与局域网共享。
func DefaultEntries(iconPath string, cloudPort, sharePort int) []Entry {
	return []Entry{
		{
			CLSID:       "cloud",
			Name:        "EasyShare 网盘",
			Description: "EasyShare 网盘",
			IconPath:    iconPath,
			TargetPath:  WebDAVUNC(cloudPort),
		},
		{
			CLSID:       "share",
			Name:        "EasyShare 共享",
			Description: "局域网共享",
			IconPath:    iconPath,
			TargetPath:  WebDAVUNC(sharePort),
		},
	}
}

// Register 通过 Finder 挂载各 WebDAV 卷。幂等——已挂载时再次挂载不会报错。
func Register(entries []Entry) error {
	for _, entry := range entries {
		if err := mountVolume(entry.TargetPath); err != nil {
			return fmt.Errorf("mount %q: %w", entry.Name, err)
		}
	}
	return nil
}

// Unregister 卸载已挂载的卷（best-effort；Finder 退出时也会自行清理）。
func Unregister(entries []Entry) error {
	for _, entry := range entries {
		_ = ejectVolume(entry.Name)
	}
	return nil
}

// mountVolume 让 Finder 连接并挂载一个 WebDAV 卷。
// 使用 AppleScript 的 mount volume，由 Finder 负责挂载到 /Volumes 并显示在侧边栏。
func mountVolume(url string) error {
	script := fmt.Sprintf("mount volume %q", url)
	return exec.Command("osascript", "-e", script).Run()
}

// ejectVolume 按卷名弹出（best-effort，卷名由 WebDAV 服务端决定，可能不精确匹配）。
func ejectVolume(name string) error {
	script := fmt.Sprintf(`tell application "Finder" to eject (every disk whose name is %q)`, name)
	return exec.Command("osascript", "-e", script).Run()
}

// IconPath 在 macOS 上无注册表图标概念，返回空。
func IconPath() string { return "" }

// IconFromBuild 在 macOS 上无注册表图标概念，返回空。
func IconFromBuild() string { return "" }
