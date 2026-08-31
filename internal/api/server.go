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

	"easyshare/internal/config"
	"easyshare/internal/discovery"
	"easyshare/internal/knowledge"
	"easyshare/internal/task"
	"easyshare/internal/transfer"
	"easyshare/internal/version"
	"github.com/coder/websocket"
)

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Status struct {
	Core      bool `json:"core"`
	Discovery bool `json:"discovery"`
	Receiver  bool `json:"receiver"`
	WebDAV    bool `json:"webdav"`
	// CloudEnabled 由桌面端按「已登录 + 控制面可达」判定后覆盖：
	// Core 不再直连对象存储，无从得知个人云盘是否可用。
	CloudEnabled bool `json:"cloudEnabled"`
}

type driveService interface {
	Start(int) error
	Stop(context.Context) error
	Running() bool
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
	knowledge    *knowledge.Service
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
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "version": version.Version, "deviceId": server.config.DeviceID, "proof": hex.EncodeToString(mac.Sum(nil))})
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
	mux.Handle("POST /api/tasks", server.auth(http.HandlerFunc(server.createTask)))
	mux.Handle("PATCH /api/tasks/{id}", server.auth(http.HandlerFunc(server.patchTask)))
	mux.Handle("GET /api/events", server.auth(http.HandlerFunc(server.events)))
	mux.Handle("POST /api/shutdown", server.auth(http.HandlerFunc(server.shutdownAll)))
	mux.Handle("POST /api/drive/start", server.auth(http.HandlerFunc(server.startDrive)))
	mux.Handle("POST /api/drive/stop", server.auth(http.HandlerFunc(server.stopDrive)))
	mux.Handle("POST /api/transfers", server.auth(http.HandlerFunc(server.sendTransfer)))
	mux.Handle("POST /api/transfers/{id}/accept", server.auth(http.HandlerFunc(server.acceptTransfer)))
	mux.Handle("POST /api/transfers/{id}/reject", server.auth(http.HandlerFunc(server.rejectTransfer)))
	mux.Handle("POST /api/config/reload", server.auth(http.HandlerFunc(server.reloadConfig)))
	mux.Handle("GET /api/knowledge/status", server.auth(http.HandlerFunc(server.knowledgeStatus)))
	mux.Handle("POST /api/knowledge/login", server.auth(http.HandlerFunc(server.knowledgeLogin)))
	mux.Handle("POST /api/knowledge/logout", server.auth(http.HandlerFunc(server.knowledgeLogout)))
	mux.Handle("POST /api/knowledge/health", server.auth(http.HandlerFunc(server.knowledgeHealth)))
	mux.Handle("POST /api/knowledge/query", server.auth(http.HandlerFunc(server.knowledgeQuery)))
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
func (server *Server) ConfigureDrive(service driveService) {
	server.driveService = service
}
func (server *Server) ConfigureShutdown(cancel context.CancelFunc) { server.cancelCore = cancel }
func (server *Server) ConfigureConfigPath(path string)             { server.configPath = path }
func (server *Server) ConfigureDiscovery(service *discovery.Service) {
	server.discovery = service
	server.peers = service.Peers
}
func (server *Server) ConfigureTransfer(receiver *transfer.Receiver) {
	server.receiver = receiver
}
// ConfigureKnowledge 注入知识网关运行态（会话由 knowledge.json 持久化）。
func (server *Server) ConfigureKnowledge(service *knowledge.Service) {
	server.knowledge = service
}

// StartLANDrive 启动局域网共享 WebDAV 服务。
// 由 Core 启动时调用，使"此电脑"中的 EasyShare 共享入口始终可用。
func (server *Server) StartLANDrive() {
	if server.driveService == nil {
		return
	}
	if err := server.driveService.Start(server.config.WebDAVPort); err != nil && !server.driveService.Running() {
		log.Printf("start LAN WebDAV: %v", err)
		return
	}
	server.setDriveStatus(true)
	log.Printf("LAN WebDAV ready at %s", server.webDAVURL())
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
		BatchID  string `json:"batchId"`
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
		err := transfer.Send(context.Background(), transfer.SendRequest{Address: net.JoinHostPort(peer.IP, strconv.Itoa(peer.TransferPort)), FilePath: input.FilePath, DeviceID: server.config.DeviceID, DeviceName: server.config.DeviceName, PeerName: peer.DeviceName, BatchID: input.BatchID, Tasks: server.tasks, OnUpdate: func(value task.Task) { server.Publish(Event{Type: "transfer.updated", Data: value}) }})
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
	server.setDriveStatus(true)
	log.Printf("WebDAV ready at %s", server.webDAVURL())
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
func (server *Server) shutdownAll(writer http.ResponseWriter, request *http.Request) {
	// Cleanup is deliberately synchronous and ordered. The accepted response is
	// written from this handler while http.Server.Shutdown waits for it to finish.
	if err := server.Shutdown(request.Context()); err != nil {
		log.Printf("shutdown cleanup: %v", err)
	}
	writeJSON(writer, http.StatusAccepted, map[string]bool{"accepted": true})
}

// Shutdown tears down resources in dependency order: WebDAV services,
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
	if server.driveService != nil {
		if err := server.driveService.Stop(ctx); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("stop WebDAV: %w", err))
		}
	}
	server.setDriveStatus(false)
	return errors.Join(cleanupErrors...)
}

func (server *Server) webDAVURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(server.config.WebDAVPort)
}

func (server *Server) setDriveStatus(running bool) {
	server.statusMutex.Lock()
	server.status.WebDAV = running
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

// createTask 由桌面端调用，在 Core 统一任务存储中注册云盘上传/下载等外部任务。
func (server *Server) createTask(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Kind       task.Kind      `json:"kind"`
		FileName   string         `json:"fileName"`
		Direction  task.Direction `json:"direction"`
		Peer       string         `json:"peer"`
		BatchID    string         `json:"batchId"`
		TotalBytes int64          `json:"totalBytes"`
		Status     task.Status    `json:"status"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "invalid JSON"})
		return
	}
	if input.Status == "" {
		input.Status = task.StatusQueued
	}
	created, err := server.tasks.Create(task.Task{
		Kind:       input.Kind,
		FileName:   input.FileName,
		Direction:  input.Direction,
		Peer:       input.Peer,
		BatchID:    input.BatchID,
		TotalBytes: input.TotalBytes,
		Status:     input.Status,
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "create_failed", Message: err.Error()})
		return
	}
	server.Publish(Event{Type: "transfer.updated", Data: created})
	writeJSON(writer, http.StatusCreated, created)
}

// patchTask 由桌面端调用，更新外部任务的进度/状态（云盘上传进度等）。
func (server *Server) patchTask(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	var input struct {
		TransferredBytes *int64       `json:"transferredBytes"`
		Speed            *float64     `json:"speed"`
		Status           *task.Status `json:"status"`
		Error            *string      `json:"error"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "invalid JSON"})
		return
	}
	updated, err := server.tasks.Update(id, func(value *task.Task) error {
		if input.TransferredBytes != nil {
			value.TransferredBytes = *input.TransferredBytes
		}
		if input.Speed != nil {
			value.Speed = *input.Speed
		}
		if input.Status != nil {
			value.Status = *input.Status
		}
		if input.Error != nil {
			value.Error = *input.Error
		}
		return nil
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "update_failed", Message: err.Error()})
		return
	}
	server.Publish(Event{Type: "transfer.updated", Data: updated})
	writeJSON(writer, http.StatusOK, updated)
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
