package main

// 客户端在线升级的桌面端桥接：绑定方法（App 方法）+ 下载状态机 + 启动自动检查。
// 升级源是账号控制面（platform-drive 的 /easyshare/app/* 匿名接口），
// 详见 docs/iterations/2026-08-31-online-update.md。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"easyshare/internal/config"
	"easyshare/internal/fsutil"
	"easyshare/internal/update"
	"easyshare/internal/version"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// autoCheckInterval 启动自动检查的最小间隔；超过才真正发起。
const autoCheckInterval = 24 * time.Hour

// UpdateAssetInfo 是下发给前端的资产元数据（不含 URL——URL 下载前现取）。
type UpdateAssetInfo struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// UpdateCheckResult 是一次升级检查的结果。
type UpdateCheckResult struct {
	CurrentVersion string           `json:"currentVersion"`
	LatestVersion  string           `json:"latestVersion"`
	HasUpdate      bool             `json:"hasUpdate"`
	Notes          string           `json:"notes"`
	PublishedAt    string           `json:"publishedAt"`
	Asset          *UpdateAssetInfo `json:"asset"`
	// InstalledMode：当前是否 NSIS 安装版（Windows）。绿色版与 macOS 为 false。
	InstalledMode bool `json:"installedMode"`
	// CanAutoInstall：能否「重启并更新」——Windows 安装版且有 installer 资产。
	CanAutoInstall bool `json:"canAutoInstall"`
}

// UpdateProgress 是 update:progress 事件的载荷。
type UpdateProgress struct {
	Received int64   `json:"received"`
	Total    int64   `json:"total"`
	Speed    float64 `json:"speed"` // 字节/秒
}

// AppVersion 返回当前客户端版本号（设置页展示用，不依赖网络）。
func (a *App) AppVersion() string { return version.Version }

// CheckUpdate 向控制面检查新版本。发现新版本时缓存清单供后续下载使用。
func (a *App) CheckUpdate() (UpdateCheckResult, error) {
	return a.checkUpdate()
}

// StartUpdateDownload 开始下载最近一次检查到的资产。进度经 update:progress 事件推送，
// 完成/失败分别经 update:downloaded / update:error 事件通知（载荷见各事件）。
func (a *App) StartUpdateDownload() error {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	if a.updateAsset == nil {
		return fmt.Errorf("请先检查更新")
	}
	if a.updateDownloading {
		return fmt.Errorf("正在下载中")
	}
	a.updateDownloading = true
	go a.downloadUpdate()
	return nil
}

// ApplyUpdate 应用已下载的更新：
//   - Windows 安装版：静默安装（/S /update）并退出，安装完由安装包自动重启应用；
//   - 其他场景（macOS / 绿色版）：解析下载 URL 交给浏览器，用户手动安装。
func (a *App) ApplyUpdate() error {
	a.updateMu.Lock()
	filePath := a.updateFilePath
	asset := a.updateAsset
	a.updateMu.Unlock()

	if filePath == "" || asset == nil {
		return fmt.Errorf("尚未下载更新")
	}
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("安装包不存在，请重新下载：%w", err)
	}

	if goruntime.GOOS == "windows" && update.IsInstalledMode() {
		a.logger.Printf("update: launching installer %s", filePath)
		if err := update.LaunchInstaller(filePath); err != nil {
			a.reportError("launch update installer", err)
			return fmt.Errorf("启动安装包失败：%w", err)
		}
		// 先优雅停 Core 再由安装包接管（安装区段会 taskkill 残留进程）
		a.beginQuit()
		runtime.Quit(a.ctx)
		return nil
	}

	// macOS / 绿色版：打开下载链接由浏览器接管
	client := a.newUpdateClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url, err := client.DownloadURL(ctx, asset.ID)
	if err != nil {
		a.reportError("resolve update url", err)
		return err
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

// OpenUpdatesFolder 在文件管理器中打开升级包目录（绿色版手动安装引导）。
func (a *App) OpenUpdatesFolder() error {
	err := fsutil.OpenFolder(a.updatesDir())
	a.reportError("open updates folder", err)
	return err
}

// --- 内部实现 ---

// checkUpdate 拉清单并比较版本；命中新版本时缓存清单与资产。
func (a *App) checkUpdate() (UpdateCheckResult, error) {
	platform := updatePlatform()
	client := a.newUpdateClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	manifest, err := client.Latest(ctx, platform)
	if err != nil {
		a.reportError("check update", err)
		return UpdateCheckResult{}, err
	}
	if manifest == nil {
		return UpdateCheckResult{}, fmt.Errorf("服务端暂无已发布版本")
	}

	result := UpdateCheckResult{
		CurrentVersion: version.Version,
		LatestVersion:  manifest.Version,
		HasUpdate:      update.CompareVersions(manifest.Version, version.Version) > 0,
		Notes:          manifest.Notes,
		PublishedAt:    manifest.PublishedAt,
		InstalledMode:  update.IsInstalledMode(),
	}
	asset := manifest.SelectAsset(platform)
	if asset != nil {
		result.Asset = &UpdateAssetInfo{
			ID: asset.ID, Kind: asset.Kind, Filename: asset.Filename,
			Size: asset.Size, SHA256: asset.SHA256,
		}
	}
	result.CanAutoInstall = result.HasUpdate && result.InstalledMode &&
		asset != nil && asset.Kind == "installer" && goruntime.GOOS == "windows"

	if result.HasUpdate && asset != nil {
		a.updateMu.Lock()
		a.updateManifest = manifest
		a.updateAsset = asset
		a.updateMu.Unlock()
	}
	return result, nil
}

// downloadUpdate 后台下载；进度 200ms 节流 + 实时速度，结果经事件通知前端。
func (a *App) downloadUpdate() {
	defer func() {
		a.updateMu.Lock()
		a.updateDownloading = false
		a.updateMu.Unlock()
	}()

	a.updateMu.Lock()
	asset := a.updateAsset
	a.updateMu.Unlock()
	if asset == nil {
		a.emitUpdateError(fmt.Errorf("请先检查更新"))
		return
	}

	client := a.newUpdateClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	url, err := client.DownloadURL(ctx, asset.ID)
	cancel()
	if err != nil {
		a.reportError("resolve update url", err)
		a.emitUpdateError(err)
		return
	}

	dest := filepath.Join(a.updatesDir(), asset.Filename)
	started := time.Now()
	var lastEmit time.Time
	err = update.Download(context.Background(), a.updateHTTPClient(), url, dest, asset.SHA256, asset.Size,
		func(received, total int64) {
			now := time.Now()
			if now.Sub(lastEmit) < 200*time.Millisecond && received != total {
				return
			}
			lastEmit = now
			elapsed := now.Sub(started).Seconds()
			speed := 0.0
			if elapsed > 0.2 {
				speed = float64(received) / elapsed
			}
			runtime.EventsEmit(a.ctx, "update:progress", UpdateProgress{Received: received, Total: total, Speed: speed})
		})
	if err != nil {
		a.reportError("download update", err)
		a.emitUpdateError(err)
		return
	}
	a.logger.Printf("update: downloaded %s (%d bytes)", dest, asset.Size)

	a.updateMu.Lock()
	a.updateFilePath = dest
	a.updateMu.Unlock()
	// 清掉历史旧包，只留本次
	update.CleanStaleDownloads(a.updatesDir(), dest)
	runtime.EventsEmit(a.ctx, "update:downloaded", map[string]any{"path": dest, "version": a.manifestVersion()})
}

// autoUpdateCheck 启动后的静默检查：距上次超过 24h 才发请求；
// 发现新版本向前端发 update:available 事件（前端设置入口打红点）。
func (a *App) autoUpdateCheck() {
	if !a.shouldAutoCheck() {
		return
	}
	a.saveLastCheckAt(time.Now())
	result, err := a.checkUpdate()
	if err != nil {
		a.logger.Printf("update: auto check skipped: %v", err)
		return
	}
	if !result.HasUpdate {
		return
	}
	a.logger.Printf("update: new version available %s (current %s)", result.LatestVersion, result.CurrentVersion)
	runtime.EventsEmit(a.ctx, "update:available", map[string]any{
		"currentVersion": result.CurrentVersion,
		"latestVersion":  result.LatestVersion,
		"notes":          result.Notes,
	})
}

func (a *App) emitUpdateError(err error) {
	runtime.EventsEmit(a.ctx, "update:error", map[string]any{"message": err.Error()})
}

func (a *App) manifestVersion() string {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	if a.updateManifest == nil {
		return ""
	}
	return a.updateManifest.Version
}

// updatesDir 升级包下载目录：与 config.json 同目录（%LOCALAPPDATA%\EasyShare\updates）。
func (a *App) updatesDir() string {
	return filepath.Join(filepath.Dir(a.configPath), "updates")
}

// newUpdateClient 每次从持久化配置取控制面地址，避免使用启动时的旧快照。
func (a *App) newUpdateClient() *update.Client {
	baseURL := a.config.PlatformBaseURL
	if value, err := config.Load(a.configPath); err == nil && value.PlatformBaseURL != "" {
		baseURL = value.PlatformBaseURL
	}
	return update.NewClient(baseURL)
}

// updateHTTPClient 下载专用：无整体超时（大文件），取消靠请求级 context。
func (a *App) updateHTTPClient() *http.Client {
	return &http.Client{}
}

// updatePlatform 当前平台在控制面清单里的标识。
func updatePlatform() string {
	if goruntime.GOOS == "darwin" {
		return update.PlatformMacOS
	}
	return update.PlatformWindows
}

// --- 自动检查节流状态（update-state.json）---

type updateState struct {
	LastCheckAt string `json:"lastCheckAt"`
}

func (a *App) updateStatePath() string {
	return filepath.Join(filepath.Dir(a.configPath), "update-state.json")
}

func (a *App) shouldAutoCheck() bool {
	data, err := os.ReadFile(a.updateStatePath())
	if err != nil {
		return true
	}
	var state updateState
	if err := json.Unmarshal(data, &state); err != nil {
		return true
	}
	last, err := time.Parse(time.RFC3339, state.LastCheckAt)
	if err != nil {
		return true
	}
	return time.Since(last) >= autoCheckInterval
}

func (a *App) saveLastCheckAt(now time.Time) {
	state := updateState{LastCheckAt: now.Format(time.RFC3339)}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(a.updateStatePath()), 0o755)
	_ = os.WriteFile(a.updateStatePath(), append(data, '\n'), 0o600)
}
