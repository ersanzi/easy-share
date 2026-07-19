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

	"easyshare/internal/config"
	"easyshare/internal/desktop"
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
	if err := desktop.EnsureCore(ctx, options); err != nil {
		a.reportError("ensure Core", err)
		runtime.LogError(ctx, err.Error())
		return
	}
	a.clientLock.Lock()
	a.client = desktop.NewClient(baseURL, value.APIToken)
	a.clientLock.Unlock()
	a.logger.Printf("connected to Core at %s", baseURL)
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

func (a *App) SelectShareDirectory() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 WebDAV 共享目录", DefaultDirectory: a.config.WebDAVRoot})
	a.reportError("select share directory", err)
	return path, err
}

func (a *App) SelectFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要发送的文件"})
	a.reportError("select file", err)
	return path, err
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
