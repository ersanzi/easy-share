package spacedav

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

// Service 把一个空间挂成本机 WebDAV 端点。
//
// 只监听 127.0.0.1：端点自身不做认证，靠「仅本机可达」+「每个操作都带登录态回控制面」
// 两层。真正的授权判定在控制面，本进程不持任何对象存储凭据。
type Service struct {
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	port     int
}

// NewService 创建一个未启动的空间 WebDAV 服务。
func NewService() *Service {
	return &Service{}
}

// Start 在指定端口挂起某空间。重复调用先停后起，用于换账号或权限变更后重挂。
func (s *Service) Start(port int, fs *FS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		s.stopLocked(context.Background())
	}
	handler := &webdav.Handler{
		Prefix:     "/",
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	s.listener, s.server, s.port = listener, server, port
	go func() { _ = server.Serve(listener) }()
	return nil
}

// Stop 停止服务。未启动时是空操作。
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked(ctx)
}

func (s *Service) stopLocked(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	server := s.server
	s.server, s.listener, s.port = nil, nil, 0
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := server.Shutdown(shutdownCtx)
	if errors.Is(err, context.DeadlineExceeded) {
		return server.Close()
	}
	return err
}

// Running 报告服务是否在跑。
func (s *Service) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.server != nil
}

// Port 返回当前监听端口，未启动时为 0。
func (s *Service) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}
