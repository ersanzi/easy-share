//go:build windows

package shellundo

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func openAckRequest(t *testing.T, host *menuHost, path string, timeout time.Duration) *pipeStream {
	t.Helper()
	deadline := time.Now().Add(timeout)
	handle, err := openPipe(pipeName(host.identity, host.cfg.Root, host.cfg.Executable), deadline)
	if err != nil {
		t.Fatal(err)
	}
	stream := &pipeStream{handle: handle, deadline: deadline}
	if err := writeFrame(stream, deleteRequest{Version: protocolVersion, Root: host.cfg.Root, Session: host.cfg.Session, Paths: []string{path}, Deadline: deadline.UnixMilli()}); err != nil {
		_ = windows.CloseHandle(handle)
		t.Fatal(err)
	}
	return stream
}

func TestPipeResponseWaitsForAckBeforeDisconnect(t *testing.T) {
	for _, failure := range []string{"", "callback result sentinel"} {
		t.Run(map[bool]string{true: "failure", false: "success"}[failure != ""], func(t *testing.T) {
			var calls atomic.Int32
			host, path := isolatedHost(t, func([]string) error {
				calls.Add(1)
				if failure != "" {
					return errors.New(failure)
				}
				return nil
			})
			stream := openAckRequest(t, host, path, 2*time.Second)
			defer closeTestHandle(t, stream.handle)
			// 故意让响应先留在 pipe buffer；原来的立即 Disconnect 会使这里读取 EOF。
			time.Sleep(100 * time.Millisecond)
			var response deleteResponse
			if err := readFrame(stream, &response); err != nil || response.Version != protocolVersion || response.Error != failure {
				t.Fatalf("等待客户端读取的响应丢失: %+v / %v", response, err)
			}
			if err := writeFrame(stream, deleteAck{Version: protocolVersion, Session: host.cfg.Session}); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("ACK 不得触发第二次 callback: %d", calls.Load())
			}
		})
	}
}

func TestPipeMissingAckIsTransportErrorWithoutCallbackRetry(t *testing.T) {
	root := canonicalTestDirectory(t)
	path := filepath.Join(root, "selected.txt")
	if err := os.WriteFile(path, []byte("ACK test does not delete"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := currentIdentity()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	host, err := startHost(root, canonicalTestPath(t, executable), identity, func([]string) error { calls.Add(1); return nil }, newMemoryMenuStore(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeErr := host.Close()
		if !errors.Is(closeErr, io.EOF) || !strings.Contains(closeErr.Error(), "未收到确认") {
			t.Errorf("对端离开必须保留未确认响应的 transport error: %v", closeErr)
		}
	})
	stream := openAckRequest(t, host, path, 2*time.Second)
	var response deleteResponse
	if err := readFrame(stream, &response); err != nil || response.Error != "" {
		_ = windows.CloseHandle(stream.handle)
		t.Fatalf("未 ACK 前已读完响应: %+v / %v", response, err)
	}
	closeTestHandle(t, stream.handle) // 不确认结果，模拟已读完后菜单对端离开。
	deadline := time.Now().Add(time.Second)
	for {
		host.mu.Lock()
		backgroundErr := host.backgroundErr
		host.mu.Unlock()
		if errors.Is(backgroundErr, io.EOF) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("宿主未记录 ACK EOF: %v", backgroundErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("未确认响应不能重试 callback: %d", calls.Load())
	}
	// 下一次显式请求可以继续服务，但不能隐式重做第一次请求。
	if err := forwardTo(host.cfg, host.identity, []string{path}, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("只有显式下一次请求才增加 callback: %d", calls.Load())
	}
}
