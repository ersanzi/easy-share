package drive

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/webdav"
)

type Service struct {
	root, username, password string
	mutex                    sync.RWMutex
	server                   *http.Server
	listener                 net.Listener
}

func NewService(root, username, password string) *Service {
	return &Service{root: root, username: username, password: password}
}

func (service *Service) Start(port int) error {
	if err := os.MkdirAll(service.root, 0o755); err != nil {
		return err
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if service.server != nil {
		return errors.New("WebDAV already running")
	}
	authenticator, err := newDigestAuthenticator(service.username, service.password)
	if err != nil {
		return err
	}
	handler := &webdav.Handler{Prefix: "/", FileSystem: webdav.Dir(service.root), LockSystem: webdav.NewMemLS()}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	// Windows blocks Basic authentication over plain HTTP by default
	// (WebClient BasicAuthLevel=1). Digest keeps loopback WebDAV authenticated
	// without requiring an administrator-only registry change.
	server := &http.Server{Handler: authenticator.handler(handler), ReadHeaderTimeout: 5 * time.Second}
	service.listener, service.server = listener, server
	go func() { _ = server.Serve(listener) }()
	return nil
}

func (service *Service) Stop(ctx context.Context) error {
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
func (service *Service) Running() bool {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	return service.server != nil
}
func (service *Service) Addr() string {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	if service.listener == nil {
		return ""
	}
	return service.listener.Addr().String()
}
