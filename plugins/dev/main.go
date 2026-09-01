// dev 是插件开发安装辅助：把 plugins/ 下的插件开发目录以目录联接（junction）
// 映射进宿主插件目录并登记 plugins.json——改文件即时生效（宿主 serve no-store，
// 插件页切走切回即重载，无需重启应用）。
//
// 用法（仓库根目录）：
//
//	go run ./plugins/dev -plugin todo          # 建立/刷新映射
//	go run ./plugins/dev -plugin todo -remove  # 移除映射
//
// 逻辑刻意保持薄：junction 经 cmd 的 mklink /J（无需管理员），登记追加进
// plugins.json（宿主按登记表决定可见性与能力权限快照）。
// 注：另有 plugins/dev.ps1 薄壳包装本工具（powershell -File plugins\dev.ps1 -Plugin todo）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

// 插件 ID 规则（与 manifest.id 一致）。
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

func main() {
	plugin := flag.String("plugin", "", "插件目录名（plugins/ 下，= manifest.id）")
	remove := flag.Bool("remove", false, "移除开发映射")
	root := flag.String("root", "", "plugins 目录（默认 <cwd>/plugins）")
	flag.Parse()
	if !idPattern.MatchString(*plugin) {
		fatal("插件目录名须为合法插件 ID（小写字母开头，字母/数字/连字符，2~32 位）：%q", *plugin)
	}

	// 开发目录定位：默认 cwd/plugins（仓库根执行），可 -root 显式指定。
	src := filepath.Join(pluginsRoot(*root), *plugin)
	manifestPath := filepath.Join(src, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		fatal("缺少 manifest.json：%s（确认 -plugin 是 plugins/ 下的插件目录名）", manifestPath)
	}

	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "EasyShare")
	dest := filepath.Join(base, "plugins", *plugin)

	if *remove {
		if _, err := os.Lstat(dest); err != nil {
			fmt.Printf("本就不存在：%s\n", dest)
			return
		}
		// rmdir 对 junction 只删联接本身，不会递归删到开发目录。
		if out, err := exec.Command("cmd", "/c", "rmdir", dest).CombinedOutput(); err != nil {
			fatal("移除映射失败：%v（%s）", err, string(out))
		}
		fmt.Printf("已移除：%s\n", dest)
		return
	}

	if fi, err := os.Lstat(dest); err == nil {
		// 已存在：Readlink 成功即联接（Go 在 Windows 对 junction 的 ModeSymlink
		// 标记跨版本不稳，以能否读出目标为准），且须指向本开发目录。
		target, linkErr := os.Readlink(dest)
		if linkErr != nil {
			_ = fi
			fatal("目标已存在且不是联接（可能是正式安装版，先卸载）：%s", dest)
		}
		if !samePath(target, src) {
			fatal("目标联接不指向本开发目录：%s -> %s（先 -remove 清理）", dest, target)
		}
		fmt.Printf("映射已存在且指向本开发目录：%s\n", dest)
	} else {
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			fatal("创建插件根目录: %v", err)
		}
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", dest, src).CombinedOutput(); err != nil {
			fatal("建立 junction 失败：%v（%s）", err, string(out))
		}
		fmt.Printf("开发映射已建立：%s -> %s\n", dest, src)
	}

	register(base, manifestPath, *plugin)
	fmt.Println("启动 EasyShare 后在侧边栏打开该插件即可；改文件后切页重载。")
}

// pluginsRoot 定位 plugins 目录：优先 -root，其次 cwd/plugins。
func pluginsRoot(root string) string {
	if root != "" {
		return root
	}
	return filepath.Join(mustAbs("."), "plugins")
}

// register 确保 plugins.json 有该插件条目（已有则不动，保留禁用状态等）。
func register(base, manifestPath, id string) {
	type entry struct {
		ID          string   `json:"id"`
		Builtin     bool     `json:"builtin"`
		Disabled    bool     `json:"disabled"`
		Version     string   `json:"version"`
		Permissions []string `json:"permissions"`
		InstalledAt string   `json:"installedAt"`
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		fatal("读 manifest: %v", err)
	}
	var man struct {
		ID          string   `json:"id"`
		Version     string   `json:"version"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(data, &man); err != nil {
		fatal("解析 manifest: %v", err)
	}
	if man.ID != id {
		fatal("manifest.id（%s）与目录名（%s）不一致", man.ID, id)
	}

	registry := filepath.Join(base, "plugins.json")
	var entries []entry
	if data, err := os.ReadFile(registry); err == nil {
		_ = json.Unmarshal(data, &entries) // 损坏则重建
	}
	for _, e := range entries {
		if e.ID == id {
			fmt.Printf("plugins.json 已有条目：%s（权限快照以登记为准，改了 manifest 权限时先 -remove 再重装）\n", id)
			return
		}
	}
	entries = append(entries, entry{
		ID: id, Builtin: false, Disabled: false,
		Version: man.Version, Permissions: man.Permissions,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	})
	out, _ := json.MarshalIndent(entries, "", "  ")
	tmp := registry + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		fatal("写登记表: %v", err)
	}
	if err := os.Rename(tmp, registry); err != nil {
		fatal("替换登记表: %v", err)
	}
	fmt.Printf("已登记进 plugins.json（id=%s v%s 权限 %d 项）\n", id, man.Version, len(man.Permissions))
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		fatal("解析绝对路径: %v", err)
	}
	return abs
}

func samePath(a, b string) bool {
	return filepath.Clean(mustAbs(a)) == filepath.Clean(mustAbs(b))
}

func fatal(f string, args ...any) {
	fmt.Printf("❌ "+f+"\n", args...)
	os.Exit(1)
}
