//go:build windows

package shellundo

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

// 全部管道 I/O 使用 OVERLAPPED：有 deadline 和关闭信号，绝不留下阻塞菜单进程。
type pipeStream struct {
	handle   windows.Handle
	deadline time.Time
	stop     <-chan struct{}
}

func (stream *pipeStream) Read(data []byte) (int, error)  { return stream.transfer(data, false) }
func (stream *pipeStream) Write(data []byte) (int, error) { return stream.transfer(data, true) }

func (stream *pipeStream) transfer(data []byte, write bool) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if err := checkPipeDeadline(stream.deadline, stream.stop); err != nil {
		return 0, err
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, fmt.Errorf("创建管道 I/O event: %w", err)
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	var done uint32
	if write {
		err = windows.WriteFile(stream.handle, data, &done, &overlapped)
	} else {
		err = windows.ReadFile(stream.handle, data, &done, &overlapped)
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		done, err = waitPipeOperation(stream.handle, &overlapped, stream.deadline, stream.stop)
	}
	runtime.KeepAlive(data)
	if !write && (errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED)) {
		return int(done), io.EOF
	}
	if err != nil {
		return int(done), err
	}
	if !write && done == 0 {
		return 0, io.EOF
	}
	return int(done), nil
}

func connectPipe(handle windows.Handle, stop <-chan struct{}) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return fmt.Errorf("创建管道连接 event: %w", err)
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(handle, &overlapped)
	if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return nil
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		_, err = waitPipeOperation(handle, &overlapped, time.Time{}, stop)
	}
	return err
}

func checkPipeDeadline(deadline time.Time, stop <-chan struct{}) error {
	if stop != nil {
		select {
		case <-stop:
			return ErrClosed
		default:
		}
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return ErrTimeout
	}
	return nil
}

func waitPipeOperation(handle windows.Handle, overlapped *windows.Overlapped, deadline time.Time, stop <-chan struct{}) (uint32, error) {
	for {
		if err := checkPipeDeadline(deadline, stop); err != nil {
			return 0, cancelPipeOperation(handle, overlapped, err)
		}
		wait := 100 * time.Millisecond
		if !deadline.IsZero() && time.Until(deadline) < wait {
			wait = time.Until(deadline)
		}
		milliseconds := uint32((wait + time.Millisecond - 1) / time.Millisecond)
		status, err := windows.WaitForSingleObject(overlapped.HEvent, milliseconds)
		if err != nil {
			return 0, cancelPipeOperation(handle, overlapped, err)
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		if status != windows.WAIT_OBJECT_0 {
			return 0, cancelPipeOperation(handle, overlapped, fmt.Errorf("管道 wait 状态无效: %d", status))
		}
		var done uint32
		err = windows.GetOverlappedResult(handle, overlapped, &done, false)
		return done, err
	}
}

func cancelPipeOperation(handle windows.Handle, overlapped *windows.Overlapped, cause error) error {
	cancelErr := windows.CancelIoEx(handle, overlapped)
	if errors.Is(cancelErr, windows.ERROR_NOT_FOUND) {
		cancelErr = nil
	}
	// OVERLAPPED 与 buffer 在真正完成取消之前必须继续存活，防止内核访问已释放内存。
	var done uint32
	finishErr := windows.GetOverlappedResult(handle, overlapped, &done, true)
	if errors.Is(finishErr, windows.ERROR_OPERATION_ABORTED) {
		finishErr = nil
	}
	return errors.Join(cause, cancelErr, finishErr)
}

func openPipe(name string, deadline time.Time) (windows.Handle, error) {
	text, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	for {
		if err := checkPipeDeadline(deadline, nil); err != nil {
			return 0, err
		}
		handle, err := windows.CreateFile(text, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			return handle, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return 0, fmt.Errorf("已运行 EasyShare 的 Shell 入口不可用: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
