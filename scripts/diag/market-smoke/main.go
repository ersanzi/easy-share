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

	// 逐个安装商城插件
	for _, it := range items {
		if it.Asset == nil {
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

	// 内置插件释放（真实 assets embed 无法在此引用，冒烟用 Manager 登记语义即可）
	list := m.List()
	if len(list) != len(items) {
		fatal("登记数不符：期望 %d 实际 %d", len(items), len(list))
	}
	fmt.Println("商城安装链路（列表→下载→SHA256→安装→登记）全部通过 ✅")
}

func fatal(f string, args ...any) {
	fmt.Printf("❌ "+f+"\n", args...)
	os.Exit(1)
}
