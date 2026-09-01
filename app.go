package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
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

	// 快捷面板（剪切板插件 ?panel=1 形态的宿主窗口，见 panel_surface.go 与平台实现）。
	// panelEmit 由平台窗口就绪后注入：向面板页推送事件（clipboard:changed、panel:shown）。
	panelListener net.Listener
	panelURL      string
	panelEmitMu   sync.RWMutex
	panelEmit     func(event string, payload any)
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
	return &App{
		logger:       log.New(io.Discard, "", log.Ldate|log.Ltime|log.Lmicroseconds),
		trayStatusCh: make(chan string, 1),
		trayUserCh:   make(chan AuthUser, 1),
		mounts:       newSpaceMounts(),
		assetMux:     http.NewServeMux(),
	}
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
	a.accountSession = nil
	a.accountMu.Unlock()
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

// AdminSharedMembers 取共享空间的成员授权：账号 ID → read/write。
func (a *App) AdminSharedMembers() (map[string]string, error) {
	client, token, err := a.adminSession()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	members, err := client.SharedMembers(ctx, token)
	a.reportError("admin list shared members", err)
	return members, err
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

// AdminGrantShared 授予或撤销某账号对共享空间的权限。permission 传空即撤销。
func (a *App) AdminGrantShared(userID, permission string) error {
	client, token, err := a.adminSession()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.GrantShared(ctx, token, userID, permission)
	if err == nil {
		a.logger.Printf("admin set shared permission for %s to %q", userID, permission)
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

// OpenFile 用系统默认应用打开指定文件（Finder 双击 / 资源管理器选中）。
func (a *App) OpenFile(path string) error {
	if path == "" {
		return nil
	}
	err := fsutil.OpenFile(path)
	a.reportError("open file", err)
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
	return drive.New(base), session.Token, nil
}

// driveObjectToFile 把控制面返回的对象转成前端已在用的 cloud.File。
// 控制面不返回 contentType（S3 列举本就没有），按扩展名推断。
func driveObjectToFile(object drive.Object) cloud.File {
	contentType := cloud.ContentTypeForKey(object.Path)
	return cloud.File{
		Key:          object.Path,
		Name:         filepath.Base(object.Path),
		Size:         object.Size,
		ContentType:  contentType,
		LastModified: object.LastModified,
		PreviewKind:  cloud.DetectPreviewKind(contentType, object.Path),
	}
}

func (a *App) CloudList() ([]cloud.File, error) {
	client, token, err := a.driveClient()
	if err != nil {
		return nil, err
	}
	objects, err := client.Objects(a.ctx, token, drive.SpacePersonal)
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

func (a *App) CloudUpload() (string, error) {
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
	go a.uploadSingleFile(core, driveClient, token, drive.SpacePersonal, path, filepath.Base(path))
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
				a.uploadDir(core, driveClient, token, space, path)
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
	start := time.Now()
	var lastPatch time.Time
	uploadErr := driveClient.UploadFile(a.ctx, token, space, objectKey, filePath, func(sent, total int64) {
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
	})
	if uploadErr != nil {
		a.reportError("cloud upload", uploadErr)
		_ = core.PatchTask(a.ctx, created.ID, map[string]any{
			"status": "failed", "error": uploadErr.Error(),
		})
		return
	}
	_ = core.PatchTask(a.ctx, created.ID, map[string]any{
		"transferredBytes": fileSize, "speed": 0, "status": "completed",
	})
}

// uploadDir 遍历目录逐文件上传，保留目录结构。进度写入 Core 统一任务存储。
func (a *App) uploadDir(core *desktop.Client, driveClient *drive.Client, token, space, dir string) {
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
	for i, filePath := range files {
		rel, relErr := filepath.Rel(dir, filePath)
		if relErr != nil {
			continue
		}
		objectKey := folderName + "/" + filepath.ToSlash(rel)
		if uploadErr := driveClient.UploadFile(a.ctx, token, space, objectKey, filePath, nil); uploadErr != nil {
			a.reportError("cloud folder upload", uploadErr)
			_ = core.PatchTask(a.ctx, created.ID, map[string]any{
				"status": "failed", "error": uploadErr.Error(),
			})
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
func (a *App) CloudUploadFolder() (string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要上传到网盘的文件夹"})
	if err != nil || dir == "" {
		return "", err
	}
	core, driveClient, token, err := a.uploadClients()
	if err != nil {
		return "", err
	}
	go a.uploadDir(core, driveClient, token, drive.SpacePersonal, dir)
	return filepath.Base(dir), nil
}

func (a *App) CloudDownload(key string) error {
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	url, err := client.PresignGet(a.ctx, token, drive.SpacePersonal, key)
	if err != nil {
		a.reportError("cloud download", err)
		return err
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

func (a *App) CloudDelete(key string) error {
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	err = client.Delete(a.ctx, token, drive.SpacePersonal, key)
	a.reportError("cloud delete", err)
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
