// Package plugin 实现桌面端插件运行时的宿主侧：
// 插件清单与登记表（plugins.json）、zip 安装/卸载/更新、静态资源 serve、
// 能力 API 注册表与插件私有 KV 存储。插件 UI 跑在前端沙箱 iframe 中，
// 经唯一动态通道 App.PluginInvoke 调用这里注册的能力。
package plugin

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 能力权限常量：manifest.permissions 取值集合。
// 新增能力 API 时在此登记权限，并在文档同步说明。
const (
	PermStorage         = "storage"          // 插件私有 KV 读写
	PermClipboardRead   = "clipboard.read"   // 读/管理剪切板历史
	PermClipboardWrite  = "clipboard.write"  // 回写剪切板
	PermClipboardEvents = "clipboard.events" // 订阅剪切板变更推送
	PermNotification    = "notification"     // 系统通知
	PermDriveUpload     = "drive.upload"     // 上传文件到个人云盘空间
)

// AllPermissions 是当前支持的全部权限（安装确认界面逐项展示用）。
var AllPermissions = []string{
	PermStorage, PermClipboardRead, PermClipboardWrite, PermClipboardEvents,
	PermNotification, PermDriveUpload,
}

// idPattern 插件 ID 规则：小写字母开头，小写字母/数字/连字符，2~32 位。
// 同时用作插件目录名与 URL 段，必须收紧。
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// ValidateID 校验插件 ID 合法性。
func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("插件 ID %q 不合法（须小写字母开头，仅小写字母/数字/连字符，2~32 位）", id)
	}
	return nil
}

// Manifest 是插件包内的 manifest.json 结构。
type Manifest struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Description    string   `json:"description"`
	Icon           string   `json:"icon"`  // emoji 或包内图标文件相对路径（如 icon.png），空则前端用默认 🧩
	Entry          string   `json:"entry"` // 入口 HTML 相对路径，默认 index.html
	Author         string   `json:"author"`
	Permissions    []string `json:"permissions"`
	MinHostVersion string   `json:"minHostVersion"` // 预留：宿主最低版本（当前不强制）
}

// EntryFile 返回入口文件名（缺省 index.html）。
func (m Manifest) EntryFile() string {
	if strings.TrimSpace(m.Entry) == "" {
		return "index.html"
	}
	return m.Entry
}

// Validate 校验 manifest 必填字段与格式。
func (m Manifest) Validate() error {
	if err := ValidateID(m.ID); err != nil {
		return err
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("插件 %s 缺少 name", m.ID)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("插件 %s 缺少 version", m.ID)
	}
	entry := m.EntryFile()
	// 入口必须是包内相对路径，防逃逸。
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(entry)), "./")
	if entry == "" || strings.HasPrefix(entry, "/") || strings.Contains(entry, "..") || clean != entry {
		return fmt.Errorf("插件 %s 的 entry %q 必须是包内相对路径", m.ID, entry)
	}
	for _, perm := range m.Permissions {
		if !knownPermission(perm) {
			return fmt.Errorf("插件 %s 声明了未知权限 %q", m.ID, perm)
		}
	}
	return nil
}

func knownPermission(perm string) bool {
	for _, known := range AllPermissions {
		if perm == known {
			return true
		}
	}
	return false
}

// Entry 是 plugins.json 登记表的单条记录。
// Builtin 插件随宿主内嵌分发：不可卸载、不可禁用，目录被删重启即恢复。
type Entry struct {
	ID          string    `json:"id"`
	Builtin     bool      `json:"builtin"`
	Disabled    bool      `json:"disabled"`
	Version     string    `json:"version"`
	Permissions []string  `json:"permissions"` // 安装时授权的权限快照（升级时以新 manifest 为准刷新）
	InstalledAt time.Time `json:"installedAt"`
}

// Info 是下发给前端的插件信息（合并 manifest 与登记状态）。
type Info struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Entry       string   `json:"entry"` // 入口 HTML 文件名（缺省 index.html）
	Builtin     bool     `json:"builtin"`
	Disabled    bool     `json:"disabled"`
	Permissions []string `json:"permissions"`
}
