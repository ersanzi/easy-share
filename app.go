package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/clipboard"
	"easyshare/internal/cloud"
	"easyshare/internal/config"
	"easyshare/internal/desktop"
	"easyshare/internal/drive"
	"easyshare/internal/driveerr"
	"easyshare/internal/fsutil"
	"easyshare/internal/knowledge"
	"easyshare/internal/logging"
	"easyshare/internal/namespace"
	"easyshare/internal/plugin"
	"easyshare/internal/update"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App bridges the Wails frontend to the background Core process.
type App struct {
	ctx        context.Context
	client     *desktop.Client
	clientLock sync.RWMutex
	config     config.Config
	configPath string
	logFile    *os.File
	logger     *log.Logger
	errorLock  sync.Mutex
	lastError  string
	lastAt     time.Time

	// Core process management
	processOptions desktop.ProcessOptions

	// Tray and window lifecycle
	quitting     bool
	quitLock     sync.Mutex
	trayStatusCh chan string
	trayOnce     sync.Once

	// 账号控制面（RuoYi）会话。token 只保管在桌面端进程，不下发前端。
	accountMu      sync.RWMutex
	accountSession *account.Session
	// trayUserCh 把登录态推给托盘悬浮窗（模式同 trayStatusCh）。
	trayUserCh chan AuthUser
	// mounts 管「此电脑」里两个云端空间的 WebDAV 端点与条目，见 spacemount.go。
	mounts *spaceMounts
	// cfapi 管 CfApi 按需占位符同步根（切片 5 生产接入，见 cfmount_app_windows.go；
	// 非 Windows 平台该结构为空壳，不参与任何流程）。
	cfapi cfapiMounts
	// dropSpace 是悬浮窗切换器选中的目标空间（拖入的文件进哪个空间）。
	// 空串表示未选择，按个人空间处理。由浮窗线程写、上传路径读，故加锁。
	dropSpaceMu sync.Mutex
	dropSpace   string
	// 在线升级状态（绑定方法见 appupdate.go）
	updateMu          sync.Mutex
	updateManifest    *update.Manifest
	updateAsset       *update.Asset
	updateFilePath    string
	updateDownloading bool
	// 插件系统与剪切板服务（集成见 appplugin.go）
	assetMux         *http.ServeMux
	pluginManager    *plugin.Manager
	pluginRegistry   *plugin.Registry
	pluginSDK        fs.FS
	clipboardService *clipboard.Service
	// 插件系统就绪闸：initPluginSystem 退出（成功或失败）时关闭。
	// 前端可能在 Startup 完成前就调绑定方法，PluginList 借此等到真实列表，
	// 避免把「未就绪」当成「已装 0 个插件」返回给前端（插件入口全消失的根因）。
	pluginReady chan struct{}

	// 快捷面板（剪切板插件 ?panel=1 形态的宿主窗口，见 panel_surface.go 与平台实现）。
	// panelEmit 由平台窗口就绪后注入：向面板页推送事件（clipboard:changed、panel:shown）。
	panelListener net.Listener
	panelURL      string
	// searchURL 全局搜索表面（第二面板窗口，与剪切板面板共用了同一 loopback 静态服务）
	searchURL string
	// searchEmit 搜索面板的 Eval 通道（异步回投搜索结果；窗口就绪后注入）
	searchEmitMu sync.RWMutex
	searchEmit   func(script string)
	panelEmitMu  sync.RWMutex
	panelEmit    func(event string, payload any)
}

// AuthUser 是下发给前端的登录态（不含 token）。
//
// IsAdmin 只决定「管理」入口是否显示，不承担鉴权——后台接口自己按 Sa-Token 角色拒绝。
type AuthUser struct {
	LoggedIn bool   `json:"loggedIn"`
	UserName string `json:"userName"`
	NickName string `json:"nickName"`
	Avatar   string `json:"avatar"`
	IsAdmin  bool   `json:"isAdmin"`
}

func NewApp() *App {
	app := &App{
		logger:       log.New(io.Discard, "", log.Ldate|log.Ltime|log.Lmicroseconds),
		trayStatusCh: make(chan string, 1),
		trayUserCh:   make(chan AuthUser, 1),
		mounts:       newSpaceMounts(),
		assetMux:     http.NewServeMux(),
		pluginReady:  make(chan struct{}),
	}
	// 全局搜索表面页（go:embed 自包含 HTML，面板窗口经 loopback 加载）
	app.assetMux.Handle("GET /search", http.HandlerFunc(app.serveSearchPage))
	return app
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	if file, path, err := logging.Open("desktop.log"); err == nil {
		a.logFile = file
		a.logger = log.New(file, "", log.Ldate|log.Ltime|log.Lmicroseconds)
		a.logger.Printf("desktop starting; log=%s", path)
	} else {
		runtime.LogError(ctx, "open runtime log: "+err.Error())
	}

	a.configPath = config.DefaultConfigPath()
	value, err := config.Load(a.configPath)
	if err != nil {
		a.reportError("load config", err)
		runtime.LogError(ctx, err.Error())
		return
	}
	a.config = value
	baseURL := "http://" + net.JoinHostPort(value.APIHost, strconv.Itoa(value.APIPort))
	options := desktop.ProcessOptions{
		BaseURL: baseURL, ConfigPath: a.configPath, Token: value.APIToken, DeviceID: value.DeviceID,
		LogPath: logging.Path("core-process.log"),
	}
	a.processOptions = options

	if err := desktop.EnsureCore(ctx, options); err != nil {
		a.reportError("ensure Core", err)
		runtime.LogError(ctx, err.Error())
		return
	}
	a.clientLock.Lock()
	a.client = desktop.NewClient(baseURL, value.APIToken)
	a.clientLock.Unlock()
	a.logger.Printf("connected to Core at %s", baseURL)

	// 恢复上次登录的账号会话（令牌落盘、只存用户级 AppData）。失败/过期时静默回到登录页。
	a.restoreAccountSession()

	go a.watchdog()
	go a.eventStream()
	// 静默检查更新（24h 节流，发现新版本发 update:available 事件）
	go a.autoUpdateCheck()
	// 插件系统 + 剪切板监听（失败不阻断主程序，见 appplugin.go）
	a.initPluginSystem()

	// Register EasyShare entries in Windows Explorer "此电脑" (This PC).
	a.registerNamespace()
}

// FilesDroppedEvent 是前端拖放文件后 Go 端返回的分类结果。
// Files 为普通文件路径，Dirs 为文件夹路径（发送时自动打包为 zip）。
type FilesDroppedEvent struct {
	Files []string `json:"files"`
	Dirs  []string `json:"dirs"`
}

// ProcessDroppedFiles 对前端拖入的路径做 os.Stat 分类：文件归 Files，文件夹归 Dirs。
// 两者均可发送（文件夹由 Core 自动打包为 zip 传输）。
func (a *App) ProcessDroppedFiles(paths []string) FilesDroppedEvent {
	files := make([]string, 0, len(paths))
	dirs := make([]string, 0)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			a.reportError("file drop stat", err)
			continue
		}
		if info.IsDir() {
			dirs = append(dirs, path)
			continue
		}
		files = append(files, path)
	}
	a.logger.Printf("file drop: %d file(s), %d dir(s)", len(files), len(dirs))
	return FilesDroppedEvent{Files: files, Dirs: dirs}
}

func (a *App) Shutdown(_ context.Context) {
	a.logger.Printf("desktop window closing")
	if a.clipboardService != nil {
		a.clipboardService.Stop()
	}
	if a.logFile != nil {
		_ = a.logFile.Close()
		a.logFile = nil
	}
}

func (a *App) reportError(operation string, err error) {
	if err == nil {
		return
	}
	message := operation + ": " + err.Error()
	now := time.Now()
	a.errorLock.Lock()
	if message == a.lastError && now.Sub(a.lastAt) < 5*time.Second {
		a.errorLock.Unlock()
		return
	}
	a.lastError, a.lastAt = message, now
	a.errorLock.Unlock()
	a.logger.Printf("ERROR %s", message)
}

func (a *App) coreClient() (*desktop.Client, error) {
	a.clientLock.RLock()
	defer a.clientLock.RUnlock()
	if a.client == nil {
		return nil, fmt.Errorf("core unavailable")
	}
	return a.client, nil
}

func (a *App) GetSnapshot() (desktop.Snapshot, error) {
	client, err := a.coreClient()
	if err != nil {
		a.reportError("get snapshot", err)
		return desktop.Snapshot{}, err
	}
	result, err := client.Snapshot(a.ctx)
	a.reportError("get snapshot", err)
	// 个人云盘归控制面管，Core 无从判断可用性，故由桌面端按会话状态覆盖。
	result.Status.CloudEnabled = a.cloudAvailable()
	return result, err
}

// cloudAvailable 判断个人云盘当前是否可用：配置了控制面地址且已登录。
func (a *App) cloudAvailable() bool {
	if strings.TrimSpace(a.config.PlatformBaseURL) == "" {
		return false
	}
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	return a.accountSession != nil && a.accountSession.Token != ""
}

func (a *App) SendFile(peerID, path string) error {
	client, err := a.coreClient()
	if err == nil {
		err = client.Send(a.ctx, peerID, path, "")
	}
	a.reportError("send file", err)
	return err
}

// SendBatch 批量发送多个文件到同一设备，共享一个 batchID 用于前端分组展示。
func (a *App) SendBatch(peerID string, paths []string) error {
	client, err := a.coreClient()
	if err != nil {
		a.reportError("send batch", err)
		return err
	}
	batchID := fmt.Sprintf("batch-%d", time.Now().UnixMilli())
	for _, path := range paths {
		if sendErr := client.Send(a.ctx, peerID, path, batchID); sendErr != nil {
			a.reportError("send batch file", sendErr)
			return sendErr
		}
	}
	return nil
}
func (a *App) AcceptTransfer(id string) error {
	client, err := a.coreClient()
	if err == nil {
		err = client.Accept(a.ctx, id)
	}
	a.reportError("accept transfer", err)
	return err
}
func (a *App) AcceptTransferAs(id string) error {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择文件保存位置", DefaultDirectory: a.config.ReceiveDir})
	if err != nil || path == "" {
		return err
	}
	client, clientErr := a.coreClient()
	if clientErr != nil {
		a.reportError("accept transfer as", clientErr)
		return clientErr
	}
	err = client.AcceptTo(a.ctx, id, path)
	a.reportError("accept transfer as", err)
	return err
}
func (a *App) RejectTransfer(id string) error {
	client, err := a.coreClient()
	if err == nil {
		err = client.Reject(a.ctx, id)
	}
	a.reportError("reject transfer", err)
	return err
}
func (a *App) driveAction(operation, path string) error {
	client, err := a.coreClient()
	if err == nil {
		err = client.Action(a.ctx, path)
	}
	a.reportError(operation, err)
	return err
}
func (a *App) StartDrive() error { return a.driveAction("start WebDAV", "/api/drive/start") }
func (a *App) StopDrive() error  { return a.driveAction("stop WebDAV", "/api/drive/stop") }
func (a *App) ShutdownAll() error {
	client, err := a.coreClient()
	if err != nil {
		return nil
	}
	if err := client.Action(a.ctx, "/api/shutdown"); err != nil {
		a.reportError("shutdown all", err)
		return err
	}
	a.clientLock.Lock()
	if a.client == client {
		a.client = nil
	}
	a.clientLock.Unlock()
	a.quitLock.Lock()
	a.quitting = true
	a.quitLock.Unlock()
	a.logger.Printf("Core shutdown accepted; quitting desktop")
	return nil
}

// ReportFrontendError records Vue and browser errors that would otherwise only
// appear in the hidden WebView developer console.
func (a *App) ReportFrontendError(message, stack string) {
	const maxLength = 16 * 1024
	value := strings.TrimSpace(message)
	if stack = strings.TrimSpace(stack); stack != "" {
		value += "\n" + stack
	}
	if len(value) > maxLength {
		value = value[:maxLength] + "…"
	}
	a.reportError("frontend", fmt.Errorf("%s", value))
}

func (a *App) GetLogDirectory() string { return logging.Directory() }

// --- 账号控制面（RuoYi）：P1 登录 + 当前用户 ---

// Login 用用户名密码向账号控制面登录，成功后把会话（含 JWT）保存在桌面端，
// 返回可展示的用户信息（不含 token）。见 docs/adr/0007。
func (a *App) Login(username, password string) (AuthUser, error) {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return AuthUser{}, fmt.Errorf("未配置账号服务地址")
	}
	client := account.New(base)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := client.Login(ctx, username, password)
	if err != nil {
		a.logger.Printf("login failed for %q: %v", username, err)
		return AuthUser{}, err
	}
	a.accountMu.Lock()
	a.accountSession = session
	a.accountMu.Unlock()
	a.saveAccountSession(session)
	a.logger.Printf("user logged in: %s", session.User.UserName)
	a.publishTrayUser()
	// 按该账号实际拥有的空间挂「此电脑」条目。异步：挂载要回控制面读空间，
	// 不该把登录这一步拖慢，登录成功与否也不取决于它。
	go a.refreshSpaceMounts()
	return a.CurrentUser(), nil
}

// Logout 清除本地会话，并尽力通知控制面登出。
func (a *App) Logout() {
	a.accountMu.Lock()
	session := a.accountSession
	a.accountMu.Unlock()
	a.clearAccountSession()
	// 先卸盘再通知控制面：会话已清空，挂着的盘此刻已经不可用，留着只会让用户点进去报错
	a.unmountAllSpaces()
	if session == nil {
		return
	}
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := account.New(base).Logout(ctx, session.Token); err != nil {
			a.logger.Printf("logout notify failed: %v", err)
		}
	}
	a.publishTrayUser()
}

// accountSessionPath 返回账号会话落盘位置：与 config.json 同目录，按用户隔离。
func (a *App) accountSessionPath() string {
	if strings.TrimSpace(a.configPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.configPath), "account-session.json")
}

func (a *App) saveAccountSession(session *account.Session) {
	path := a.accountSessionPath()
	if path == "" {
		return
	}
	if err := account.SaveSession(path, session); err != nil {
		a.logger.Printf("persist account session: %v", err)
	}
}

func (a *App) clearAccountSession() {
	a.accountMu.Lock()
	a.accountSession = nil
	a.accountMu.Unlock()
	path := a.accountSessionPath()
	if path == "" {
		return
	}
	if err := account.RemoveSession(path); err != nil {
		a.logger.Printf("remove account session: %v", err)
	}
}

// restoreAccountSession 启动时恢复落盘会话，再异步向控制面校验：
// 网络不通时保留本地登录态供离线使用，只有明确 401 才清空并通知前端回登录页。
func (a *App) restoreAccountSession() {
	path := a.accountSessionPath()
	if path == "" {
		return
	}
	session, err := account.LoadSession(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			a.logger.Printf("load account session: %v", err)
			_ = account.RemoveSession(path)
		}
		return
	}
	if session.Expired(time.Now()) {
		a.logger.Printf("stored account session expired at %s", session.ExpiresAt.Format(time.RFC3339))
		_ = account.RemoveSession(path)
		return
	}
	a.accountMu.Lock()
	a.accountSession = session
	a.accountMu.Unlock()
	a.logger.Printf("restored account session for %s", session.User.UserName)
	a.publishTrayUser()
	go a.refreshSpaceMounts()
	go a.validateAccountSession()
}

func (a *App) validateAccountSession() {
	a.accountMu.RLock()
	session := a.accountSession
	a.accountMu.RUnlock()
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if session == nil || session.Token == "" || base == "" {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	fresh, err := account.New(base).SessionInfo(ctx, session.Token)
	if err == nil {
		a.accountMu.Lock()
		if a.accountSession != nil && a.accountSession.Token == session.Token {
			a.accountSession.User = fresh.User
			a.accountSession.Roles = fresh.Roles
			a.accountSession.Permissions = fresh.Permissions
		}
		current := a.accountSession
		a.accountMu.Unlock()
		if current != nil {
			a.saveAccountSession(current)
		}
		a.publishTrayUser()
		return
	}
	if errors.Is(err, account.ErrUnauthorized) {
		a.logger.Printf("restored account session rejected: %v", err)
		a.clearAccountSession()
		a.unmountAllSpaces()
		a.publishTrayUser()
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "auth:expired")
		}
		return
	}
	// 控制面不可达时保留本地会话：离线仍可使用局域网传输与本地设置。
	a.logger.Printf("validate restored account session (keep offline): %v", err)
}

// ServiceEndpoints 是登录后下发的服务拓扑：客户端只预知控制面一个地址，
// 其余服务地址从这里拿（租户服务发现）。registry=管理员在控制面登记；
// derived=未登记时按同主机默认端口推导（控制面 :8090 → 知识服务 :8000）。
type ServiceEndpoints struct {
	KnowledgeURL       string `json:"knowledgeUrl"`
	KnowledgeURLSource string `json:"knowledgeUrlSource"`
	PlatformBaseURL    string `json:"platformBaseUrl"`
}

// ServiceEndpoints 给「知识」页等提供登录后的服务地址。控制面拉取失败不阻塞：
// 回退推导值，员工照样能用（登记只是让同机推导之外的同义部署也能工作）。
func (a *App) ServiceEndpoints() ServiceEndpoints {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	endpoints := ServiceEndpoints{
		KnowledgeURL:       account.DeriveKnowledgeURL(base),
		KnowledgeURLSource: "derived",
		PlatformBaseURL:    base,
	}
	a.accountMu.Lock()
	session := a.accountSession
	a.accountMu.Unlock()
	if session == nil {
		return endpoints
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if cfg, err := account.New(base).ServiceConfig(ctx, session.Token); err == nil && cfg != nil && cfg.KnowledgeURL != "" {
		endpoints.KnowledgeURL = cfg.KnowledgeURL
		endpoints.KnowledgeURLSource = "registry"
	}
	return endpoints
}

// SaveServiceConfig 管理员登记知识服务地址（空串=清除）。权限裁决在控制面。
func (a *App) SaveServiceConfig(knowledgeURL string) error {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return fmt.Errorf("未配置账号服务地址")
	}
	knowledgeURL = strings.TrimSpace(knowledgeURL)
	// 前置校验给管理员友好文案；控制面仍会校验（两侧双保险），此处拦住最常见的笔误
	if knowledgeURL != "" && !strings.HasPrefix(knowledgeURL, "http://") && !strings.HasPrefix(knowledgeURL, "https://") {
		return fmt.Errorf("地址必须以 http:// 或 https:// 开头，例如 http://192.168.1.10:8000")
	}
	a.accountMu.Lock()
	session := a.accountSession
	a.accountMu.Unlock()
	if session == nil {
		return fmt.Errorf("请先登录")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := account.New(base).SetServiceConfig(ctx, session.Token, knowledgeURL); err != nil {
		a.reportError("save service config", err)
		return err
	}
	return nil
}

// AdminAiConfigGet 管理页读取 AI 能力配置（超管；密钥掩码回显）。权限裁决在控制面。
func (a *App) AdminAiConfigGet() (*account.AiConfig, error) {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return nil, fmt.Errorf("未配置账号服务地址")
	}
	a.accountMu.Lock()
	session := a.accountSession
	a.accountMu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("请先登录")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	vo, err := account.New(base).AiConfig(ctx, session.Token)
	if err != nil {
		a.reportError("get ai config", err)
		return nil, err
	}
	return vo, nil
}

// AdminAiConfigSave 保存 AI 能力配置；控制面负责校验并推送知识服务热生效，
// 推送结果在返回的 Pushed/PushError 里（配置已入控制面，不受推送失败影响）。
// llmApiKey 传空串表示保持已有密钥不变。
func (a *App) AdminAiConfigSave(llmBaseUrl, llmModel, llmApiKey string, asrEnabled, asrSpeakerEnabled *bool) (*account.AiConfig, error) {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return nil, fmt.Errorf("未配置账号服务地址")
	}
	a.accountMu.Lock()
	session := a.accountSession
	a.accountMu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("请先登录")
	}
	body := map[string]any{
		"llmBaseUrl":        strings.TrimSpace(llmBaseUrl),
		"llmModel":          strings.TrimSpace(llmModel),
		"llmApiKey":         strings.TrimSpace(llmApiKey),
		"asrEnabled":        asrEnabled,
		"asrSpeakerEnabled": asrSpeakerEnabled,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	vo, err := account.New(base).SetAiConfig(ctx, session.Token, body)
	if err != nil {
		a.reportError("save ai config", err)
		return nil, err
	}
	return vo, nil
}

// publishTrayUser 把当前登录态推给托盘悬浮窗（非阻塞，模式同 updateTrayStatus）。
func (a *App) publishTrayUser() {
	user := a.CurrentUser()
	select {
	case a.trayUserCh <- user:
	default:
		// 通道满则丢弃旧值再放新值，保证悬浮窗拿到最新态。
		select {
		case <-a.trayUserCh:
		default:
		}
		select {
		case a.trayUserCh <- user:
		default:
		}
	}
}

// CurrentUser 返回当前登录态（未登录时 LoggedIn=false）。
func (a *App) CurrentUser() AuthUser {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	return a.currentUserLocked()
}

// currentUserLocked 在持有 accountMu 时组装 AuthUser。
func (a *App) currentUserLocked() AuthUser {
	if a.accountSession == nil {
		return AuthUser{LoggedIn: false}
	}
	u := a.accountSession.User
	return AuthUser{
		LoggedIn: true,
		UserName: u.UserName,
		NickName: u.NickName,
		Avatar:   u.Avatar,
		IsAdmin:  a.accountSession.IsAdmin(),
	}
}

// --- 管理页（P3）：客户端内自绘的账号与空间管理 ---
//
// 管理界面是客户端自己的页面，沿用客户端色调，不跳 RuoYi 自带后台。下面的方法在把请求
// 转给控制面之前先做一道本地检查（adminSession），目的是给出中文错误、避免无谓往返；
// **真正的鉴权在控制面**——JWT 不是管理员的，接口自己会拒。

// adminSession 取当前会话，要求已登录且为管理员。返回控制面客户端与 token。
func (a *App) adminSession() (*account.Client, string, error) {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return nil, "", fmt.Errorf("未配置账号服务地址")
	}
	a.accountMu.RLock()
	session := a.accountSession
	a.accountMu.RUnlock()
	if session == nil || session.Token == "" {
		return nil, "", fmt.Errorf("请先登录")
	}
	if !session.IsAdmin() {
		return nil, "", fmt.Errorf("当前账号没有管理权限")
	}
	return account.New(base), session.Token, nil
}

// AdminListUsers 分页取账号列表。
func (a *App) AdminListUsers(pageNum, pageSize int) (*account.UserPage, error) {
	client, token, err := a.adminSession()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	page, err := client.ListUsers(ctx, token, pageNum, pageSize)
	a.reportError("admin list users", err)
	return page, err
}

// AdminCreateUser 新建账号。
func (a *App) AdminCreateUser(user account.NewUser) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.CreateUser(ctx, token, user)
	if err == nil {
		a.logger.Printf("admin created user %q", user.UserName)
	}
	a.reportError("admin create user", err)
	return err
}

// AdminSetUserStatus 启用/停用账号。
func (a *App) AdminSetUserStatus(userID string, enabled bool) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.SetUserStatus(ctx, token, userID, enabled)
	a.reportError("admin set user status", err)
	return err
}

// AdminResetPassword 重置指定账号的密码。
func (a *App) AdminResetPassword(userID, password string) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.ResetUserPassword(ctx, token, userID, password)
	if err == nil {
		a.logger.Printf("admin reset password for user %s", userID)
	}
	a.reportError("admin reset password", err)
	return err
}

// AdminDeleteUser 删除账号。
func (a *App) AdminDeleteUser(userID string) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.DeleteUser(ctx, token, userID)
	if err == nil {
		a.logger.Printf("admin deleted user %s", userID)
	}
	a.reportError("admin delete user", err)
	return err
}

// AdminRegisterEnabled 读「允许自助注册」开关。
func (a *App) AdminRegisterEnabled() (bool, error) {
	client, token, err := a.adminSession()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	enabled, err := client.RegisterEnabled(ctx, token)
	a.reportError("admin read register switch", err)
	return enabled, err
}

// AdminSetRegisterEnabled 写「允许自助注册」开关。
func (a *App) AdminSetRegisterEnabled(enabled bool) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.SetRegisterEnabled(ctx, token, enabled)
	if err == nil {
		a.logger.Printf("admin set register switch to %v", enabled)
	}
	a.reportError("admin set register switch", err)
	return err
}

// --- 空间与配额（P3）---
//
// 「一个地方砍空间」：管理页的空间区同时设定共享容量与逐账号个人配额。
// 配额的强制点在控制面签发预签名 URL 时，客户端这里只是入口。

// MySpaces 取当前登录账号可见的空间：个人空间 +（被授权时的）共享空间。
//
// 与 Admin* 系列不同，这个不要求管理员——每个账号都要能看自己的容量。
func (a *App) MySpaces() ([]account.Space, error) {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return nil, fmt.Errorf("未配置账号服务地址")
	}
	a.accountMu.RLock()
	session := a.accountSession
	a.accountMu.RUnlock()
	if session == nil || session.Token == "" {
		return nil, fmt.Errorf("请先登录")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	spaces, err := account.New(base).MySpaces(ctx, session.Token)
	a.reportError("read my spaces", err)
	return spaces, err
}

// AdminListSpaces 取全部空间与实时用量（共享在前）。
func (a *App) AdminListSpaces() ([]account.Space, error) {
	client, token, err := a.adminSession()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spaces, err := client.ListSpaces(ctx, token)
	a.reportError("admin list spaces", err)
	return spaces, err
}

// AdminCapacity 取容量总览：物理可用、已承诺、实际已用。
//
// 管理页据此判断是否超配。逐空间配额看不出这件事——那是本方法存在的理由。
func (a *App) AdminCapacity() (*account.Capacity, error) {
	client, token, err := a.adminSession()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	capacity, err := client.GetCapacity(ctx, token)
	a.reportError("admin read capacity", err)
	return capacity, err
}

// AdminSharedMember 共享空间授权主体（管理页显示用）。
type AdminSharedMember struct {
	MemberType string `json:"memberType"`
	MemberID   string `json:"memberId"`
	Permission string `json:"permission"`
	Name       string `json:"name"`
}

// AdminDept 管理页部门下拉条目。
type AdminDept struct {
	DeptID   string `json:"deptId"`
	DeptName string `json:"deptName"`
}

// AdminSharedMembers 取共享空间授权主体（账号/部门）。
func (a *App) AdminSharedMembers() ([]AdminSharedMember, error) {
	client, token, err := a.adminSession()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	members, err := client.SharedMembers(ctx, token)
	a.reportError("admin list shared members", err)
	result := make([]AdminSharedMember, 0, len(members))
	for _, member := range members {
		result = append(result, AdminSharedMember{
			MemberType: member.MemberType, MemberID: member.MemberID,
			Permission: member.Permission, Name: member.Name,
		})
	}
	return result, err
}

// AdminListDepts 部门下拉数据源（启用中未删除）。
func (a *App) AdminListDepts() ([]AdminDept, error) {
	client, token, err := a.adminSession()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	depts, err := client.ListDepts(ctx, token)
	a.reportError("admin list depts", err)
	result := make([]AdminDept, 0, len(depts))
	for _, dept := range depts {
		result = append(result, AdminDept{DeptID: dept.DeptID, DeptName: dept.DeptName})
	}
	return result, err
}

// AdminSetPersonalQuota 设定某账号的个人空间容量。0 收回、-1 不限。
func (a *App) AdminSetPersonalQuota(userID string, quotaBytes int64) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.SetPersonalQuota(ctx, token, userID, quotaBytes)
	if err == nil {
		a.logger.Printf("admin set personal quota for %s to %d bytes", userID, quotaBytes)
		// 改的可能就是自己的配额：重挂一次，让副标题的数字与盘的有无立刻跟上
		go a.refreshSpaceMounts()
	}
	a.reportError("admin set personal quota", err)
	return err
}

// AdminSetSharedQuota 设定共享空间容量。
func (a *App) AdminSetSharedQuota(quotaBytes int64) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.SetSharedQuota(ctx, token, quotaBytes)
	if err == nil {
		a.logger.Printf("admin set shared quota to %d bytes", quotaBytes)
		go a.refreshSpaceMounts()
	}
	a.reportError("admin set shared quota", err)
	return err
}

// SetFileVisibility 设置共享空间文档的可见部门（空列表=恢复全体可见；仅上传者本人可调）。
// deptIDs 是**十进制字符串**：部门 ID 与文件 ID 一样是雪花 ID，
// 超出 JS 安全整数（实测 4/4 个真实部门 ID 经 number 往返后撞成同一个值）。
// 前端已有的 Dept 契约本来就用 string（见 frontend/src/types/core.ts 的 deptId），
// 这个入口此前用 []int64 是同一件事的第二套表示法，现已统一。
func (a *App) SetFileVisibility(space, path string, deptIDs []string) error {
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.SetFileVisibilityByRef(ctx, token, space, path, deptIDs); err != nil {
		a.reportError("set file visibility", err)
		return err
	}
	a.logger.Printf("file visibility updated: %s (%s) -> %v", path, space, deptIDs)
	return nil
}

// AdminKnowledgeStats 管理员汇总页：知识服务聚合统计（查询/盲区/生成质量/文档规模）。
func (a *App) AdminKnowledgeStats(days int) (knowledge.KnowledgeStats, error) {
	core, err := a.coreClient()
	if err != nil {
		return knowledge.KnowledgeStats{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stats, err := core.KnowledgeStats(ctx, days)
	a.reportError("admin knowledge stats", err)
	return stats, err
}

// AdminGrantShared 授予或撤销某主体（账号/部门）对共享空间的权限。permission 传空即撤销。
func (a *App) AdminGrantShared(memberType, memberID, permission string) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.GrantShared(ctx, token, memberType, memberID, permission)
	if err == nil {
		a.logger.Printf("admin set shared permission for %s(%s) to %q", memberType, memberID, permission)
		// 撤销自己的授权时，共享盘要从「此电脑」里消失
		go a.refreshSpaceMounts()
	}
	a.reportError("admin grant shared", err)
	return err
}

// OpenAdminConsole 在系统默认浏览器里打开 RuoYi 自带后台（plus-ui）。
//
// 这是**次要出口**，不是管理页本身：客户端的管理页自绘、直接调 REST。此处只覆盖本产品
// 不复刻的后台专属运维动作（菜单/字典/定时任务等）。
func (a *App) OpenAdminConsole() error {
	target := strings.TrimSpace(a.config.AdminConsoleURL)
	if target == "" {
		err := fmt.Errorf("未配置管理后台地址")
		a.reportError("open admin console", err)
		return err
	}
	// 只放行 http/https：地址来自 config.json，损坏或被改成别的 scheme 时不该拿去唤起程序。
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		wrapped := fmt.Errorf("管理后台地址无效（需 http/https）：%s", target)
		a.reportError("open admin console", wrapped)
		return wrapped
	}
	runtime.BrowserOpenURL(a.ctx, target)
	return nil
}

// OpenFile 用系统默认应用打开指定文件（Finder 双击 / 资源管理器双击）。
func (a *App) OpenFile(path string) error {
	if path == "" {
		return nil
	}
	err := fsutil.OpenFile(path)
	a.reportError("open file", err)
	return err
}

// ShowInFolder 在文件管理器中定位并选中指定文件（下载完成后的「打开所在文件夹」）。
func (a *App) ShowInFolder(path string) error {
	if path == "" {
		return nil
	}
	err := fsutil.ShowInFolder(path)
	a.reportError("show in folder", err)
	return err
}

// OpenReceiveFolder 在文件管理器中打开当前设置的文件接收目录。
func (a *App) OpenReceiveFolder() error {
	return a.openReceiveFolder(fsutil.OpenFolder)
}

// openReceiveFolder 每次从持久化配置读取接收目录，避免使用桌面端启动时的旧配置快照。
func (a *App) openReceiveFolder(openFolder func(string) error) error {
	value, err := config.Load(a.configPath)
	if err != nil {
		err = fmt.Errorf("读取接收目录配置失败：%w", err)
		a.reportError("open receive folder", err)
		return err
	}

	dir := strings.TrimSpace(value.ReceiveDir)
	if dir == "" {
		err = fmt.Errorf("接收目录未配置")
		a.reportError("open receive folder", err)
		return err
	}

	err = openFolder(dir)
	a.reportError("open receive folder", err)
	return err
}

func (a *App) ClearHistory() error {
	client, err := a.coreClient()
	if err == nil {
		err = client.ClearTasks(a.ctx)
	}
	a.reportError("clear history", err)
	return err
}

func (a *App) DeleteTask(id string) error {
	client, err := a.coreClient()
	if err == nil {
		err = client.DeleteTask(a.ctx, id)
	}
	a.reportError("delete task", err)
	return err
}

func (a *App) SelectShareDirectory() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 WebDAV 共享目录", DefaultDirectory: a.config.WebDAVRoot})
	a.reportError("select share directory", err)
	return path, err
}

func (a *App) SelectReceiveDirectory() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择文件接收目录", DefaultDirectory: a.config.ReceiveDir})
	a.reportError("select receive directory", err)
	return path, err
}

// Settings is the user-facing subset of configuration exposed to the frontend.
type Settings struct {
	DeviceName string `json:"deviceName"`
	ReceiveDir string `json:"receiveDir"`
	WebDAVRoot string `json:"webdavRoot"`
}

func (a *App) GetSettings() Settings {
	return Settings{
		DeviceName: a.config.DeviceName,
		ReceiveDir: a.config.ReceiveDir,
		WebDAVRoot: a.config.WebDAVRoot,
	}
}

func (a *App) SaveSettings(deviceName, receiveDir, webdavRoot string) error {
	deviceName = strings.TrimSpace(deviceName)
	if deviceName == "" {
		return fmt.Errorf("设备名称不能为空")
	}
	if receiveDir == "" {
		return fmt.Errorf("接收目录不能为空")
	}
	if webdavRoot == "" {
		return fmt.Errorf("共享目录不能为空")
	}

	a.config.DeviceName = deviceName
	a.config.ReceiveDir = receiveDir
	a.config.WebDAVRoot = webdavRoot

	if err := config.Save(a.configPath, a.config); err != nil {
		a.reportError("save settings", err)
		return fmt.Errorf("保存配置失败：%w", err)
	}

	// Attempt to notify Core to reload configuration; non-fatal if unavailable.
	if client, err := a.coreClient(); err == nil {
		_ = client.Action(a.ctx, "/api/config/reload")
	}
	a.logger.Printf("settings saved: device=%s receive=%s webdav=%s", deviceName, receiveDir, webdavRoot)
	return nil
}

func (a *App) SelectFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要发送的文件"})
	a.reportError("select file", err)
	return path, err
}

func (a *App) SelectFiles() ([]string, error) {
	paths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要发送的文件（可多选）"})
	a.reportError("select files", err)
	return paths, err
}

// --- Cloud drive ---
//
// 个人云盘的存储访问一律经账号控制面换短期预签名 URL，再直传/直取 RustFS：
// 桌面端不持存储凭据（ADR-0007 不变量 1，修 KI-2），对象键的用户命名空间由
// 控制面强制（不变量 2，修 KI-3）。传输任务与进度仍记在 Core（不变量 3）。

// driveClient 返回云盘客户端与当前登录用户的 JWT。未登录或未配置控制面时报错。
func (a *App) driveClient() (*drive.Client, string, error) {
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		return nil, "", fmt.Errorf("未配置账号服务地址")
	}
	a.accountMu.RLock()
	session := a.accountSession
	a.accountMu.RUnlock()
	if session == nil || session.Token == "" {
		return nil, "", fmt.Errorf("请先登录后再使用网盘")
	}
	client := drive.New(base)
	// 大文件会话持久化目录：与 config.json 同级（数据根），进程重启后同指纹
	// 文件自动续传（断点续传的"断点"落在本机，服务端会话状态由控制面记账）
	client.Sessions = drive.NewSessionStore(filepath.Join(filepath.Dir(a.configPath), "upload-sessions"))
	return client, session.Token, nil
}

// driveObjectToFile 把控制面返回的对象转成前端已在用的 cloud.File。
// 控制面不返回 contentType（S3 列举本就没有），按扩展名推断。
func driveObjectToFile(object drive.Object) cloud.File {
	contentType := cloud.ContentTypeForKey(object.Path)
	// 目录标记对象的 Path 带尾斜杠（"photos/"）：Base 前先去掉，
	// 否则不同平台上对尾斜杠的处理不一致，Name 可能带上斜杠
	name := filepath.Base(strings.TrimRight(object.Path, "/"))
	return cloud.File{
		Key:          object.Path,
		Name:         name,
		Size:         object.Size,
		ContentType:  contentType,
		LastModified: object.LastModified,
		PreviewKind:  cloud.DetectPreviewKind(contentType, object.Path),
		FileId:       object.FileId.Int64(),
		CreatedAt:    object.CreatedAt,
		UploadByName: object.UploadByName,
		VersionId:    object.VersionId.Int64(),
	}
}

func (a *App) CloudList() ([]cloud.File, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return nil, err
	}
	// 带目录列举：网盘页要把空文件夹也显示出来（目录标记对象）。
	// 其他出口（挂载盘 / WebDAV / 快搜）继续走不带目录的 Objects，行为不变。
	objects, err := client.ObjectsWithDirs(a.ctx, token, drive.SpacePersonal)
	if err != nil {
		a.reportError("cloud list", err)
		return nil, err
	}
	files := make([]cloud.File, 0, len(objects))
	for _, object := range objects {
		files = append(files, driveObjectToFile(object))
	}
	return files, nil
}

// CloudPreview 返回文件的安全预览能力。
//
// 图片/PDF 交给 WebView2 直接加载控制面签发的预签名 URL；文本内容由本进程限量读入后内联。
// SVG 等可含主动内容的类型在 DetectPreviewKind 里已归为 unsupported，不会拿到 URL。
func (a *App) CloudPreview(key string) (cloud.Preview, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return cloud.Preview{}, err
	}
	contentType := cloud.ContentTypeForKey(key)
	preview := cloud.Preview{
		Key:         key,
		Name:        filepath.Base(key),
		ContentType: contentType,
		Kind:        cloud.DetectPreviewKind(contentType, key),
	}

	switch preview.Kind {
	case cloud.PreviewImage, cloud.PreviewPDF:
		url, presignErr := client.PresignGet(a.ctx, token, drive.SpacePersonal, key)
		if presignErr != nil {
			a.reportError("cloud preview", presignErr)
			return cloud.Preview{}, presignErr
		}
		preview.ContentURL = url
		if size, sizeErr := a.driveObjectSize(client, token, key); sizeErr == nil {
			preview.Size = size
		}
	case cloud.PreviewText:
		body, size, openErr := client.Open(a.ctx, token, drive.SpacePersonal, key)
		if openErr != nil {
			a.reportError("cloud preview", openErr)
			return cloud.Preview{}, openErr
		}
		defer body.Close()
		if size > 0 {
			preview.Size = size
		}
		if fillErr := cloud.FillTextPreview(&preview, body); fillErr != nil {
			a.reportError("cloud preview", fillErr)
			return cloud.Preview{}, fillErr
		}
	default:
		if size, sizeErr := a.driveObjectSize(client, token, key); sizeErr == nil {
			preview.Size = size
		}
	}
	return preview, nil
}

// driveObjectSize 从控制面列举结果里取对象大小。
// 预签名 URL 不暴露元数据，而控制面暂无单对象 stat 接口，故按列举匹配。
func (a *App) driveObjectSize(client *drive.Client, token, key string) (int64, error) {
	objects, err := client.Objects(a.ctx, token, drive.SpacePersonal)
	if err != nil {
		return 0, err
	}
	for _, object := range objects {
		if object.Path == key {
			return object.Size, nil
		}
	}
	return 0, fmt.Errorf("对象不存在：%s", key)
}

// joinObjectKey 把目标目录前缀与相对键拼成对象键（目录为空=根目录）。
// 目录规范为不带首尾斜杠（如 "photos/2024"）。
func joinObjectKey(dir, key string) string {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return key
	}
	return dir + "/" + strings.TrimLeft(key, "/")
}

// objectVersionMap 把对象列表转成 path -> versionId。
//
// CAS 的关键在于「上传方带回自己看到的版本」；这份 map 就是上传方在本次动作开始时
// 看到的世界。后面每次写入都会把它带回控制面，服务端发现别人先改过就拒绝。
func objectVersionMap(objects []drive.Object) map[string]int64 {
	versions := make(map[string]int64, len(objects))
	for _, obj := range objects {
		versions[obj.Path] = obj.VersionId.Int64()
	}
	return versions
}

// expectedVersionFor 返回上传时带回给控制面的版本指针。
//
// 语义：0 = 我认为这个路径还不存在；n>0 = 我看到的是第 n 版。
// 必须返回指针而不是值，nil 与 0 在服务端是两个语义（nil = 旧客户端不校验）。
func expectedVersionFor(versions map[string]int64, key string) *int64 {
	if v, ok := versions[key]; ok {
		return &v
	}
	zero := int64(0)
	return &zero
}

// isVersionConflict 判断是否是 CAS 拒绝。
//
// 只在**版本冲突**这一类错误上做自动改名重试；配额、权限、网络错误绝不能改名重试——
// 那样会把“没权限”变成“写到别的地方”，既掩盖根因也可能越权。
func isVersionConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "已被其他人修改") ||
		strings.Contains(msg, "已被删除") ||
		strings.Contains(msg, "版本") && strings.Contains(msg, "已过期")
}

// conflictObjectKey 生成不覆盖任何现有文件的冲突副本路径。
//
// 规则来自路线图切片 3：保双方、冲突副本、不覆盖。
// 先尝试 “原名 (冲突副本).ext”；若仍被占用则递增 “(冲突副本2)”。
func conflictObjectKey(versions map[string]int64, key string) string {
	ext := filepath.Ext(key)
	base := strings.TrimSuffix(key, ext)
	if _, exists := versions[base+" (冲突副本)"+ext]; !exists {
		return base + " (冲突副本)" + ext
	}
	for i := 2; ; i++ {
		candidate := base + " (冲突副本" + strconv.Itoa(i) + ")" + ext
		if _, exists := versions[candidate]; !exists {
			return candidate
		}
	}
}

// 冲突策略（路线图切片 3）的固定规则，写死、不弹窗问用户：
//
//	情形 1  远端新建 vs 本地已有、内容相同   → 认作同一文件（哈希去重），不算冲突
//	情形 2  远端新建 vs 本地已有、内容不同   → 保双方，本地加标记改名（冲突副本）
//	情形 3  本地改 + 远端改                  → 冲突副本，两份都留
//	情形 4  移动目标已存在                   → 冲突副本，不覆盖
//
// 情形 3/4 已由 CAS 与 spacedav.Rename 各自落地；本文件负责情形 1/2 的判定。
//
// 为什么情形 1 必须单独判：没有它时，用户把**同一份内容**重新上传到同一路径，
// 若中间别人动过这个路径，CAS 会拒绝、随后落一个「冲突副本」——
// 于是云盘里出现两份**逐字节相同**的文件，用户还要自己判断删哪个。
// 判据是内容哈希（md5_reliable='client' 的整文件读算，见切片 6），
// 哈希不可用（旧客户端上传/存量行）时退回「按冲突处理」——宁多一份副本，不误合。

// sameContentOnRemote 判断远端该路径的现有内容是否与本地文件相同。
//
// 只在**我们即将因版本冲突去建副本**时调用：那一刻远端内容已被别人改过，
// 若改出来恰好和我们这次要写的字节一致，就没必要再留一份副本。
//
// 返回 true 表示「同一内容，忽略这次冲突」。
// 任何存疑情形（远端无哈希、本地读文件失败）都返回 false —— 走冲突副本，
// 代价只是一份多余副本，而误判为「相同」会**丢掉用户这次上传的内容**。
func sameContentOnRemote(objects []drive.Object, objectKey, localPath string) bool {
	var remoteHash string
	for _, obj := range objects {
		if obj.Path == objectKey {
			remoteHash = obj.ContentMd5
			break
		}
	}
	if remoteHash == "" {
		return false // 远端未记录哈希（存量/旧客户端上传）：无从比对，按冲突处理
	}
	localHash, err := fileContentMD5(localPath)
	if err != nil {
		return false
	}
	return strings.EqualFold(localHash, remoteHash)
}

// failTask 把一个任务标记为失败，并写入错误三件套（路线图切片 7）。
//
// 三个字段缺一个，用户看到的就只是「失败」：
//   - error       人读文本（中文，直接展示）
//   - errorCode   业务码（稳定字符串，工单/埋点/日志检索用）
//   - systemCode  系统码（HTTP 状态 / OS 错误号；没有就空）
//
// 分类走 internal/driveerr.Classify（只从已有信息推断，不改变行为）。
func (a *App) failTask(core *desktop.Client, taskID string, err error) {
	triple := driveerr.Classify(err)
	// 调用点显式标注的分类优先：本地写盘失败在中文文案里未必带
	// 英文 errno / "permission denied"，不让它掉进 UNKNOWN。
	if code := driveerr.CodeOf(err); code != "" {
		triple.Code = code
	}
	message, code, system := triple.Format()
	patch := map[string]any{
		"status": "failed",
		"error":  message,
	}
	if code != "" {
		patch["errorCode"] = code
	}
	if system != "" {
		patch["systemCode"] = system
	}
	_ = core.PatchTask(a.ctx, taskID, patch)
	a.logger.Printf("task %s failed: %s", taskID, triple.Error())
}

// fileContentMD5 整文件读算十六进制 MD5（与 internal/drive 上传路径同口径）。
func fileContentMD5(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CloudUpload 选择单个文件上传到网盘（targetDir 非空时落到该目录下）。
func (a *App) CloudUpload(targetDir string) (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要上传到网盘的文件"})
	if err != nil || path == "" {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	core, driveClient, token, err := a.uploadClients()
	if err != nil {
		return "", err
	}
	objectKey := joinObjectKey(targetDir, filepath.Base(path))
	go a.uploadSingleFile(core, driveClient, token, drive.SpacePersonal, path, objectKey)
	return filepath.Base(path), nil
}

// CloudUploadPaths 将指定路径（文件或文件夹）上传到当前目标空间，供拖拽调用。
// 文件以文件名为键，文件夹保留目录结构（"文件夹名/相对路径"）。
//
// 目标空间取自悬浮窗切换器的选择（默认个人空间）——这就是那个滑动开关的落点：
// 切到「共享」后，拖进去的文件进共享空间而不是个人空间。
func (a *App) CloudUploadPaths(paths []string) error {
	core, driveClient, token, err := a.uploadClients()
	if err != nil {
		return err
	}
	space := a.targetSpace()
	go func() {
		for _, path := range paths {
			info, statErr := os.Stat(path)
			if statErr != nil {
				continue
			}
			if info.IsDir() {
				a.uploadDir(core, driveClient, token, space, path, "")
			} else {
				a.uploadSingleFile(core, driveClient, token, space, path, filepath.Base(path))
			}
		}
	}()
	return nil
}

// uploadClients 一次取齐上传所需的两个客户端：Core 记任务进度，控制面客户端传字节。
func (a *App) uploadClients() (*desktop.Client, *drive.Client, string, error) {
	core, err := a.coreClient()
	if err != nil {
		return nil, nil, "", err
	}
	driveClient, token, err := a.driveClient()
	if err != nil {
		return nil, nil, "", err
	}
	return core, driveClient, token, nil
}

// uploadSingleFile 上传单个文件：字节经控制面预签名直传 RustFS，
// 进度仍写入 Core 的统一任务存储（Core 保留传输任务职责，ADR-0007 不变量 3）。
func (a *App) uploadSingleFile(core *desktop.Client, driveClient *drive.Client, token, space, filePath, objectKey string) {
	fileName := filepath.Base(filePath)
	info, err := os.Stat(filePath)
	if err != nil {
		return
	}
	fileSize := info.Size()
	created, createErr := core.CreateTask(a.ctx, map[string]any{
		"kind": "cloud_upload", "fileName": fileName, "direction": "send",
		"peer": "网盘", "totalBytes": fileSize, "status": "running",
	})
	if createErr != nil {
		a.reportError("create cloud upload task", createErr)
		return
	}
	// 上传前先看一眼当前版本：这是 CAS 的“我看到的世界”。
	objects, listErr := driveClient.Objects(a.ctx, token, space)
	if listErr != nil {
		a.reportError("cloud upload list", listErr)
		a.failTask(core, created.ID, listErr)
		return
	}
	versions := objectVersionMap(objects)

	start := time.Now()
	var lastPatch time.Time
	progress := func(sent, total int64) {
		now := time.Now()
		if now.Sub(lastPatch) < 200*time.Millisecond && sent < total {
			return
		}
		lastPatch = now
		elapsed := now.Sub(start).Seconds()
		speed := 0.0
		if elapsed > 0 {
			speed = float64(sent) / elapsed
		}
		_ = core.PatchTask(a.ctx, created.ID, map[string]any{
			"transferredBytes": sent, "speed": speed, "status": "running",
		})
	}
	uploadErr := driveClient.UploadFileIfVersion(a.ctx, token, space, objectKey, filePath,
		expectedVersionFor(versions, objectKey), progress)

	// 版本冲突：不覆盖对方内容，自动落到冲突副本（切片 3 的固定规则）。
	if isVersionConflict(uploadErr) {
		fresh, freshErr := driveClient.Objects(a.ctx, token, space)
		if freshErr != nil {
			a.reportError("cloud upload refresh", freshErr)
			a.failTask(core, created.ID, uploadErr)
			return
		}
		// 情形 1（哈希去重）：远端那份与我这次要写的内容逐字节相同 →
		// 认作同一文件，直接算成功，不再留一份多余副本。
		if sameContentOnRemote(fresh, objectKey, filePath) {
			a.logger.Printf("cloud upload conflict resolved by content hash: %s（内容相同，视为同一文件）", objectKey)
			_ = core.PatchTask(a.ctx, created.ID, map[string]any{
				"transferredBytes": fileSize, "speed": 0, "status": "completed",
			})
			return
		}
		// 情形 2（内容不同）：保双方，本地落到冲突副本
		versions = objectVersionMap(fresh)
		conflictKey := conflictObjectKey(versions, objectKey)
		zero := int64(0) // 冲突副本是新建，不允许覆盖任何人
		a.logger.Printf("cloud upload conflict: %s -> %s", objectKey, conflictKey)
		uploadErr = driveClient.UploadFileIfVersion(a.ctx, token, space, conflictKey, filePath, &zero, progress)
	}

	if uploadErr != nil {
		a.reportError("cloud upload", uploadErr)
		a.failTask(core, created.ID, uploadErr)
		return
	}
	_ = core.PatchTask(a.ctx, created.ID, map[string]any{
		"transferredBytes": fileSize, "speed": 0, "status": "completed",
	})
}

// uploadDir 遍历目录逐文件上传，保留目录结构（baseDir 为网盘内目标目录前缀）。
// 进度写入 Core 统一任务存储。
func (a *App) uploadDir(core *desktop.Client, driveClient *drive.Client, token, space, dir, baseDir string) {
	folderName := filepath.Base(dir)
	var files []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if len(files) == 0 {
		return
	}
	// 文件夹级任务：totalBytes 为文件数，transferredBytes 为已完成文件数
	created, createErr := core.CreateTask(a.ctx, map[string]any{
		"kind": "cloud_upload", "fileName": folderName + "/", "direction": "send",
		"peer": "网盘", "totalBytes": int64(len(files)), "status": "running",
	})
	if createErr != nil {
		a.reportError("create cloud folder upload task", createErr)
		return
	}
	// 批量上传只列一次版本，避免每个文件都打一遍控制面。
	objects, listErr := driveClient.Objects(a.ctx, token, space)
	if listErr != nil {
		a.reportError("cloud folder upload list", listErr)
		a.failTask(core, created.ID, listErr)
		return
	}
	versions := objectVersionMap(objects)

	for i, filePath := range files {
		rel, relErr := filepath.Rel(dir, filePath)
		if relErr != nil {
			continue
		}
		objectKey := joinObjectKey(baseDir, folderName+"/"+filepath.ToSlash(rel))
		uploadErr := driveClient.UploadFileIfVersion(a.ctx, token, space, objectKey, filePath,
			expectedVersionFor(versions, objectKey), nil)

		// 版本冲突：生成副本，不覆盖远端；副本名仍避开所有现有文件。
		if isVersionConflict(uploadErr) {
			fresh, freshErr := driveClient.Objects(a.ctx, token, space)
			if freshErr != nil {
				a.reportError("cloud folder upload refresh", freshErr)
				a.failTask(core, created.ID, uploadErr)
				return
			}
			// 情形 1（哈希去重）：内容相同 → 视为同一文件，跳过
			if sameContentOnRemote(fresh, objectKey, filePath) {
				a.logger.Printf("cloud folder upload conflict resolved by content hash: %s", objectKey)
				versions = objectVersionMap(fresh)
				_ = core.PatchTask(a.ctx, created.ID, map[string]any{
					"transferredBytes": int64(i + 1), "status": "running",
				})
				continue
			}
			// 情形 2（内容不同）：保双方
			versions = objectVersionMap(fresh)
			conflictKey := conflictObjectKey(versions, objectKey)
			zero := int64(0)
			a.logger.Printf("cloud folder upload conflict: %s -> %s", objectKey, conflictKey)
			uploadErr = driveClient.UploadFileIfVersion(a.ctx, token, space, conflictKey, filePath, &zero, nil)
		}

		if uploadErr != nil {
			a.reportError("cloud folder upload", uploadErr)
			a.failTask(core, created.ID, uploadErr)
			return
		}
		_ = core.PatchTask(a.ctx, created.ID, map[string]any{
			"transferredBytes": int64(i + 1), "status": "running",
		})
	}
	a.logger.Printf("cloud folder upload done: %s (%d files)", folderName, len(files))
	_ = core.PatchTask(a.ctx, created.ID, map[string]any{
		"transferredBytes": int64(len(files)), "speed": 0, "status": "completed",
	})
}

// CloudUploadFolder 选择文件夹并逐文件上传到网盘，保留目录结构。
// 对象键格式为 "文件夹名/相对路径"（如 "photos/2024/img.jpg"）。
// CloudUploadFolder 选择文件夹并逐文件上传到网盘，保留目录结构（targetDir
// 非空时整棵文件夹落到该目录下）。对象键格式为 "文件夹名/相对路径"
// （如 "photos/2024/img.jpg"）。
func (a *App) CloudUploadFolder(targetDir string) (string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要上传到网盘的文件夹"})
	if err != nil || dir == "" {
		return "", err
	}
	core, driveClient, token, err := a.uploadClients()
	if err != nil {
		return "", err
	}
	go a.uploadDir(core, driveClient, token, drive.SpacePersonal, dir, targetDir)
	return filepath.Base(dir), nil
}

// CloudMkdir 在网盘当前目录下新建文件夹。
//
// targetDir 语义与 CloudUploadPaths 一致：” 或 '/' 表示根。目录在对象存储里
// 没有实体，落地为「键以 / 结尾的 0 字节标记对象」，网盘页据此显示空文件夹；
// 建完立即可见（CloudList 走带目录的列举），点进去可继续上传。
//
// 重复创建同名文件夹幂等成功，与本地文件系统行为对齐——用户为点第二次
// 「新建文件夹」先手动删一次，是不可接受的交互。
//
// name 只接受单段名：与 CloudRename 同款校验，拒绝分隔符与 ..
// （新建不该顺手把目录建到别处去）。
func (a *App) CloudMkdir(targetDir, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("文件夹名不能为空")
	}
	if strings.ContainsAny(name, `/\`) {
		return errors.New("文件夹名不能包含路径分隔符")
	}
	if name == "." || name == ".." {
		return errors.New("文件夹名不合法")
	}
	dir := strings.Trim(strings.TrimSpace(targetDir), "/")
	if dir == ".." || strings.HasPrefix(dir, "../") || strings.Contains(dir, "/../") {
		return errors.New("目标目录不合法")
	}
	target := name
	if dir != "" {
		target = path.Join(dir, name)
	}
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	if _, err := client.Mkdir(a.ctx, token, drive.SpacePersonal, target); err != nil {
		a.reportError("cloud mkdir", err)
		return err
	}
	return nil
}

// CloudDownload 把云端文件下载到本机接收目录，进度写入 Core 统一任务存储。
//
// 不走浏览器：预签名 URL 只在本进程内取字节。WPS / 钉钉桌面端同样是
// 「默认落到下载目录 + 传输面板里给进度」，把保存动作甩给浏览器会丢失
// 进度、失败原因和完成后的打开入口——用户在应用里看不到下载发生过。
func (a *App) CloudDownload(key string) error {
	return a.startCloudDownload(key, "", "")
}

// CloudDownloadAs 弹保存框让用户另选落点；取消时不创建任务。
func (a *App) CloudDownloadAs(key string) error {
	defaultDir, err := a.downloadDirectory()
	if err != nil {
		return err
	}
	defaultName, err := safeCloudDownloadName(key)
	if err != nil {
		return err
	}
	target, dialogErr := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:            "另存云端文件",
		DefaultDirectory: defaultDir,
		DefaultFilename:  defaultName,
	})
	if dialogErr != nil {
		a.reportError("cloud download dialog", dialogErr)
		return dialogErr
	}
	if target == "" {
		return nil
	}
	return a.startCloudDownload(key, filepath.Dir(target), filepath.Base(target))
}

// startCloudDownload 打开远端流并登记任务，字节复制交给后台 goroutine。
//
// 远端流在这里同步打开：未登录、无权限、对象不存在这类错误要立刻回到
// 调用方（前端能马上提示），而不是等用户去任务面板里发现一条失败记录。
// dir/name 为空表示走默认接收目录，并在重名时自动加序号。
func (a *App) startCloudDownload(key, dir, name string) error {
	core, err := a.coreClient()
	if err != nil {
		return err
	}
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	body, size, err := client.Open(a.ctx, token, drive.SpacePersonal, key)
	if err != nil {
		a.reportError("cloud download", err)
		return err
	}
	started := false
	defer func() {
		if !started {
			_ = body.Close()
		}
	}()

	explicitTarget := name != ""
	if dir == "" {
		dir, err = a.downloadDirectory()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		wrapped := driveerr.Wrap(fmt.Errorf("创建下载目录失败：%w", err), driveerr.CodeLocal)
		a.reportError("cloud download mkdir", wrapped)
		return wrapped
	}
	if name == "" {
		name, err = safeCloudDownloadName(key)
		if err != nil {
			return err
		}
	}
	target := filepath.Join(dir, name)
	if !explicitTarget {
		// 默认下载与同目录重名时不让用户覆盖（WPS / 钉钉同款
		// 「文件 (1).ext」规则）；另存为的落点由保存框确认。
		target = uniqueDownloadPath(target)
	}
	if size < 0 {
		size = 0
	}
	// 先写临时文件再改名：失败时不会留下一个「看起来下载完了」的半截文件。
	temp, err := os.CreateTemp(dir, ".easyshare-download-*.part")
	if err != nil {
		wrapped := driveerr.Wrap(fmt.Errorf("创建本地文件失败：%w", err), driveerr.CodeLocal)
		a.reportError("cloud download create temp", wrapped)
		return wrapped
	}
	created, createErr := core.CreateTask(a.ctx, map[string]any{
		"kind": "cloud_download", "fileName": filepath.Base(target), "direction": "receive",
		"peer": "网盘", "totalBytes": size, "status": "running", "localPath": target,
	})
	if createErr != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		a.reportError("create cloud download task", createErr)
		return createErr
	}
	started = true
	go a.copyCloudDownload(core, created.ID, body, temp, target, size)
	return nil
}

// copyCloudDownload 把远端流写进临时文件，200ms 节流上报进度，完成后原子落位。
func (a *App) copyCloudDownload(core *desktop.Client, taskID string, body io.ReadCloser, file *os.File, target string, total int64) {
	defer body.Close()
	tempPath := file.Name()
	start := time.Now()
	var lastPatch time.Time
	written := int64(0)

	fail := func(cause error) {
		_ = file.Close()
		_ = os.Remove(tempPath)
		a.reportError("cloud download", cause)
		a.failTask(core, taskID, cause)
	}

	buffer := make([]byte, 256*1024)
	for {
		read, readErr := body.Read(buffer)
		if read > 0 {
			if _, writeErr := file.Write(buffer[:read]); writeErr != nil {
				fail(driveerr.Wrap(fmt.Errorf("写入本地文件失败：%w", writeErr), driveerr.CodeLocal))
				return
			}
			written += int64(read)
			if now := time.Now(); total > 0 && written < total && now.Sub(lastPatch) >= 200*time.Millisecond {
				lastPatch = now
				speed := 0.0
				if elapsed := now.Sub(start).Seconds(); elapsed > 0 {
					speed = float64(written) / elapsed
				}
				reported := written
				if reported > total {
					reported = total
				}
				_ = core.PatchTask(a.ctx, taskID, map[string]any{
					"transferredBytes": reported, "speed": speed, "status": "running",
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			fail(fmt.Errorf("下载中断：%w", readErr))
			return
		}
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		a.failTask(core, taskID, driveerr.Wrap(fmt.Errorf("关闭本地文件失败：%w", err), driveerr.CodeLocal))
		return
	}
	if err := placeDownloadedFile(tempPath, target); err != nil {
		_ = os.Remove(tempPath)
		a.failTask(core, taskID, driveerr.Wrap(fmt.Errorf("保存文件失败：%w", err), driveerr.CodeLocal))
		return
	}
	patch := map[string]any{"speed": 0, "status": "completed"}
	if total > 0 {
		patch["transferredBytes"] = total
	}
	if err := core.PatchTask(a.ctx, taskID, patch); err != nil {
		a.reportError("complete cloud download task", err)
	}
	a.logger.Printf("cloud download completed: %s (%d bytes)", target, written)
}

// downloadDirectory 取当前配置的接收目录（下载默认落在这里）。
// 每次从持久化配置读取，避免桌面端启动快照与设置页刚改的值不一致。
func (a *App) downloadDirectory() (string, error) {
	value, err := config.Load(a.configPath)
	if err != nil {
		wrapped := fmt.Errorf("读取下载目录配置失败：%w", err)
		a.reportError("cloud download directory", wrapped)
		return "", wrapped
	}
	dir := strings.TrimSpace(value.ReceiveDir)
	if dir == "" {
		err := fmt.Errorf("下载目录未配置")
		a.reportError("cloud download directory", err)
		return "", err
	}
	return dir, nil
}

// safeCloudDownloadName 从云端键取单段文件名，并清掉 Windows 不允许的字符。
// 文件名来自用户内容，不能直接拼进本地路径（分隔符、保留名、尾点都会出问题）。
func safeCloudDownloadName(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	name := path.Base(strings.TrimSuffix(trimmed, "/"))
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", fmt.Errorf("云端路径不合法：%s", key)
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	cleaned = strings.TrimSpace(strings.TrimRight(cleaned, ". "))
	if cleaned == "" {
		cleaned = "下载文件"
	}
	stem := strings.TrimSuffix(cleaned, filepath.Ext(cleaned))
	if windowsReservedNames[strings.ToUpper(stem)] {
		cleaned = "_" + cleaned
	}
	return cleaned, nil
}

// windowsReservedNames 是 Windows 设备名：即便带扩展名也不能作为文件名。
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// uniqueDownloadPath 在重名时生成「文件 (1).ext」，与 WPS / 钉钉的下载落盘规则一致。
func uniqueDownloadPath(target string) string {
	if _, err := os.Stat(target); err != nil {
		return target
	}
	ext := filepath.Ext(target)
	stem := strings.TrimSuffix(target, ext)
	for index := 1; index <= 9999; index++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, index, ext)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
	return target
}

// placeDownloadedFile 把临时文件改名为最终文件。
// Windows 的 Rename 不能覆盖已存在文件；另存为时保存框已确认覆盖，
// 因此改名失败且目标存在时先移除旧文件——下载失败时旧文件原样保留。
func placeDownloadedFile(tempPath, target string) error {
	if err := os.Rename(tempPath, target); err == nil {
		return nil
	}
	if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
		return removeErr
	}
	return os.Rename(tempPath, target)
}

func (a *App) CloudDelete(key string) error {
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	// 目录键（带尾斜杠）走递归：控制面把子树里每个文件各自软删进回收站，
	// 目录标记一并清掉。非目录仍走单对象软删，语义与挂载盘/WebDAV 一致。
	// 递归与否由键形决定，不再多加一个绑定参数——前端只有一个「删除」动作，
	// 让它去区分两种后端语义只会把「以为删了其实没删」留在界面里。
	if strings.HasSuffix(key, "/") {
		err = client.DeleteTree(a.ctx, token, drive.SpacePersonal, key)
	} else {
		// 软删（P1 回收站）：控制面置 state='recycled'，对象保留，可经回收站还原
		err = client.Delete(a.ctx, token, drive.SpacePersonal, key)
	}
	a.reportError("cloud delete", err)
	return err
}

// CloudRename 重命名文件（只改同目录下的末段名），底层复用控制面 /move。
//
// 刻意只做「改名」不做「移动」：跨目录移动是另一个语义（会改变路径层级、
// 可能与目标目录同名冲突），混在一个方法里会让调用方分不清自己触发了什么。
//
// key 是空间内相对路径（cloud.File.Key），不是 S3 物理键。
func (a *App) CloudRename(key, newName string) error {
	name := strings.TrimSpace(newName)
	if name == "" {
		return errors.New("文件名不能为空")
	}
	// 拒绝路径分隔符：改名不该顺手把文件搬到别的目录去
	if strings.ContainsAny(name, `/\`) {
		return errors.New("文件名不能包含路径分隔符")
	}
	if name == path.Base(key) {
		return nil
	}
	target := path.Join(path.Dir(key), name)
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	err = client.Move(a.ctx, token, drive.SpacePersonal, key, target)
	a.reportError("cloud rename", err)
	return err
}

// CloudMove 把文件移到另一个目录（文件名保持不变）。
//
// 与 CloudRename 的分工：rename 动末段名、位置不变；move 动目录、名字不变。
// 底层同为控制面 /move（服务端复制+删源，**fileId 不变**，分享链接与任务引用不失效），
// 拆成两个导出方法只是为了「调用点能表达出用户到底想干什么」，避免一个方法混两种语义。
//
// targetDir 是空间内相对目录（” 或 '/' 表示根）。
func (a *App) CloudMove(key, targetDir string) error {
	name := path.Base(key)
	dir := strings.Trim(strings.TrimSpace(targetDir), "/")
	// 拒绝向上逃逸：路径是相对空间的，出现 .. 说明调用方拼错了
	if dir == ".." || strings.HasPrefix(dir, "../") || strings.Contains(dir, "/../") {
		return errors.New("目标目录不合法")
	}
	target := name
	if dir != "" {
		target = path.Join(dir, name)
	}
	if target == key {
		// 原地不动：不必惊动服务端（也避免一次无意义的复制+删源）
		return nil
	}
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	err = client.Move(a.ctx, token, drive.SpacePersonal, key, target)
	a.reportError("cloud move", err)
	return err
}

// CloudStar 设置或取消星标。
//
// 星标是「用户 × 路径」的标记，不挂 fileId——同名文件删掉重建会换 fileId，
// 但用户认知里星标的还是那个位置。
func (a *App) CloudStar(key string, starred bool) error {
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	err = client.Star(a.ctx, token, drive.SpacePersonal, key, starred)
	a.reportError("cloud star", err)
	return err
}

// CloudStars 取当前空间的星标路径列表。
func (a *App) CloudStars() ([]string, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return nil, err
	}
	return client.Stars(a.ctx, token, drive.SpacePersonal)
}

// CloudTouch 记录一次最近访问。
//
// 刻意不返回 error：这是埋点，记录失败不该让「打开文件」这件事跟着失败。
func (a *App) CloudTouch(key string) {
	client, token, err := a.driveClient()
	if err != nil {
		return
	}
	_ = client.Touch(a.ctx, token, drive.SpacePersonal, key)
}

// CloudRecents 取最近访问路径（时间倒序）。
func (a *App) CloudRecents(limit int) ([]string, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return nil, err
	}
	return client.Recents(a.ctx, token, drive.SpacePersonal, limit)
}

// ── 回收站（P1，wps-cloud-drive-benchmark.md §6-P1）──────────────────────
// 三个导出方法直通控制面；fileId 是回收站内唯一定位方式（挂 fileId，不绑路径）。

// CloudRecycleList 列举个人空间回收站。
//
// ⚠️ 出参的 fileId 是**十进制字符串**，不是数字——这是一个必须守住的边界。
//
// 雪花 ID 是 19 位，超出 JS 安全整数 2^53-1。RuoYi 因此把 Long 序列化成字符串
// （Go 侧用 drive.FlexInt64 收下），但**跨 Wails 边界时又变回了 JSON number**：
// 前端拿到的是被静默截断的值，再传回来就查不到任何行。
// 实测本机 13 行真实 ID 经一次往返**全部改变**（known-issues.md KI-21）。
//
// 所以 JS 边界上只用字符串：能无损表示 19 位整数的最小改动，
// 也是控制面本身的选择（前后端在这件事上不该有两套表示法）。
func (a *App) CloudRecycleList() ([]drive.RecycleEntryView, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return nil, err
	}
	entries, err := client.RecycleList(a.ctx, token, drive.SpacePersonal)
	a.reportError("cloud recycle list", err)
	if err != nil {
		return nil, err
	}
	views := make([]drive.RecycleEntryView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, entry.View())
	}
	return views, nil
}

// CloudRecycleRestore 按 fileId 还原到原位置；原位置被占用时错误透出冲突说明。
//
// fileID 是十进制字符串（理由见 CloudRecycleList）：JS 侧没有那个整数的精确值，
// 收 number 就等于收一个已经错了的值。
func (a *App) CloudRecycleRestore(fileID string) error {
	id, err := drive.ParseFileID(fileID)
	if err != nil {
		return err
	}
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	err = client.RecycleRestore(a.ctx, token, drive.SpacePersonal, id)
	a.reportError("cloud recycle restore", err)
	return err
}

// CloudRecyclePurge 按 fileId 彻底删除（摘行 + 删对象，不可还原）。
func (a *App) CloudRecyclePurge(fileID string) error {
	id, err := drive.ParseFileID(fileID)
	if err != nil {
		return err
	}
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	err = client.RecyclePurge(a.ctx, token, drive.SpacePersonal, id)
	a.reportError("cloud recycle purge", err)
	return err
}

// CloudShare 生成可分享的链接。
//
// 当前实现返回控制面签发的预签名下载 URL，其有效期由控制面统一约束
// （easyshare.drive.get-expiry，默认 10 分钟），**不接受调用方指定的小时数**——
// 长效外链需要控制面提供独立的分享接口（可撤销、可审计），属后续阶段。
func (a *App) CloudShare(key string, expiryHours int) (string, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return "", err
	}
	url, err := client.PresignGet(a.ctx, token, drive.SpacePersonal, key)
	a.reportError("cloud share", err)
	return url, err
}

// CloudShareLink 创建**长效且可撤销**的分享链接。
//
// 与 CloudShare 的区别：后者是 10 分钟就失效、也收不回的预签名地址，
// 只适合「马上要下载一次」；发给同事/客户的链接必须是这一条——它落到
// es_share 表，能撤销、能设有效期、能统计访问。
//
// expiresHours <= 0 时由控制面取默认 7 天，上限 30 天。
func (a *App) CloudShareLink(key string, expiresHours int) (*drive.ShareLink, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return nil, err
	}
	link, err := client.CreateShare(a.ctx, token, drive.SpacePersonal, key, expiresHours)
	a.reportError("cloud share link", err)
	return link, err
}

// CloudShares 列出我创建的分享（含已撤销与已过期）。
func (a *App) CloudShares() ([]drive.ShareLink, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return nil, err
	}
	return client.Shares(a.ctx, token)
}

// CloudRevokeShare 撤销一条分享，立即生效。
func (a *App) CloudRevokeShare(shareID string) (bool, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return false, err
	}
	ok, err := client.RevokeShare(a.ctx, token, shareID)
	a.reportError("cloud revoke share", err)
	return ok, err
}

// CloudVersions 某个文件的历史版本（版本号倒序）。
//
// 只有被覆盖过的文件才有历史：留档发生在 presign-put 那一刻，
// 首次上传没有旧内容可留，返回空数组是正常结果。
func (a *App) CloudVersions(key string) ([]drive.VersionEntry, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return nil, err
	}
	return client.Versions(a.ctx, token, drive.SpacePersonal, key)
}

// CloudRestoreVersion 回滚到指定版本。
//
// 控制面会先把当前内容留档再覆盖，所以这个动作本身也可撤销。
func (a *App) CloudRestoreVersion(key string, versionID int64) error {
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	err = client.RestoreVersion(a.ctx, token, drive.SpacePersonal, key, versionID)
	a.reportError("cloud restore version", err)
	return err
}

// --- 知识问答（Core 作网关，令牌不进前端） ---

// KnowledgeStatus 获取知识登录态（是否配置/登录、服务器地址、用户名）。
func (a *App) KnowledgeStatus() (knowledge.StatusView, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.StatusView{}, err
	}
	result, err := client.KnowledgeStatus(a.ctx)
	a.reportError("knowledge status", err)
	return result, err
}

// KnowledgeLogin 登录知识服务；服务器地址形如 http://192.168.1.10:8000。
func (a *App) KnowledgeLogin(serverURL, username, password string) (knowledge.StatusView, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.StatusView{}, err
	}
	result, err := client.KnowledgeLogin(a.ctx, strings.TrimSpace(serverURL), username, password)
	a.reportError("knowledge login", err)
	return result, err
}

// KnowledgeLogout 退出知识服务登录。
func (a *App) KnowledgeLogout() error {
	client, err := a.coreClient()
	if err == nil {
		err = client.KnowledgeLogout(a.ctx)
	}
	a.reportError("knowledge logout", err)
	return err
}

// KnowledgeHealth 探测知识服务健康度（文档规模/LLM 状态）。
func (a *App) KnowledgeHealth() (knowledge.Health, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Health{}, err
	}
	result, err := client.KnowledgeHealth(a.ctx)
	a.reportError("knowledge health", err)
	return result, err
}

// KnowledgeAsk 向知识服务提问；多跳检索链路可能较慢，属预期行为。
func (a *App) KnowledgeAsk(question string) (knowledge.Answer, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Answer{}, err
	}
	result, err := client.KnowledgeAsk(a.ctx, question)
	a.reportError("knowledge ask", err)
	return result, err
}

// KnowledgeDocuments 拉知识库文档清单（最近更新排序）。
func (a *App) KnowledgeDocuments(limit int) (knowledge.DocumentList, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.DocumentList{}, err
	}
	result, err := client.KnowledgeDocuments(a.ctx, limit)
	a.reportError("knowledge documents", err)
	return result, err
}

// KnowledgeDocument 读取知识文档阅读页数据（结构化正文 + 元数据）。
func (a *App) KnowledgeDocument(fileID, versionID string) (knowledge.DocumentView, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.DocumentView{}, err
	}
	result, err := client.KnowledgeDocument(a.ctx, fileID, versionID)
	a.reportError("knowledge document", err)
	return result, err
}

// KnowledgeDownload 下载知识文档原件到用户选择的位置；返回保存路径（取消时为空）。
func (a *App) KnowledgeDownload(fileID, versionID, filename string) (string, error) {
	client, err := a.coreClient()
	if err != nil {
		return "", err
	}

	// 先让用户选保存位置，取消时不发起下载；避免保存框停留期间占着远端流
	defaultName := filepath.Base(strings.TrimSpace(filename))
	if defaultName == "." || defaultName == "" {
		defaultName = "文档"
	}
	target, dialogErr := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存文档原件",
		DefaultFilename: defaultName,
	})
	if dialogErr != nil {
		a.reportError("knowledge download dialog", dialogErr)
		return "", dialogErr
	}
	if target == "" {
		return "", nil
	}

	response, err := client.KnowledgeDocumentSource(a.ctx, fileID, versionID)
	if err != nil {
		a.reportError("knowledge download", err)
		return "", err
	}
	defer response.Body.Close()

	file, err := os.Create(target)
	if err != nil {
		a.reportError("knowledge download create", err)
		return "", err
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		a.reportError("knowledge download write", err)
		return "", err
	}
	if err := file.Close(); err != nil {
		a.reportError("knowledge download close", err)
		return "", err
	}
	return target, nil
}

// --- Local file browser (我的电脑) ---

func (a *App) ListDrives() ([]fsutil.DriveInfo, error) {
	drives, err := fsutil.ListDrives()
	a.reportError("list drives", err)
	return drives, err
}

func (a *App) ListDir(path string) ([]fsutil.FileEntry, error) {
	entries, err := fsutil.ListDir(path)
	a.reportError("list dir", err)
	return entries, err
}

func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

// registerNamespace 清理「此电脑」里的 EasyShare 条目，为登录后的挂载做准备。
//
// 「此电脑」里只该有**两个**盘：网盘（个人）与共享，都由 refreshSpaceMounts 在登录后
// 按实际授权挂载（见 spacemount.go）。局域网收件箱是本机目录，从文件管理器正常访问即可，
// 不单独占一个条目——多一个条目只会让「哪个是我的网盘」变得不清楚。
func (a *App) registerNamespace() {
	namespace.Log = a.logger.Printf

	// 清掉上次会话（或上次崩溃）留下的条目，含早期版本注册过的局域网条目。
	//
	// 必须无条件清：进程刚起，本次还没挂过任何东西，unmountAllSpaces 会因为
	// 「没挂过」而跳过清理，于是残留条目会一直留在「此电脑」里点进去报错。
	a.clearSpaceEntriesOnStartup()
}

// clearSpaceEntriesOnStartup 启动时无条件移除两个云端空间条目。
//
// 它们的有效性完全取决于登录态，而进程刚起时没有登录态。登录后
// refreshSpaceMounts 会按实际授权重新挂上。
func (a *App) clearSpaceEntriesOnStartup() {
	stale := []namespace.Entry{
		{CLSID: namespace.PersonalCLSID()},
		{CLSID: namespace.SharedCLSID()},
		// 早期版本把局域网目录也注册成一个条目，导致「此电脑」里出现三个盘。
		// 这里主动清掉，否则老用户升级后会一直留着那个多余条目。
		{CLSID: namespace.LANCLSID()},
	}
	if err := namespace.Unregister(stale); err != nil {
		a.logger.Printf("namespace clear stale space entries: %v", err)
	}
}

// --- Tray and window lifecycle ---

func (a *App) trayReady() {
	a.logger.Printf("system tray ready")
	a.updateTrayStatus()
}

func (a *App) showWindow() {
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

func (a *App) quitFromTray() {
	a.logger.Printf("quit requested from tray")
	a.quitAll()
}

// beginQuit 置退出标记并尽力优雅停 Core（watchdog 见标记后不再拉起），重复调用安全。
// 需要在退出前插入额外动作的调用方（如升级前启动安装包），应在 beginQuit 与
// runtime.Quit 之间执行该动作。
func (a *App) beginQuit() {
	a.quitLock.Lock()
	if a.quitting {
		a.quitLock.Unlock()
		return
	}
	a.quitting = true
	a.quitLock.Unlock()

	// 给 Core shutdown 加超时，避免 Core 无响应时阻塞退出流程（macOS 上常见）
	if client, err := a.coreClient(); err == nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		done := make(chan struct{})
		go func() {
			_ = client.Action(shutdownCtx, "/api/shutdown")
			close(done)
		}()
		select {
		case <-done:
			a.logger.Printf("Core shutdown completed")
		case <-shutdownCtx.Done():
			a.logger.Printf("Core shutdown timed out, forcing quit")
		}
		cancel()
	}

	// CfApi 同步根要在退出前注销：不注销会在 HKLM\SyncRootManager 留下
	// 指向已退出进程的注册表残留，下次注册同根会报 ERROR_ALREADY_EXISTS
	a.unmountCfapiSpaces()
}

// quitAll 优雅退出：停 Core 后关闭窗口（托盘退出与升级应用共用一条链路）。
func (a *App) quitAll() {
	a.beginQuit()
	runtime.Quit(a.ctx)
}

func (a *App) isQuitting() bool {
	a.quitLock.Lock()
	defer a.quitLock.Unlock()
	return a.quitting
}

func (a *App) updateTrayStatus() {
	client, err := a.coreClient()
	status := "已停止"
	if err == nil {
		if snap, snapErr := client.Snapshot(a.ctx); snapErr == nil && snap.Status.Core {
			status = "运行中"
		}
	}
	select {
	case a.trayStatusCh <- status:
	default:
	}
}

// --- Real-time event stream ---

// eventStream 订阅 Core 的 WebSocket 事件流，将每条事件转发为 Wails 前端事件。
// 断线后指数退避重连（1s → 2s → 4s → 最大 8s），退出时自动停止。
func (a *App) eventStream() {
	backoff := time.Second
	const maxBackoff = 8 * time.Second

	for {
		if a.isQuitting() {
			return
		}
		client, err := a.coreClient()
		if err != nil {
			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		err = client.SubscribeEvents(ctx, func(raw []byte) {
			// 转发原始 JSON 给前端，前端按 type 字段分发
			runtime.EventsEmit(a.ctx, "core-event", string(raw))
			// 传输完成/失败时弹系统通知
			a.notifyTransfer(raw)
		})
		cancel()

		if a.isQuitting() {
			return
		}
		a.logger.Printf("event stream disconnected: %v, reconnecting in %v", err, backoff)
		time.Sleep(backoff)
		backoff = min(backoff*2, maxBackoff)
	}
}

// notifyTransfer 解析事件 JSON，传输完成或失败时弹系统通知。
func (a *App) notifyTransfer(raw []byte) {
	var event struct {
		Type string `json:"type"`
		Data struct {
			FileName string `json:"fileName"`
			Status   string `json:"status"`
			Peer     string `json:"peer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return
	}
	if event.Type != "transfer.updated" {
		return
	}
	switch event.Data.Status {
	case "completed":
		_ = runtime.SendNotification(a.ctx, runtime.NotificationOptions{
			Title: "传输完成",
			Body:  event.Data.FileName + " — " + event.Data.Peer,
		})
	case "failed":
		_ = runtime.SendNotification(a.ctx, runtime.NotificationOptions{
			Title: "传输失败",
			Body:  event.Data.FileName + " — " + event.Data.Peer,
		})
	}
}

// --- Core recovery ---

// watchdog monitors Core health and restarts it if unresponsive.
func (a *App) watchdog() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	failures := 0

	for range ticker.C {
		if a.isQuitting() {
			return
		}
		if desktop.CoreHealthy(a.processOptions) {
			if failures > 0 {
				a.logger.Printf("watchdog: Core recovered (was unhealthy for %d checks)", failures)
			}
			failures = 0
			continue
		}
		failures++
		if failures < 3 {
			continue
		}
		a.logger.Printf("watchdog: Core unresponsive for %d checks, restarting", failures)
		failures = 0

		if err := desktop.EnsureCore(a.ctx, a.processOptions); err != nil {
			a.reportError("watchdog restart Core", err)
			a.logger.Printf("watchdog: restart failed: %v", err)
			continue
		}
		baseURL := "http://" + net.JoinHostPort(a.config.APIHost, strconv.Itoa(a.config.APIPort))
		a.clientLock.Lock()
		a.client = desktop.NewClient(baseURL, a.config.APIToken)
		a.clientLock.Unlock()
		a.logger.Printf("watchdog: Core restarted successfully")
		a.updateTrayStatus()
	}
}
