package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"easyshare/internal/cloud"
	"easyshare/internal/config"
	"easyshare/internal/desktop"
	"easyshare/internal/drive"
	"easyshare/internal/logging"
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
}

func NewApp() *App {
	return &App{
		logger:       log.New(io.Discard, "", log.Ldate|log.Ltime|log.Lmicroseconds),
		trayStatusCh: make(chan string, 1),
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

	a.configPath = filepath.Join(os.Getenv("LOCALAPPDATA"), "EasyShare", "config.json")
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

	// Clean up stale drive mapping left by a previous crashed Core.
	a.cleanStaleMapping()

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
}

func (a *App) Shutdown(_ context.Context) {
	a.logger.Printf("desktop window closing")
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
	return result, err
}

func (a *App) SendFile(peerID, path string) error {
	client, err := a.coreClient()
	if err == nil {
		err = client.Send(a.ctx, peerID, path)
	}
	a.reportError("send file", err)
	return err
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
func (a *App) MapDrive() error   { return a.driveAction("map drive", "/api/drive/map") }
func (a *App) UnmapDrive() error { return a.driveAction("unmap drive", "/api/drive/unmap") }
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
	DriveLetter string `json:"driveLetter"`
}

func (a *App) GetSettings() Settings {
	return Settings{
		DeviceName:  a.config.DeviceName,
		ReceiveDir:  a.config.ReceiveDir,
		WebDAVRoot:  a.config.WebDAVRoot,
		DriveLetter: a.config.DriveLetter,
	}
}

func (a *App) SaveSettings(deviceName, receiveDir, webdavRoot, driveLetter string) error {
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
	driveLetter = strings.TrimSpace(driveLetter)
	if len(driveLetter) == 1 {
		driveLetter += ":"
	}
	if len(driveLetter) != 2 || driveLetter[1] != ':' {
		return fmt.Errorf("盘符格式无效，应为单个字母如 Z")
	}
	letter := strings.ToUpper(driveLetter[:1])
	if letter < "D" || letter > "Z" {
		return fmt.Errorf("盘符须在 D-Z 之间")
	}
	driveLetter = letter + ":"

	a.config.DeviceName = deviceName
	a.config.ReceiveDir = receiveDir
	a.config.WebDAVRoot = webdavRoot
	a.config.DriveLetter = driveLetter

	if err := config.Save(a.configPath, a.config); err != nil {
		a.reportError("save settings", err)
		return fmt.Errorf("保存配置失败：%w", err)
	}

	// Attempt to notify Core to reload configuration; non-fatal if unavailable.
	if client, err := a.coreClient(); err == nil {
		_ = client.Action(a.ctx, "/api/config/reload")
	}
	a.logger.Printf("settings saved: device=%s receive=%s webdav=%s drive=%s", deviceName, receiveDir, webdavRoot, driveLetter)
	return nil
}

func (a *App) SelectFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要发送的文件"})
	a.reportError("select file", err)
	return path, err
}

// CloudSettings is the user-facing RustFS configuration.
type CloudSettings struct {
	Endpoint          string `json:"endpoint"`
	Region            string `json:"region"`
	AccessKeyID       string `json:"accessKeyId"`
	SecretAccessKey   string `json:"secretAccessKey"`
	Bucket            string `json:"bucket"`
	AllowInsecureHTTP bool   `json:"allowInsecureHttp"`
}

func (a *App) GetCloudSettings() CloudSettings {
	c := a.config.Cloud
	return CloudSettings{
		Endpoint:          c.Endpoint,
		Region:            c.Region,
		AccessKeyID:       c.AccessKeyID,
		SecretAccessKey:   c.SecretAccessKey,
		Bucket:            c.Bucket,
		AllowInsecureHTTP: c.AllowInsecureHTTP,
	}
}

func (a *App) SaveCloudSettings(endpoint, region, accessKeyID, secretAccessKey, bucket string, allowInsecureHTTP bool) error {
	a.config.Cloud = config.CloudConfig{
		Endpoint:          strings.TrimSpace(endpoint),
		Region:            strings.TrimSpace(region),
		AccessKeyID:       strings.TrimSpace(accessKeyID),
		SecretAccessKey:   strings.TrimSpace(secretAccessKey),
		Bucket:            strings.TrimSpace(bucket),
		AllowInsecureHTTP: allowInsecureHTTP,
	}
	if err := config.Save(a.configPath, a.config); err != nil {
		a.reportError("save cloud settings", err)
		return fmt.Errorf("保存网盘配置失败：%w", err)
	}
	// Notify Core to reload; cloud service will be re-initialized on next restart.
	if client, err := a.coreClient(); err == nil {
		_ = client.Action(a.ctx, "/api/config/reload")
	}
	a.logger.Printf("cloud settings saved: endpoint=%s bucket=%s", endpoint, bucket)
	return nil
}

func (a *App) SelectFiles() ([]string, error) {
	paths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要发送的文件（可多选）"})
	a.reportError("select files", err)
	return paths, err
}

// --- Cloud drive ---

func (a *App) CloudList() ([]cloud.File, error) {
	client, err := a.coreClient()
	if err != nil {
		return nil, err
	}
	result, err := client.CloudList(a.ctx)
	a.reportError("cloud list", err)
	return result, err
}

func (a *App) CloudUpload() (cloud.UploadResult, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要上传到网盘的文件"})
	if err != nil || path == "" {
		return cloud.UploadResult{}, err
	}
	client, clientErr := a.coreClient()
	if clientErr != nil {
		return cloud.UploadResult{}, clientErr
	}
	result, err := client.CloudUpload(a.ctx, path)
	a.reportError("cloud upload", err)
	return result, err
}

func (a *App) CloudDownload(key string) error {
	client, err := a.coreClient()
	if err != nil {
		return err
	}
	url, err := client.CloudDownload(a.ctx, key)
	if err != nil {
		a.reportError("cloud download", err)
		return err
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

func (a *App) CloudDelete(key string) error {
	client, err := a.coreClient()
	if err != nil {
		return err
	}
	err = client.CloudDelete(a.ctx, key)
	a.reportError("cloud delete", err)
	return err
}

func (a *App) CloudShare(key string, expiryHours int) (string, error) {
	client, err := a.coreClient()
	if err != nil {
		return "", err
	}
	url, err := client.CloudShare(a.ctx, key, expiryHours)
	a.reportError("cloud share", err)
	return url, err
}

func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
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
	a.quitLock.Lock()
	a.quitting = true
	a.quitLock.Unlock()
	a.logger.Printf("quit requested from tray")

	// Attempt graceful Core shutdown before exiting the desktop process.
	if client, err := a.coreClient(); err == nil {
		_ = client.Action(a.ctx, "/api/shutdown")
		a.logger.Printf("Core shutdown requested from tray")
	}
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

// --- Core recovery ---

// cleanStaleMapping removes a drive mapping left behind by a previously crashed
// Core process. This is best-effort: if no mapping exists or the unmap fails,
// startup proceeds normally.
func (a *App) cleanStaleMapping() {
	webdavURL := "http://127.0.0.1:" + strconv.Itoa(a.config.WebDAVPort)
	mapper := drive.NewMapper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := mapper.Unmap(ctx, a.config.DriveLetter, webdavURL); err == nil {
		a.logger.Printf("cleaned stale drive mapping %s", a.config.DriveLetter)
	}
}

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
