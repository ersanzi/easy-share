//go:build windows

package shellundo

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

var windowsShellNotify = windows.NewLazySystemDLL("shell32.dll").NewProc("SHChangeNotify")

// Start 由已运行的 EasyShare 在 CFAPI 挂载完成后调用；创建同用户/同会话 IPC 并同步菜单 root。
// callback 必须完成授权、目录树深层校验、临时 hydration、NativeRecycle 与云端删除/undo tracking。
// 关闭只停止接收新请求并移除自身会话菜单，不强行取消已经进入 callback 的文件操作。
func Start(root string, callback func([]string) error) (io.Closer, error) {
	executable, err := currentExecutable()
	if err != nil {
		return nil, err
	}
	identity, err := currentIdentity()
	if err != nil {
		return nil, err
	}
	return startHost(root, executable, identity, callback, userMenuStore{base: classesBase}, true)
}

type menuHost struct {
	cfg           menuConfig
	identity      userIdentity
	callback      func([]string) error
	store         menuStore
	handle        windows.Handle
	stop          chan struct{}
	done          chan struct{}
	busy          atomic.Bool
	once          sync.Once
	closeErr      error
	notify        bool
	mu            sync.Mutex
	backgroundErr error
}

func startHost(root, executable string, identity userIdentity, callback func([]string) error, store menuStore, notify bool) (*menuHost, error) {
	if callback == nil {
		return nil, fmt.Errorf("Shell 删除 callback 不能为空")
	}
	normalRoot, err := normalizeLocalPath(root)
	if err != nil {
		return nil, err
	}
	if len(normalRoot) == 3 {
		return nil, fmt.Errorf("Shell 云根不得为盘符根")
	}
	if err := inspectLocalPath(normalRoot, true); err != nil {
		return nil, fmt.Errorf("Shell 云根校验: %w", err)
	}
	normalExe, err := normalizeLocalPath(executable)
	if err != nil {
		return nil, err
	}
	if err := inspectLocalPath(normalExe, false); err != nil {
		return nil, fmt.Errorf("Shell 程序路径校验: %w", err)
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	cfg := menuConfig{Root: normalRoot, Executable: normalExe, Session: hex.EncodeToString(random[:])}
	security, err := pipeSecurity(identity)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(pipeName(identity, normalRoot, normalExe))
	if err != nil {
		return nil, err
	}
	// FIRST_PIPE_INSTANCE 拒绝第二套 host；远程连接同时由 PIPE_REJECT_REMOTE_CLIENTS 与 ACL 拒绝。
	handle, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 65536, 65536, 1000, security)
	runtime.KeepAlive(security)
	if err != nil {
		return nil, fmt.Errorf("创建 EasyShare Shell 同用户入口: %w", err)
	}
	host := &menuHost{cfg: cfg, identity: identity, callback: callback, store: store, handle: handle, stop: make(chan struct{}), done: make(chan struct{}), notify: notify}
	if err := installMenus(store, cfg); err != nil {
		return nil, errors.Join(err, windows.CloseHandle(handle))
	}
	go host.serve()
	if notify {
		notifyMenusChanged()
	}
	return host, nil
}

func (host *menuHost) recordBackground(err error) {
	if err != nil && !errors.Is(err, ErrClosed) {
		host.mu.Lock()
		host.backgroundErr = errors.Join(host.backgroundErr, err)
		host.mu.Unlock()
	}
}

func (host *menuHost) serve() {
	defer close(host.done)
	defer func() { host.recordBackground(windows.CloseHandle(host.handle)) }()
	for {
		if err := connectPipe(host.handle, host.stop); err != nil {
			host.recordBackground(err)
			return
		}
		stream := &pipeStream{handle: host.handle, deadline: time.Now().Add(DefaultForwardTimeout), stop: host.stop}
		err := verifyPipePeer(host.handle, false, host.identity, host.cfg.Executable)
		var request deleteRequest
		if err == nil {
			err = readFrame(stream, &request)
		}
		if err == nil {
			deadline := time.UnixMilli(request.Deadline)
			if deadline.Before(stream.deadline) {
				stream.deadline = deadline
			}
			err = host.dispatch(request)
		}
		response := deleteResponse{Version: protocolVersion}
		if err != nil {
			response.Error = err.Error()
		}
		// 写完不等于客户端读完；在原 deadline/stop 下确认 ACK 后才断开，绝不重试 callback。
		if responseErr := writeFrame(stream, response); responseErr != nil {
			if !errors.Is(responseErr, ErrTimeout) && !errors.Is(responseErr, ErrClosed) {
				host.recordBackground(fmt.Errorf("Shell 响应发送: %w", responseErr))
			}
		} else if ackErr := awaitResponseAck(stream, host.cfg.Session); ackErr != nil {
			host.recordBackground(ackErr) // EOF/对端离开保留为 transport error，不能当作删除失败重做。
		}
		if disconnectErr := windows.DisconnectNamedPipe(host.handle); disconnectErr != nil && !errors.Is(disconnectErr, windows.ERROR_PIPE_NOT_CONNECTED) {
			host.recordBackground(disconnectErr)
			return
		}
		select {
		case <-host.stop:
			return
		default:
		}
	}
}

func (host *menuHost) dispatch(request deleteRequest) error {
	if request.Version != protocolVersion || request.Session != host.cfg.Session || !strings.EqualFold(request.Root, host.cfg.Root) {
		return fmt.Errorf("Shell 请求版本、当前云根或会话不匹配")
	}
	deadline := time.UnixMilli(request.Deadline)
	if request.Deadline <= 0 || deadline.After(time.Now().Add(DefaultForwardTimeout+time.Second)) {
		return fmt.Errorf("Shell 请求 deadline 无效")
	}
	if err := checkPipeDeadline(deadline, host.stop); err != nil {
		return err
	}
	paths, err := ValidatePaths(host.cfg.Root, request.Paths)
	if err != nil {
		return err
	}
	if err := checkPipeDeadline(deadline, host.stop); err != nil {
		return err
	}
	if !host.busy.CompareAndSwap(false, true) {
		return fmt.Errorf("已有可撤销删除正在处理，请等待它结束")
	}
	done := make(chan error, 1)
	go func() {
		defer host.busy.Store(false)
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("Shell 删除 callback panic: %v", recovered)
			}
		}()
		done <- host.callback(append([]string(nil), paths...))
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return ErrTimeout
	case <-host.stop:
		return ErrClosed
	}
}

func (host *menuHost) Close() error {
	host.once.Do(func() {
		close(host.stop)
		host.closeErr = removeOwnedMenus(host.store, host.cfg)
		if host.notify {
			notifyMenusChanged()
		}
		<-host.done
		host.mu.Lock()
		host.closeErr = errors.Join(host.closeErr, host.backgroundErr)
		host.mu.Unlock()
	})
	return host.closeErr
}

// Forward 将选择发送给已运行的当前 EasyShare；不注册菜单，不初始化 App/Wails/Core。
// timeout 只限制 IPC 等待，不能回滚已进入原生 UI 或云端操作的 callback。
func Forward(paths []string, timeout time.Duration) error {
	if timeout <= 0 || timeout > DefaultForwardTimeout {
		timeout = DefaultForwardTimeout
	}
	executable, err := currentExecutable()
	if err != nil {
		return err
	}
	cfg, err := readMenuConfig(userMenuStore{base: classesBase}, executable)
	if err != nil {
		return err
	}
	identity, err := currentIdentity()
	if err != nil {
		return err
	}
	return forwardTo(cfg, identity, paths, time.Now().Add(timeout))
}

func forwardTo(cfg menuConfig, identity userIdentity, paths []string, deadline time.Time) error {
	paths, err := ValidatePaths(cfg.Root, paths)
	if err != nil {
		return err
	}
	handle, err := openPipe(pipeName(identity, cfg.Root, cfg.Executable), deadline)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	if err := verifyPipePeer(handle, true, identity, cfg.Executable); err != nil {
		return err
	}
	stream := &pipeStream{handle: handle, deadline: deadline}
	request := deleteRequest{Version: protocolVersion, Root: cfg.Root, Session: cfg.Session, Paths: paths, Deadline: deadline.UnixMilli()}
	if err := writeFrame(stream, request); err != nil {
		return fmt.Errorf("转发 Shell 删除: %w", err)
	}
	var response deleteResponse
	if err := readFrame(stream, &response); err != nil {
		return fmt.Errorf("等待 EasyShare Shell 删除结果: %w", err)
	}
	if response.Version != protocolVersion {
		return fmt.Errorf("Shell 响应版本不一致")
	}
	// 成功与失败响应都必须先确认已读完；ACK 也使用原来的有界 stream。
	if err := writeFrame(stream, deleteAck{Version: protocolVersion, Session: cfg.Session}); err != nil {
		var resultErr error
		if response.Error != "" {
			resultErr = fmt.Errorf("EasyShare 可撤销删除: %s", response.Error)
		}
		return errors.Join(resultErr, fmt.Errorf("Shell 结果已收到但确认发送失败，不能重复删除: %w", err))
	}
	if response.Error != "" {
		return fmt.Errorf("EasyShare 可撤销删除: %s", response.Error)
	}
	return nil
}
