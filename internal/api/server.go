package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"easyshare/internal/cloud"
	"easyshare/internal/config"
	"easyshare/internal/discovery"
	"easyshare/internal/drive"
	"easyshare/internal/task"
	"easyshare/internal/transfer"
	"github.com/coder/websocket"
)

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Status struct {
	Core         bool `json:"core"`
	Discovery    bool `json:"discovery"`
	Receiver     bool `json:"receiver"`
	WebDAV       bool `json:"webdav"`
	DriveMapped  bool `json:"driveMapped"`
	CloudEnabled bool `json:"cloudEnabled"`
}

type driveService interface {
	Start(int) error
	Stop(context.Context) error
	Running() bool
}

type driveMapper interface {
	Map(context.Context, string, string, string, string) error
	Unmap(context.Context, string, string) error
}

type Server struct {
	config       config.Config
	configPath   string
	tasks        *task.Store
	hub          *eventHub
	httpServer   *http.Server
	listener     net.Listener
	shutdownOnce sync.Once
	shutdown     chan struct{}
	driveService driveService
	mapper       driveMapper
	cloud        *cloud.Service
	statusMutex  sync.RWMutex
	status       Status
	peers        func() []discovery.Peer
	discovery    *discovery.Service
	receiver     *transfer.Receiver
	cancelCore   context.CancelFunc
}

func NewServer(value config.Config, tasks *task.Store) *Server {
	server := &Server{config: value, tasks: tasks, hub: newEventHub(), shutdown: make(chan struct{}), status: Status{Core: true}}
	server.httpServer = &http.Server{Handler: server.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	return server
}

func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, request *http.Request) {
		nonce := request.URL.Query().Get("nonce")
		if nonce == "" {
			writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "nonce_required", Message: "nonce required"})
			return
		}
		mac := hmac.New(sha256.New, []byte(server.config.APIToken))
		_, _ = mac.Write([]byte(nonce))
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "version": "0.1.0", "deviceId": server.config.DeviceID, "proof": hex.EncodeToString(mac.Sum(nil))})
	})
	mux.Handle("GET /api/status", server.auth(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		server.statusMutex.RLock()
		value := server.status
		server.statusMutex.RUnlock()
		writeJSON(writer, http.StatusOK, value)
	})))
	mux.Handle("GET /api/peers", server.auth(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if server.peers == nil {
			writeJSON(writer, http.StatusOK, []discovery.Peer{})
			return
		}
		writeJSON(writer, http.StatusOK, server.peers())
	})))
	mux.Handle("GET /api/tasks", server.auth(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, server.tasks.List())
	})))
	mux.Handle("POST /api/tasks/clear", server.auth(http.HandlerFunc(server.clearTasks)))
	mux.Handle("DELETE /api/tasks/{id}", server.auth(http.HandlerFunc(server.deleteTask)))
	mux.Handle("GET /api/events", server.auth(http.HandlerFunc(server.events)))
	mux.Handle("POST /api/shutdown", server.auth(http.HandlerFunc(server.shutdownAll)))
	mux.Handle("POST /api/drive/start", server.auth(http.HandlerFunc(server.startDrive)))
	mux.Handle("POST /api/drive/stop", server.auth(http.HandlerFunc(server.stopDrive)))
	mux.Handle("POST /api/drive/map", server.auth(http.HandlerFunc(server.mapDrive)))
	mux.Handle("POST /api/drive/unmap", server.auth(http.HandlerFunc(server.unmapDrive)))
	mux.Handle("POST /api/transfers", server.auth(http.HandlerFunc(server.sendTransfer)))
	mux.Handle("POST /api/transfers/{id}/accept", server.auth(http.HandlerFunc(server.acceptTransfer)))
	mux.Handle("POST /api/transfers/{id}/reject", server.auth(http.HandlerFunc(server.rejectTransfer)))
	mux.Handle("POST /api/config/reload", server.auth(http.HandlerFunc(server.reloadConfig)))
	mux.Handle("GET /api/cloud/files", server.auth(http.HandlerFunc(server.cloudList)))
	mux.Handle("POST /api/cloud/upload", server.auth(http.HandlerFunc(server.cloudUpload)))
	mux.Handle("POST /api/cloud/download", server.auth(http.HandlerFunc(server.cloudDownload)))
	mux.Handle("DELETE /api/cloud/files", server.auth(http.HandlerFunc(server.cloudDelete)))
	mux.Handle("POST /api/cloud/share", server.auth(http.HandlerFunc(server.cloudShare)))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, pattern := mux.Handler(request)
		if pattern == "" {
			writeJSON(writer, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "route not found"})
			return
		}
		mux.ServeHTTP(writer, request)
	})
}

func (server *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+server.config.APIToken {
			writeJSON(writer, http.StatusUnauthorized, ErrorResponse{Code: "unauthorized", Message: "missing or invalid token"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) events(writer http.ResponseWriter, request *http.Request) {
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{OriginPatterns: []string{"127.0.0.1:*", "localhost:*"}})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	server.hub.serve(request.Context(), connection)
}

func (server *Server) Start(ctx context.Context) error {
	if net.ParseIP(server.config.APIHost) == nil || !net.ParseIP(server.config.APIHost).IsLoopback() {
		return errors.New("API host must be loopback")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(server.config.APIHost, strconv.Itoa(server.config.APIPort)))
	if err != nil {
		return err
	}
	server.listener = listener
	go func() {
		select {
		case <-ctx.Done():
		case <-server.shutdown:
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.httpServer.Shutdown(shutdownCtx)
	}()
	err = server.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (server *Server) Addr() string {
	if server.listener == nil {
		return ""
	}
	return server.listener.Addr().String()
}
func (server *Server) Publish(event Event)             { server.hub.publish(event) }
func (server *Server) ShutdownSignal() <-chan struct{} { return server.shutdown }
func (server *Server) ConfigureDrive(service driveService, mapper driveMapper) {
	server.driveService, server.mapper = service, mapper
}
func (server *Server) ConfigureShutdown(cancel context.CancelFunc) { server.cancelCore = cancel }
func (server *Server) ConfigureConfigPath(path string)            { server.configPath = path }
func (server *Server) ConfigureDiscovery(service *discovery.Service) {
	server.discovery = service
	server.peers = service.Peers
}
func (server *Server) ConfigureTransfer(receiver *transfer.Receiver) {
	server.receiver = receiver
}
func (server *Server) ConfigureCloud(service *cloud.Service) {
	server.cloud = service
	server.statusMutex.Lock()
	server.status.CloudEnabled = service != nil
	server.statusMutex.Unlock()
}
func (server *Server) MarkDiscovery(running bool) {
	server.statusMutex.Lock()
	server.status.Discovery = running
	value := server.status
	server.statusMutex.Unlock()
	server.Publish(Event{Type: "status.changed", Data: value})
}
func (server *Server) MarkReceiver(running bool) {
	server.statusMutex.Lock()
	server.status.Receiver = running
	value := server.status
	server.statusMutex.Unlock()
	server.Publish(Event{Type: "status.changed", Data: value})
}
func (server *Server) sendTransfer(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		PeerID   string `json:"peerId"`
		FilePath string `json:"filePath"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil {
		writeJSON(writer, 400, ErrorResponse{Code: "invalid_request", Message: "invalid JSON"})
		return
	}
	if server.discovery == nil {
		writeJSON(writer, 503, ErrorResponse{Code: "discovery_unavailable", Message: "discovery unavailable"})
		return
	}
	peer, ok := server.discovery.Peer(input.PeerID)
	if !ok {
		writeJSON(writer, 404, ErrorResponse{Code: "peer_not_found", Message: "peer not found"})
		return
	}
	go func() {
		err := transfer.Send(context.Background(), transfer.SendRequest{Address: net.JoinHostPort(peer.IP, strconv.Itoa(peer.TransferPort)), FilePath: input.FilePath, DeviceID: server.config.DeviceID, DeviceName: server.config.DeviceName, PeerName: peer.DeviceName, Tasks: server.tasks, OnUpdate: func(value task.Task) { server.Publish(Event{Type: "transfer.updated", Data: value}) }})
		if err != nil {
			server.Publish(Event{Type: "error", Data: ErrorResponse{Code: "transfer_failed", Message: err.Error()}})
		}
	}()
	writeJSON(writer, 202, map[string]bool{"accepted": true})
}
func (server *Server) acceptTransfer(writer http.ResponseWriter, request *http.Request) {
	if server.receiver == nil {
		writeJSON(writer, 503, ErrorResponse{Code: "receiver_unavailable", Message: "receiver unavailable"})
		return
	}
	var input struct {
		SaveDir string `json:"saveDir"`
	}
	_ = json.NewDecoder(request.Body).Decode(&input)
	var err error
	if input.SaveDir != "" {
		err = server.receiver.AcceptTo(request.PathValue("id"), input.SaveDir)
	} else {
		err = server.receiver.Accept(request.PathValue("id"))
	}
	if err != nil {
		writeJSON(writer, 404, ErrorResponse{Code: "task_not_found", Message: err.Error()})
		return
	}
	writeJSON(writer, 200, map[string]bool{"accepted": true})
}
func (server *Server) rejectTransfer(writer http.ResponseWriter, request *http.Request) {
	if server.receiver == nil {
		writeJSON(writer, 503, ErrorResponse{Code: "receiver_unavailable", Message: "receiver unavailable"})
		return
	}
	if err := server.receiver.Reject(request.PathValue("id")); err != nil {
		writeJSON(writer, 404, ErrorResponse{Code: "task_not_found", Message: err.Error()})
		return
	}
	writeJSON(writer, 200, map[string]bool{"rejected": true})
}

func (server *Server) startDrive(writer http.ResponseWriter, _ *http.Request) {
	if server.driveService == nil {
		writeJSON(writer, 500, ErrorResponse{Code: "drive_unavailable", Message: "drive service unavailable"})
		return
	}
	if err := server.driveService.Start(server.config.WebDAVPort); err != nil && !server.driveService.Running() {
		log.Printf("start WebDAV: %v", err)
		writeJSON(writer, 500, ErrorResponse{Code: "drive_start_failed", Message: err.Error()})
		return
	}
	server.setDriveStatus(true, false)
	log.Printf("WebDAV ready at %s with Digest authentication", server.webDAVURL())
	writeJSON(writer, 200, map[string]bool{"running": true})
}
func (server *Server) stopDrive(writer http.ResponseWriter, request *http.Request) {
	if err := server.stopDriveServices(request.Context()); err != nil {
		log.Printf("stop drive services: %v", err)
		writeJSON(writer, http.StatusConflict, ErrorResponse{Code: "drive_stop_failed", Message: err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"running": false})
}
func (server *Server) mapDrive(writer http.ResponseWriter, request *http.Request) {
	url := server.webDAVURL()
	if server.mapper == nil {
		writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "mapper_unavailable", Message: "mapper unavailable"})
		return
	}
	if server.driveService == nil {
		writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "drive_unavailable", Message: "drive service unavailable"})
		return
	}
	if !server.driveService.Running() {
		if err := server.driveService.Start(server.config.WebDAVPort); err != nil {
			log.Printf("start WebDAV before mapping: %v", err)
			writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "drive_start_failed", Message: err.Error()})
			return
		}
		server.setDriveStatus(true, false)
		log.Printf("WebDAV ready at %s with Digest authentication", url)
	}
	if err := server.mapper.Map(request.Context(), server.config.DriveLetter, url, server.config.WebDAVUsername, server.config.WebDAVPassword); err != nil {
		log.Printf("map drive %s to %s: %v", server.config.DriveLetter, url, err)
		server.setDriveStatus(true, false)
		message := err.Error()
		if errors.Is(err, drive.ErrDriveOccupied) {
			message = fmt.Sprintf("%s 已被其他磁盘或网络位置占用，请释放该盘符后重试", server.config.DriveLetter)
		}
		writeJSON(writer, http.StatusConflict, ErrorResponse{Code: "drive_map_failed", Message: message})
		return
	}
	server.setDriveStatus(true, true)
	log.Printf("drive %s mapped to %s", server.config.DriveLetter, url)
	writeJSON(writer, http.StatusOK, map[string]bool{"mapped": true})
}
func (server *Server) unmapDrive(writer http.ResponseWriter, request *http.Request) {
	url := server.webDAVURL()
	if server.mapper != nil {
		if err := server.mapper.Unmap(request.Context(), server.config.DriveLetter, url); err != nil {
			log.Printf("unmap drive %s: %v", server.config.DriveLetter, err)
			writeJSON(writer, 409, ErrorResponse{Code: "drive_unmap_failed", Message: err.Error()})
			return
		}
	}
	running := server.driveService != nil && server.driveService.Running()
	server.setDriveStatus(running, false)
	writeJSON(writer, 200, map[string]bool{"mapped": false})
}
func (server *Server) shutdownAll(writer http.ResponseWriter, request *http.Request) {
	// Cleanup is deliberately synchronous and ordered. The accepted response is
	// written from this handler while http.Server.Shutdown waits for it to finish.
	if err := server.Shutdown(request.Context()); err != nil {
		log.Printf("shutdown cleanup: %v", err)
	}
	writeJSON(writer, http.StatusAccepted, map[string]bool{"accepted": true})
}

// Shutdown tears down resources in dependency order: mapped drive, WebDAV,
// background services, then the Core HTTP server.
func (server *Server) Shutdown(ctx context.Context) error {
	err := server.stopDriveServices(ctx)
	if server.cancelCore != nil {
		server.cancelCore()
	}
	server.shutdownOnce.Do(func() { close(server.shutdown) })
	return err
}

func (server *Server) stopDriveServices(ctx context.Context) error {
	var cleanupErrors []error
	server.statusMutex.RLock()
	mapped := server.status.DriveMapped
	server.statusMutex.RUnlock()
	if mapped && server.mapper != nil {
		if err := server.mapper.Unmap(ctx, server.config.DriveLetter, server.webDAVURL()); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("unmap drive: %w", err))
		}
	}
	if server.driveService != nil {
		if err := server.driveService.Stop(ctx); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("stop WebDAV: %w", err))
		}
	}
	server.setDriveStatus(false, false)
	return errors.Join(cleanupErrors...)
}

func (server *Server) webDAVURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(server.config.WebDAVPort)
}

func (server *Server) setDriveStatus(running, mapped bool) {
	server.statusMutex.Lock()
	server.status.WebDAV = running
	server.status.DriveMapped = mapped
	value := server.status
	server.statusMutex.Unlock()
	server.Publish(Event{Type: "drive.status.changed", Data: value})
}

func (server *Server) clearTasks(writer http.ResponseWriter, _ *http.Request) {
	server.tasks.ClearHistory()
	writeJSON(writer, http.StatusOK, map[string]bool{"cleared": true})
}

func (server *Server) deleteTask(writer http.ResponseWriter, request *http.Request) {
	if err := server.tasks.Delete(request.PathValue("id")); err != nil {
		writeJSON(writer, http.StatusNotFound, ErrorResponse{Code: "task_not_found", Message: err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"deleted": true})
}

func (server *Server) reloadConfig(writer http.ResponseWriter, _ *http.Request) {
	if server.configPath == "" {
		writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "config_path_unset", Message: "config path not configured"})
		return
	}
	value, err := config.Load(server.configPath)
	if err != nil {
		log.Printf("reload config: %v", err)
		writeJSON(writer, http.StatusUnprocessableEntity, ErrorResponse{Code: "config_invalid", Message: err.Error()})
		return
	}
	server.config = value
	if server.discovery != nil {
		server.discovery.SetDeviceName(value.DeviceName)
	}
	if server.receiver != nil {
		server.receiver.SetReceiveDir(value.ReceiveDir)
	}
	log.Printf("config reloaded: device=%s receive=%s", value.DeviceName, value.ReceiveDir)
	writeJSON(writer, http.StatusOK, map[string]bool{"reloaded": true})
}

func (server *Server) cloudList(writer http.ResponseWriter, request *http.Request) {
	if server.cloud == nil {
		writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{Code: "cloud_disabled", Message: "cloud drive not configured"})
		return
	}
	prefix := request.URL.Query().Get("prefix")
	files, err := server.cloud.List(request.Context(), prefix)
	if err != nil {
		log.Printf("cloud list: %v", err)
		writeJSON(writer, http.StatusBadGateway, ErrorResponse{Code: "cloud_list_failed", Message: err.Error()})
		return
	}
	if files == nil {
		files = []cloud.File{}
	}
	writeJSON(writer, http.StatusOK, files)
}

func (server *Server) cloudUpload(writer http.ResponseWriter, request *http.Request) {
	if server.cloud == nil {
		writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{Code: "cloud_disabled", Message: "cloud drive not configured"})
		return
	}
	var input struct {
		FilePath string `json:"filePath"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil || input.FilePath == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "filePath required"})
		return
	}
	result, err := server.cloud.Upload(request.Context(), input.FilePath)
	if err != nil {
		log.Printf("cloud upload: %v", err)
		writeJSON(writer, http.StatusBadGateway, ErrorResponse{Code: "cloud_upload_failed", Message: err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (server *Server) cloudDownload(writer http.ResponseWriter, request *http.Request) {
	if server.cloud == nil {
		writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{Code: "cloud_disabled", Message: "cloud drive not configured"})
		return
	}
	var input struct {
		Key string `json:"key"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil || input.Key == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "key required"})
		return
	}
	url, err := server.cloud.DownloadURL(request.Context(), input.Key, 0)
	if err != nil {
		log.Printf("cloud download url: %v", err)
		writeJSON(writer, http.StatusBadGateway, ErrorResponse{Code: "cloud_download_failed", Message: err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"url": url})
}

func (server *Server) cloudDelete(writer http.ResponseWriter, request *http.Request) {
	if server.cloud == nil {
		writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{Code: "cloud_disabled", Message: "cloud drive not configured"})
		return
	}
	var input struct {
		Key string `json:"key"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil || input.Key == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "key required"})
		return
	}
	if err := server.cloud.Delete(request.Context(), input.Key); err != nil {
		log.Printf("cloud delete: %v", err)
		writeJSON(writer, http.StatusBadGateway, ErrorResponse{Code: "cloud_delete_failed", Message: err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"deleted": true})
}

func (server *Server) cloudShare(writer http.ResponseWriter, request *http.Request) {
	if server.cloud == nil {
		writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{Code: "cloud_disabled", Message: "cloud drive not configured"})
		return
	}
	var input struct {
		Key    string `json:"key"`
		Expiry int    `json:"expiryHours"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil || input.Key == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "key required"})
		return
	}
	expiry := time.Duration(input.Expiry) * time.Hour
	url, err := server.cloud.ShareLink(request.Context(), input.Key, expiry)
	if err != nil {
		log.Printf("cloud share: %v", err)
		writeJSON(writer, http.StatusBadGateway, ErrorResponse{Code: "cloud_share_failed", Message: err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"url": url})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func DecodeAddress(value string) (string, int, error) {
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	return strings.TrimSpace(host), port, err
}

var _ = fmt.Sprintf
