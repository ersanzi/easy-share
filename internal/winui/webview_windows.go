//go:build windows

package winui

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// go-webview2 v1.0.22 未导出 Controller.Close / Environment.Release；只补 Win32 COM ABI
// 固定的两个入口，不复制框架或访问 Chromium 私有字段。必须在创建线程调用。
type comObject struct{ VTable *[26]uintptr }

// ReleaseCOMInterface 仅可用于 QueryInterface/Get... 返回的 COM 接口，不可用于 Go callback。
func ReleaseCOMInterface[T any](object *T) {
	if object == nil {
		return
	}
	com := (*comObject)(unsafe.Pointer(object))
	syscall.SyscallN(com.VTable[2], uintptr(unsafe.Pointer(object)))
}

func CloseWebViewController(controller *edge.ICoreWebView2Controller) error {
	if controller == nil {
		return nil
	}
	com := (*comObject)(unsafe.Pointer(controller))
	hr, _, _ := syscall.SyscallN(com.VTable[24], uintptr(unsafe.Pointer(controller)))
	if int32(hr) < 0 {
		return fmt.Errorf("WebView2 Controller.Close: %w", syscall.Errno(hr))
	}
	return nil
}

// 上游 PutShouldDetectMonitorScaleChanges 把 BOOL 错传为指针；原生 ABI 是值传递。
// 这里开启本窗口自动缩放，不改变 Wails 进程 DPI 上下文。
func WebViewDetectMonitorScale(controller *edge.ICoreWebView2Controller3) error {
	if controller == nil {
		return nil
	}
	hr, _, _ := controller.Vtbl.PutShouldDetectMonitorScaleChanges.Call(uintptr(unsafe.Pointer(controller)), 1)
	if windows.Handle(hr) != windows.S_OK {
		return fmt.Errorf("WebView2 monitor scale: %w", syscall.Errno(hr))
	}
	return nil
}
