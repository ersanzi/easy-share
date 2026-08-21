package main

import (
	"context"
	"encoding/json"
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
	"easyshare/internal/fsutil"
	"easyshare/internal/knowledge"
	"easyshare/internal/logging"
	"easyshare/internal/namespace"
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

func (a *App) CloudList() ([]cloud.File, error) {
	client, err := a.coreClient()
	if err != nil {
		return nil, err
	}
	result, err := client.CloudList(a.ctx)
	a.reportError("cloud list", err)
	return result, err
}

// CloudPreview 返回文件的安全预览能力。
func (a *App) CloudPreview(key string) (cloud.Preview, error) {
	client, err := a.coreClient()
	if err != nil {
		return cloud.Preview{}, err
	}
	result, err := client.CloudPreview(a.ctx, key)
	a.reportError("cloud preview", err)
	return result, err
}

func (a *App) CloudUpload() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要上传到网盘的文件"})
	if err != nil || path == "" {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	fileName := filepath.Base(path)
	fileSize := info.Size()

	client, clientErr := a.coreClient()
	if clientErr != nil {
		return "", clientErr
	}

	// 在 Core 统一任务存储中注册云盘上传任务
	created, createErr := client.CreateTask(a.ctx, map[string]any{
		"kind": "cloud_upload", "fileName": fileName, "direction": "send",
		"peer": "网盘", "totalBytes": fileSize, "status": "running",
	})
	if createErr != nil {
		a.reportError("create cloud upload task", createErr)
		return "", createErr
	}

	go func() {
		start := time.Now()
		var lastPatch time.Time

		result, uploadErr := client.CloudUploadStream(a.ctx, path, func(sent, total int64) {
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
			_ = client.PatchTask(a.ctx, created.ID, map[string]any{
				"transferredBytes": sent, "speed": speed, "status": "running",
			})
		})

		if uploadErr != nil {
			a.reportError("cloud upload", uploadErr)
			_ = client.PatchTask(a.ctx, created.ID, map[string]any{
				"status": "failed", "error": uploadErr.Error(),
			})
			return
		}
		a.logger.Printf("cloud upload done: key=%s etag=%s", result.Key, result.ETag)
		_ = client.PatchTask(a.ctx, created.ID, map[string]any{
			"transferredBytes": fileSize, "speed": 0, "status": "completed",
		})
	}()

	return fileName, nil
}

// CloudUploadPaths 将指定路径（文件或文件夹）上传到网盘，供拖拽调用。
// 文件以文件名为键，文件夹保留目录结构（"文件夹名/相对路径"）。
func (a *App) CloudUploadPaths(paths []string) error {
	client, err := a.coreClient()
	if err != nil {
		return err
	}
	go func() {
		for _, path := range paths {
			info, statErr := os.Stat(path)
			if statErr != nil {
				continue
			}
			if info.IsDir() {
				a.uploadDir(client, path)
			} else {
				a.uploadSingleFile(client, path, filepath.Base(path))
			}
		}
	}()
	return nil
}

// uploadSingleFile 上传单个文件，进度写入 Core 统一任务存储。
func (a *App) uploadSingleFile(client *desktop.Client, filePath, objectKey string) {
	fileName := filepath.Base(filePath)
	info, err := os.Stat(filePath)
	if err != nil {
		return
	}
	fileSize := info.Size()
	created, createErr := client.CreateTask(a.ctx, map[string]any{
		"kind": "cloud_upload", "fileName": fileName, "direction": "send",
		"peer": "网盘", "totalBytes": fileSize, "status": "running",
	})
	if createErr != nil {
		a.reportError("create cloud upload task", createErr)
		return
	}
	start := time.Now()
	var lastPatch time.Time
	_, uploadErr := client.CloudUploadStreamWithKey(a.ctx, filePath, objectKey, func(sent, total int64) {
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
		_ = client.PatchTask(a.ctx, created.ID, map[string]any{
			"transferredBytes": sent, "speed": speed, "status": "running",
		})
	})
	if uploadErr != nil {
		a.reportError("cloud upload", uploadErr)
		_ = client.PatchTask(a.ctx, created.ID, map[string]any{
			"status": "failed", "error": uploadErr.Error(),
		})
		return
	}
	_ = client.PatchTask(a.ctx, created.ID, map[string]any{
		"transferredBytes": fileSize, "speed": 0, "status": "completed",
	})
}

// uploadDir 遍历目录逐文件上传，保留目录结构。进度写入 Core 统一任务存储。
func (a *App) uploadDir(client *desktop.Client, dir string) {
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
	created, createErr := client.CreateTask(a.ctx, map[string]any{
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
		_, uploadErr := client.CloudUploadStreamWithKey(a.ctx, filePath, objectKey, nil)
		if uploadErr != nil {
			a.reportError("cloud folder upload", uploadErr)
			_ = client.PatchTask(a.ctx, created.ID, map[string]any{
				"status": "failed", "error": uploadErr.Error(),
			})
			return
		}
		_ = client.PatchTask(a.ctx, created.ID, map[string]any{
			"transferredBytes": int64(i + 1), "status": "running",
		})
	}
	a.logger.Printf("cloud folder upload done: %s (%d files)", folderName, len(files))
	_ = client.PatchTask(a.ctx, created.ID, map[string]any{
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
	client, clientErr := a.coreClient()
	if clientErr != nil {
		return "", clientErr
	}
	go a.uploadDir(client, dir)
	return filepath.Base(dir), nil
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

// registerNamespace adds EasyShare entries to the system file-space entry.
// Windows：注册「此电脑」命名空间条目；macOS：Finder 挂载 WebDAV 卷。
// 网盘和共享入口直接指向 WebDAV，不暴露盘符。
func (a *App) registerNamespace() {
	namespace.Log = a.logger.Printf
	iconPath := namespace.IconFromBuild()
	cloudPort := a.config.WebDAVPort + 1
	entries := namespace.DefaultEntries(iconPath, cloudPort, a.config.WebDAVPort)
	if err := namespace.Register(entries); err != nil {
		a.logger.Printf("namespace register: %v", err)
	} else {
		a.logger.Printf("namespace registered")
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
	a.quitLock.Lock()
	if a.quitting {
		a.quitLock.Unlock()
		return
	}
	a.quitting = true
	a.quitLock.Unlock()
	a.logger.Printf("quit requested from tray")

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
