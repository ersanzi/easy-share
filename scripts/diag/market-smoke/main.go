// 一次性冒烟：从 dev 控制面商城真实下载并安装插件（market.go → InstallBytes 全链路）。
// 前置：RuoYi 8090 运行中且已上架插件。用完即删。
package main

import (
	"context"
	"fmt"
	"os"

	"easyshare/internal/plugin"
)

func main() {
	base := "http://localhost:8090"
	client := plugin.NewMarketClient(base)
	ctx := context.Background()

	items, err := client.List(ctx)
	if err != nil {
		fatal("商城列表: %v", err)
	}
	if len(items) == 0 {
		fatal("商城为空，请先发布插件")
	}
	fmt.Printf("商城 %d 个插件：", len(items))
	for _, it := range items {
		fmt.Printf(" %s v%s", it.ID, it.Version)
	}
	fmt.Println()

	root, _ := os.MkdirTemp("", "market-smoke-*")
	defer os.RemoveAll(root)
	m, err := plugin.NewManager(root)
	if err != nil {
		fatal("NewManager: %v", err)
	}

	// 可选：先装本地旧版 zip（os.Args[1]），模拟「用户已装旧版」再走商城更新流
	if len(os.Args) > 1 {
		if _, err := m.InstallZip(os.Args[1]); err != nil {
			fatal("预装旧版 %s: %v", os.Args[1], err)
		}
		fmt.Printf("已预装本地旧版：%s\n", os.Args[1])
	}

	// 逐个安装商城插件（本地已装且商城版本不高时跳过初装，走后面的更新流）
	for _, it := range items {
		if it.Asset == nil {
			continue
		}
		if _, ok := m.Get(it.ID); ok {
			fmt.Printf("跳过初装：%s 本地已装，走更新流\n", it.ID)
			continue
		}
		data, err := client.Download(ctx, *it.Asset)
		if err != nil {
			fatal("下载 %s: %v", it.ID, err)
		}
		man, err := m.InstallBytes(data, it.Asset.SHA256)
		if err != nil {
			fatal("安装 %s: %v", it.ID, err)
		}
		info, _ := m.Get(man.ID)
		fmt.Printf("安装成功: %s v%s（permissions=%d 项）\n", info.ID, info.Version, len(info.Permissions))
	}

	// 更新流验证：预览识别为更新 → 权限 diff 为空（1.0.1 未新增权限）→ 同意集直接升级
	for _, it := range items {
		if it.Asset == nil {
			continue
		}
		data, err := client.Download(ctx, *it.Asset)
		if err != nil {
			fatal("重新下载 %s: %v", it.ID, err)
		}
		preview, err := m.PreviewInstall(data, it.Asset.SHA256)
		if err != nil {
			fatal("预览 %s: %v", it.ID, err)
		}
		if !preview.IsUpdate {
			fatal("%s 预览应识别为更新（本地已装 %s，商城 %s）", it.ID, preview.InstalledVersion, preview.Version)
		}
		fmt.Printf("更新预览: %s 本地 v%s → 商城 v%s，新增权限 %d 项\n",
			it.ID, preview.InstalledVersion, preview.Version, len(preview.NewPermissions))
		man, err := m.InstallWithConsent(data, it.Asset.SHA256, preview.NewPermissions)
		if err != nil {
			fatal("升级 %s: %v", it.ID, err)
		}
		if man.Version != it.Version {
			fatal("升级后版本不符：期望 %s 实际 %s", it.Version, man.Version)
		}
		fmt.Printf("升级成功: %s → v%s ✅\n", it.ID, man.Version)
	}

	// 内置插件释放（真实 assets embed 无法在此引用，冒烟用 Manager 登记语义即可）
	list := m.List()
	if len(list) != len(items) {
		fatal("登记数不符：期望 %d 实际 %d", len(items), len(list))
	}
	fmt.Println("商城安装/更新链路（列表→下载→SHA256→安装/预览→权限diff→升级→登记）全部通过 ✅")
}

func fatal(f string, args ...any) {
	fmt.Printf("❌ "+f+"\n", args...)
	os.Exit(1)
}
