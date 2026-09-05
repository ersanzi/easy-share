package main

// appplugin.go：插件系统的桌面端集成。
// 静态绑定只此一组（PluginList/PluginInvoke/管理动作），插件能力全部经
// PluginInvoke 动态通道进入 internal/plugin 的能力注册表，避免 Wails 绑定级联。
// 剪切板服务是宿主能力（内置插件消费），随主程序分发、数据不随插件消失。

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"easyshare/internal/clipboard"
	"easyshare/internal/drive"
	"easyshare/internal/plugin"
	"easyshare/internal/update"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:assets all:plugins/clipboard
var pluginAssets embed.FS

// PluginInvokeResult 是 PluginInvoke 的统一返回（前端桥按 ok 分发 data/error）。
type PluginInvokeResult struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// initPluginSystem 在 Startup 时创建插件管理器与剪切板服务并注册能力。
// 失败不阻断主程序（插件系统是增强能力），记入运行日志与错误面板。
func (a *App) initPluginSystem() {
	// 无论成败，退出即放行等待方（PluginList 等绑定），失败时它们拿到空列表而非挂死。
	defer close(a.pluginReady)

	dataRoot := filepath.Dir(a.configPath) // 与 config.json 同级（%LOCALAPPDATA%\EasyShare）

	manager, err := plugin.NewManager(dataRoot)
	if err != nil {
		a.reportError("init plugins", err)
		return
	}

	// 剪切板是「首发插件」而非内置：源码在插件工程（plugins/clipboard），主仓
	// embed 直读该目录做**首次运行的种子**——落成普通插件（可卸载可禁用），之后
	// 更新走商城流程（含权限确认），卸载后不复活。注意：这使主仓对 plugins/ 存在
	// 一处构建期引用，插件仓拆分时需把该目录留在主仓（见拆分计划的例外记录）。
	clipFS, err := fs.Sub(pluginAssets, "plugins/clipboard")
	if err != nil {
		a.reportError("plugin assets", err)
		return
	}
	if err := manager.SeedPlugin("clipboard", clipFS); err != nil {
		a.reportError("seed clipboard plugin", err)
	}

	sdkFS, err := fs.Sub(pluginAssets, "assets")
	if err != nil {
		a.reportError("plugin assets", err)
		return
	}

	clipSvc, err := clipboard.NewService(dataRoot)
	if err != nil {
		a.reportError("init clipboard", err)
	}
	if clipSvc != nil {
		clipSvc.SetOnChange(func(e clipboard.Entry) {
			// 监听线程回调 → Wails 事件（EventsEmit 线程安全）
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "clipboard:changed", e)
			}
		})
	}

	registry := plugin.NewRegistry()
	a.registerCapabilities(registry, clipSvc)

	a.pluginManager = manager
	a.pluginRegistry = registry
	a.pluginSDK = sdkFS
	a.clipboardService = clipSvc
	// 资产路由此时才注册：mux 在 NewApp 即创建（main.go 构造期就要交给
	// AssetServer），而插件管理器到这里才就绪。
	a.assetMux.Handle("/plugins/", manager.HTTPHandler(sdkFS))
	if clipSvc != nil {
		a.assetMux.Handle("/clipboard-files/", clipSvc.FilesHandler())
	}
	// 面板事件通道：剪切板变化并行推给快捷面板页（主窗 iframe 走 Wails 事件）。
	if clipSvc != nil {
		clipSvc.AddOnChange(func(e clipboard.Entry) {
			a.panelEmitEvent("clipboard:changed", e)
		})
	}
	// 剪切板插件的录制与快捷面板随插件在场状态启停（装/卸/启停都会重新对齐）。
	a.syncClipboardSurface()
	// 全局搜索面板：不随插件启停，启动即注册热键（未登录时搜索降级为仅文件路/空结果）。
	startSearchPanel(a)
	// 启动后延迟检查插件更新（发现新版 → plugin:updates-available 事件 → 插件中心红点）
	go a.checkPluginUpdates()
	a.logger.Printf("plugin system ready; installed=%d", len(manager.List()))
}

// registerCapabilities 把宿主能力注册进插件能力注册表。
// drive.upload 在批次 2（商城待办插件）接入。
func (a *App) registerCapabilities(r *plugin.Registry, clip *clipboard.Service) {
	if clip != nil {
		r.Register("clipboard.history", plugin.PermClipboardRead, func(args json.RawMessage) (any, error) {
			var req struct {
				Limit  int    `json:"limit"`
				Offset int    `json:"offset"`
				Kind   string `json:"kind"`
				Query  string `json:"query"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &req); err != nil {
					return nil, fmt.Errorf("参数须为 {limit,offset,kind,query}")
				}
			}
			return clip.List(req.Limit, req.Offset, req.Kind, req.Query), nil
		})
		r.Register("clipboard.delete", plugin.PermClipboardRead, func(args json.RawMessage) (any, error) {
			var req struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &req); err != nil || req.ID == "" {
				return nil, fmt.Errorf("clipboard.delete 需要 {id}")
			}
			return true, clip.Delete(req.ID)
		})
		r.Register("clipboard.stats", plugin.PermClipboardRead, func(args json.RawMessage) (any, error) {
			return clip.Stats(), nil
		})
		r.Register("clipboard.clear", plugin.PermClipboardRead, func(args json.RawMessage) (any, error) {
			return true, clip.Clear()
		})
		r.Register("clipboard.write", plugin.PermClipboardWrite, func(args json.RawMessage) (any, error) {
			var req clipboard.WriteRequest
			if err := json.Unmarshal(args, &req); err != nil {
				return nil, fmt.Errorf("clipboard.write 参数错误")
			}
			return true, clip.Write(req)
		})
		r.Register("clipboard.settings", plugin.PermClipboardRead, func(args json.RawMessage) (any, error) {
			var req struct {
				Paused     *bool `json:"paused"`
				MaxEntries int   `json:"maxEntries"`
				AutoStart  *bool `json:"autoStart"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &req); err != nil {
					return nil, fmt.Errorf("clipboard.settings 参数错误")
				}
				if req.Paused != nil {
					if err := clip.SetPaused(*req.Paused); err != nil {
						return nil, err
					}
				}
				if req.MaxEntries > 0 {
					st := clip.Settings()
					st.MaxEntries = req.MaxEntries
					if err := clip.SaveSettings(st); err != nil {
						return nil, err
					}
				}
				if req.AutoStart != nil {
					if err := clip.SetAutoStart(*req.AutoStart); err != nil {
						return nil, err
					}
				}
			}
			// 自启状态以 OS（HKCU Run 键）为唯一真相源，不落 settings.json；
			// 平台不支持时 supported=false，UI 据此隐藏开关
			autoStart, err := clip.AutoStartEnabled()
			if err != nil {
				autoStart = false
			}
			return struct {
				clipboard.Settings
				AutoStart          bool `json:"autoStart"`
				AutoStartSupported bool `json:"autoStartSupported"`
			}{Settings: clip.Settings(), AutoStart: autoStart, AutoStartSupported: clip.AutoStartSupported()}, nil
		})
	}
	r.Register("notification.show", plugin.PermNotification, func(args json.RawMessage) (any, error) {
		var req struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := json.Unmarshal(args, &req); err != nil || strings.TrimSpace(req.Title) == "" {
			return nil, fmt.Errorf("notification.show 需要 {title,body}")
		}
		if a.ctx != nil {
			runtime.SendNotification(a.ctx, runtime.NotificationOptions{Title: req.Title, Body: req.Body})
		}
		return true, nil
	})
	// drive.upload：把插件生成的文本内容（如周报 markdown）上传到个人云盘空间。
	// 走与「网盘上传」相同的预签名直传与统一任务通道，进度在活动抽屉可见。
	r.Register("drive.upload", plugin.PermDriveUpload, func(args json.RawMessage) (any, error) {
		var req struct {
			Filename string `json:"filename"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal(args, &req); err != nil || strings.TrimSpace(req.Filename) == "" || req.Content == "" {
			return nil, fmt.Errorf("drive.upload 需要 {filename,content}")
		}
		core, driveClient, token, err := a.uploadClients()
		if err != nil {
			return nil, err
		}
		// 写临时文件复用统一上传管线（临时目录随系统清理策略，不留在插件可见位置）。
		tmp, err := os.CreateTemp("", "eshare-plugin-upload-*-"+filepath.Base(req.Filename))
		if err != nil {
			return nil, fmt.Errorf("创建临时文件: %w", err)
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if _, err := tmp.WriteString(req.Content); err != nil {
			_ = tmp.Close()
			return nil, fmt.Errorf("写临时文件: %w", err)
		}
		_ = tmp.Close()
		go a.uploadSingleFile(core, driveClient, token, drive.SpacePersonal, tmpPath, req.Filename)
		return true, nil
	})
}

// PluginMarketList 拉取商城列表，并按本地已装版本回填「可更新」标记。
func (a *App) PluginMarketList() ([]plugin.MarketItem, error) {
	items, err := a.marketItems()
	if err != nil {
		return nil, err
	}
	if a.pluginManager != nil {
		for i := range items {
			if info, ok := a.pluginManager.Get(items[i].ID); ok {
				items[i].UpdateAvailable = update.CompareVersions(items[i].Version, info.Version) > 0
			}
		}
	}
	return items, nil
}

// marketItems 拉商城列表（带控制面地址校验）。
func (a *App) marketItems() ([]plugin.MarketItem, error) {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return nil, fmt.Errorf("未配置账号服务地址")
	}
	return plugin.NewMarketClient(base).List(a.ctx)
}

// checkPluginUpdates 启动后的插件更新检查（延迟执行避免与启动抢资源）。
// 与主程序升级检查同思路：发现新版 → 事件通知前端在「插件中心」入口亮红点。
// 无需落盘节流——单个匿名 GET 很轻，每次启动查一次即可。
func (a *App) checkPluginUpdates() {
	time.Sleep(15 * time.Second) // 延迟执行，避免与启动抢资源
	items, err := a.marketItems()
	if err != nil {
		a.logger.Printf("plugin update check: %v", err) // 控制面不可达等：静默，下次启动再查
		return
	}
	var notices []map[string]string
	for _, it := range items {
		info, ok := a.pluginManager.Get(it.ID)
		if ok && update.CompareVersions(it.Version, info.Version) > 0 {
			notices = append(notices, map[string]string{"id": it.ID, "name": it.Name, "version": it.Version})
		}
	}
	if len(notices) > 0 && a.ctx != nil {
		runtime.EventsEmit(a.ctx, "plugin:updates-available", notices)
		a.logger.Printf("plugin updates available: %d", len(notices))
	}
}

// PluginPreviewFromMarket 商城安装第一步：下载并校验插件包（不落成安装），
// 返回新版本概要与需用户确认的权限清单（首装=全部声明，更新=相对本地新增）。
func (a *App) PluginPreviewFromMarket(assetID, expectedSHA256 string, expectedSizeBytes int64) (plugin.PreviewResult, error) {
	if a.pluginManager == nil {
		return plugin.PreviewResult{}, fmt.Errorf("插件系统未初始化")
	}
	data, err := a.downloadMarketAsset(assetID, expectedSHA256, expectedSizeBytes)
	if err != nil {
		return plugin.PreviewResult{}, err
	}
	return a.pluginManager.PreviewInstall(data, expectedSHA256)
}

// PluginInstallFromMarket 商城安装第二步：带权限同意完成安装。
// acceptedPermissions 是用户在确认框里同意的权限集合；包内新增权限超出该集合时拒绝安装。
func (a *App) PluginInstallFromMarket(assetID, expectedSHA256 string, expectedSizeBytes int64, acceptedPermissions []string) (plugin.Info, error) {
	if a.pluginManager == nil {
		return plugin.Info{}, fmt.Errorf("插件系统未初始化")
	}
	data, err := a.downloadMarketAsset(assetID, expectedSHA256, expectedSizeBytes)
	if err != nil {
		return plugin.Info{}, err
	}
	man, err := a.pluginManager.InstallWithConsent(data, expectedSHA256, acceptedPermissions)
	if err != nil {
		return plugin.Info{}, err
	}
	a.syncClipboardSurface() // 装的是剪切板插件时，恢复录制与快捷面板
	info, ok := a.pluginManager.Get(man.ID)
	if !ok {
		return plugin.Info{}, fmt.Errorf("安装完成但读取插件信息失败")
	}
	return info, nil
}

// downloadMarketAsset 从商城下载插件包（大小与来源校验）。
func (a *App) downloadMarketAsset(assetID, expectedSHA256 string, expectedSizeBytes int64) ([]byte, error) {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return nil, fmt.Errorf("未配置账号服务地址")
	}
	asset := plugin.MarketAsset{ID: assetID, SizeBytes: expectedSizeBytes, SHA256: expectedSHA256}
	return plugin.NewMarketClient(base).Download(a.ctx, asset)
}

// AssetHandler 返回挂到 Wails AssetServer fallback 的组合静态服务。
// mux 在 NewApp 创建（插件路由在 initPluginSystem 完成后注册，Go 1.22+ 运行时注册安全）：
// /plugins/... 给插件包与公共 SDK，/clipboard-files/... 给剪切板图片。
func (a *App) AssetHandler() http.Handler {
	return a.assetMux
}

// pluginAssetMiddleware 把宿主动态资源提到 AssetServer 链路最前。
// 仅靠 fallback Handler 在 dev 下失效：wails dev 的前端由 Vite 提供，
// 它对未知路径回 200（SPA fallback），请求永远到不了 fallback，
// 插件 iframe 会加载到前端应用本身。因此这两个前缀必须在 Middleware
// 阶段直接接管，dev 与生产行为才一致。
// 注意保持包级函数：作为 App 导出方法会被 wails 绑定且其返回类型
// （assetserver.Middleware 是函数类型）不会生成 models，前端 TS 必挂。
func pluginAssetMiddleware(a *App) assetserver.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if strings.HasPrefix(path, "/plugins/") || strings.HasPrefix(path, "/clipboard-files/") {
				a.AssetHandler().ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PluginList 返回全部已安装插件（含内置与禁用状态）。
// 前端可能在 initPluginSystem 完成前就发起本调用（生产启动实测存在），
// 此时 manager 为 nil、空列表会被当成「已装 0 个」且前端不再重试——
// 因此这里先等就绪闸，超时兜底放行。
func (a *App) PluginList() []plugin.Info {
	if a.pluginReady != nil {
		select {
		case <-a.pluginReady:
		case <-time.After(3 * time.Second):
		}
	}
	if a.pluginManager == nil {
		return []plugin.Info{}
	}
	return a.pluginManager.List()
}

// PluginInvoke 插件能力调用的唯一动态通道。
func (a *App) PluginInvoke(pluginID, api, argsJSON string) PluginInvokeResult {
	if a.pluginManager == nil || a.pluginRegistry == nil {
		return PluginInvokeResult{Error: "插件系统未初始化"}
	}
	// 已禁用/未安装的插件不给调用能力。
	info, ok := a.pluginManager.Get(pluginID)
	if !ok {
		return PluginInvokeResult{Error: "插件未安装"}
	}
	if info.Disabled {
		return PluginInvokeResult{Error: "插件已禁用"}
	}
	var args json.RawMessage
	if strings.TrimSpace(argsJSON) != "" {
		args = json.RawMessage(argsJSON)
	}
	data, err := a.pluginManager.InvokeFor(a.pluginRegistry, pluginID, api, args)
	if err != nil {
		return PluginInvokeResult{Error: err.Error()}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return PluginInvokeResult{Error: fmt.Sprintf("序列化结果: %v", err)}
	}
	return PluginInvokeResult{OK: true, Data: encoded}
}

// PluginInstallFromPath 从本地 zip 包安装/更新插件（设置页「从 zip 安装」入口）。
func (a *App) PluginInstallFromPath(path string) (plugin.Info, error) {
	if a.pluginManager == nil {
		return plugin.Info{}, fmt.Errorf("插件系统未初始化")
	}
	man, err := a.pluginManager.InstallZip(path)
	if err != nil {
		return plugin.Info{}, err
	}
	a.syncClipboardSurface() // 本地 zip 装/升级剪切板插件时同步录制与面板
	info, ok := a.pluginManager.Get(man.ID)
	if !ok {
		return plugin.Info{}, fmt.Errorf("安装完成但读取插件信息失败")
	}
	return info, nil
}

// PluginSetDisabled 启用/禁用插件（内置插件不可禁用）。
func (a *App) PluginSetDisabled(id string, disabled bool) error {
	if a.pluginManager == nil {
		return fmt.Errorf("插件系统未初始化")
	}
	if err := a.pluginManager.SetDisabled(id, disabled); err != nil {
		return err
	}
	a.syncClipboardSurface() // 剪切板插件禁用/启用时同步录制与快捷面板
	return nil
}

// PluginUninstall 卸载插件（内置插件不可卸载）。
func (a *App) PluginUninstall(id string) error {
	if a.pluginManager == nil {
		return fmt.Errorf("插件系统未初始化")
	}
	if err := a.pluginManager.Uninstall(id); err != nil {
		return err
	}
	a.syncClipboardSurface() // 剪切板是普通插件：卸载即停录制、收面板、释放热键
	return nil
}
