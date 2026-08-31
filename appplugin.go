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

	"easyshare/internal/clipboard"
	"easyshare/internal/drive"
	"easyshare/internal/plugin"
	"easyshare/internal/update"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:assets
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
	dataRoot := filepath.Dir(a.configPath) // 与 config.json 同级（%LOCALAPPDATA%\EasyShare）

	manager, err := plugin.NewManager(dataRoot)
	if err != nil {
		a.reportError("init plugins", err)
		return
	}

	builtinFS, err := fs.Sub(pluginAssets, "assets/builtin-plugins")
	if err != nil {
		a.reportError("plugin assets", err)
		return
	}
	if err := manager.EnsureBuiltin(builtinFS); err != nil {
		a.reportError("ensure builtin plugins", err)
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
		if err := clipSvc.Start(); err != nil {
			a.logger.Printf("clipboard listener: %v", err)
		}
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
				Paused    *bool  `json:"paused"`
				MaxEntries int   `json:"maxEntries"`
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
			}
			return clip.Settings(), nil
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
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return nil, fmt.Errorf("未配置账号服务地址")
	}
	items, err := plugin.NewMarketClient(base).List(a.ctx)
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

// PluginInstallFromMarket 从商城安装/更新插件：下载（SHA256 校验）→ 解压安装 → 登记。
func (a *App) PluginInstallFromMarket(assetID, expectedSHA256 string, expectedSizeBytes int64) (plugin.Info, error) {
	if a.pluginManager == nil {
		return plugin.Info{}, fmt.Errorf("插件系统未初始化")
	}
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return plugin.Info{}, fmt.Errorf("未配置账号服务地址")
	}
	asset := plugin.MarketAsset{ID: assetID, SizeBytes: expectedSizeBytes, SHA256: expectedSHA256}
	data, err := plugin.NewMarketClient(base).Download(a.ctx, asset)
	if err != nil {
		return plugin.Info{}, err
	}
	man, err := a.pluginManager.InstallBytes(data, expectedSHA256)
	if err != nil {
		return plugin.Info{}, err
	}
	info, ok := a.pluginManager.Get(man.ID)
	if !ok {
		return plugin.Info{}, fmt.Errorf("安装完成但读取插件信息失败")
	}
	return info, nil
}

// AssetHandler 返回挂到 Wails AssetServer fallback 的组合静态服务。
// mux 在 NewApp 创建（插件路由在 initPluginSystem 完成后注册，Go 1.22+ 运行时注册安全）：
// /plugins/... 给插件包与公共 SDK，/clipboard-files/... 给剪切板图片。
func (a *App) AssetHandler() http.Handler {
	return a.assetMux
}

// PluginList 返回全部已安装插件（含内置与禁用状态）。
func (a *App) PluginList() []plugin.Info {
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
	return a.pluginManager.SetDisabled(id, disabled)
}

// PluginUninstall 卸载插件（内置插件不可卸载）。
func (a *App) PluginUninstall(id string) error {
	if a.pluginManager == nil {
		return fmt.Errorf("插件系统未初始化")
	}
	return a.pluginManager.Uninstall(id)
}
