package drive

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/webdav"
)

// CloudService 通过 WebDAV 协议将云端文件（S3 后端）暴露给 Windows 资源管理器。
// 仅监听 127.0.0.1（回环地址），无需认证——只有本机进程可以访问。
type CloudService struct {
	fs       webdav.FileSystem
	mutex    sync.RWMutex
	server   *http.Server
	listener net.Listener
}

func NewCloudService(fs webdav.FileSystem) *CloudService {
	return &CloudService{fs: fs}
}

func (service *CloudService) Start(port int) error {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if service.server != nil {
		return errors.New("cloud WebDAV already running")
	}
	handler := &webdav.Handler{Prefix: "/", FileSystem: service.fs, LockSystem: webdav.NewMemLS()}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	service.listener, service.server = listener, server
	go func() { _ = server.Serve(listener) }()
	return nil
}

func (service *CloudService) Stop(ctx context.Context) error {
	service.mutex.Lock()
	server := service.server
	service.server = nil
	service.listener = nil
	service.mutex.Unlock()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

func (service *CloudService) Running() bool {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	return service.server != nil
}

func (service *CloudService) Addr() string {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	if service.listener == nil {
		return ""
	}
	return service.listener.Addr().String()
}
