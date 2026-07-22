//go:build darwin

// Package namespace 在 macOS 上通过 Finder 挂载 WebDAV 卷，作为 Windows
// 「此电脑」品牌入口的等价物。挂载后的卷出现在 Finder 侧边栏与桌面。
//
// 挂载是真机相关行为，无法在 Windows 上验证，因此这里采用多策略 fallback
// 并输出详细日志（经 namespace.Log 接到 desktop.log），便于在 Mac 上快速定位。
package namespace

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// Register 挂载各 WebDAV 卷。单个卷失败不阻断其他卷，首个错误返回给调用方。
// 每一步都经 Log 输出诊断信息。
func Register(entries []Entry) error {
	var firstErr error
	for _, entry := range entries {
		if err := mountVolume(entry); err != nil {
			Log("挂载 %q 失败: %v", entry.Name, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("mount %q: %w", entry.Name, err)
			}
			continue
		}
		Log("挂载 %q 就绪: %s", entry.Name, entry.TargetPath)
	}
	return firstErr
}

// Unregister 卸载已挂载的卷（best-effort；Finder 退出时也会自行清理）。
func Unregister(entries []Entry) error {
	for _, entry := range entries {
		if err := unmountVolume(entry); err != nil {
			Log("卸载 %q: %v (best-effort)", entry.Name, err)
		}
	}
	return nil
}

// mountVolume 依次尝试多种策略挂载 WebDAV 卷，全部失败才返回错误。
func mountVolume(entry Entry) error {
	if alreadyMounted(entry.TargetPath) {
		Log("卷 %q 已挂载，跳过 (%s)", entry.Name, entry.TargetPath)
		return nil
	}

	// 策略 1：Finder 原生挂载。无需管理挂载点、最贴合系统体验，
	// 但无认证 WebDAV 可能弹出连接/认证对话框。
	if out, err := run("osascript", "-e", fmt.Sprintf("mount volume %q", entry.TargetPath)); err == nil {
		Log("策略1 osascript mount volume 成功: %s", strings.TrimSpace(out))
		return nil
	} else {
		Log("策略1 osascript mount volume 失败: %v | 输出: %s", err, strings.TrimSpace(out))
	}

	// 策略 2：mount_webdav 命令行，挂载到 /Volumes 下。
	// 更直接，但创建 /Volumes 挂载点与 mount 调用可能需要管理员权限。
	mountPoint := "/Volumes/" + entry.Name
	if mkdirErr := os.MkdirAll(mountPoint, 0o755); mkdirErr != nil {
		Log("策略2 创建挂载点 %s 失败: %v", mountPoint, mkdirErr)
	}
	if out, err := run("mount_webdav", entry.TargetPath, mountPoint); err == nil {
		Log("策略2 mount_webdav 成功: %s -> %s", entry.TargetPath, mountPoint)
		return nil
	} else {
		Log("策略2 mount_webdav 失败: %v | 输出: %s", err, strings.TrimSpace(out))
	}

	return fmt.Errorf("所有挂载策略均失败（详见日志）")
}

// alreadyMounted 通过 mount 输出判断该 URL 是否已挂载（best-effort，保证幂等）。
func alreadyMounted(url string) bool {
	out, err := run("mount")
	if err != nil {
		return false
	}
	// mount 输出形如: http://127.0.0.1:19080/ on /Volumes/xxx (webdav, ...)
	probe := strings.TrimSuffix(url, "/")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, probe) {
			return true
		}
	}
	return false
}

// unmountVolume 优先按 URL 从 mount 输出里找到挂载点再 diskutil unmount，
// 找不到则回退到按卷名 eject。
func unmountVolume(entry Entry) error {
	if out, err := run("mount"); err == nil {
		probe := strings.TrimSuffix(entry.TargetPath, "/")
		for _, line := range strings.Split(out, "\n") {
			if !strings.Contains(line, probe) {
				continue
			}
			if mountPoint := parseMountPoint(line); mountPoint != "" {
				if _, umErr := run("diskutil", "unmount", "force", mountPoint); umErr == nil {
					Log("已卸载 %s", mountPoint)
					return nil
				}
			}
		}
	}
	_, err := run("osascript", "-e",
		fmt.Sprintf(`tell application "Finder" to eject (every disk whose name is %q)`, entry.Name))
	return err
}

// parseMountPoint 从形如 "url on /Volumes/xxx (webdav, ...)" 的行里提取挂载点。
func parseMountPoint(line string) string {
	idx := strings.Index(line, " on ")
	if idx < 0 {
		return ""
	}
	rest := line[idx+4:]
	if end := strings.Index(rest, " ("); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// run 执行命令并返回合并输出（stdout+stderr），便于诊断。
func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// IconPath 在 macOS 上无注册表图标概念，返回空。
func IconPath() string { return "" }

// IconFromBuild 在 macOS 上无注册表图标概念，返回空。
func IconFromBuild() string { return "" }
