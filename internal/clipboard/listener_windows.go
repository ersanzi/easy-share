//go:build windows

package clipboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"

	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 遵循项目既有风格：LazyDLL + NewProc + .Call（见 internal/winui/win32_windows.go）。
// 本文件覆盖剪切板监听所需的 user32/kernel32/shell32 API 面。
var (
	clipUser32   = windows.NewLazySystemDLL("user32.dll")
	clipKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	clipShell32  = windows.NewLazySystemDLL("shell32.dll")

	procOpenClipboard        = clipUser32.NewProc("OpenClipboard")
	procCloseClipboard       = clipUser32.NewProc("CloseClipboard")
	procGetClipboardData     = clipUser32.NewProc("GetClipboardData")
	procSetClipboardData     = clipUser32.NewProc("SetClipboardData")
	procEmptyClipboard       = clipUser32.NewProc("EmptyClipboard")
	procEnumClipboardFormats = clipUser32.NewProc("EnumClipboardFormats")
	procGetClipboardFormatNm = clipUser32.NewProc("GetClipboardFormatNameW")
	procIsClipFormatAvail    = clipUser32.NewProc("IsClipboardFormatAvailable")
	procAddClipFmtListener   = clipUser32.NewProc("AddClipboardFormatListener")
	procRemoveClipFmtListen  = clipUser32.NewProc("RemoveClipboardFormatListener")
	procGetForegroundWindow  = clipUser32.NewProc("GetForegroundWindow")
	procGetWndThreadProcId   = clipUser32.NewProc("GetWindowThreadProcessId")

	procGlobalLock              = clipKernel32.NewProc("GlobalLock")
	procGlobalUnlock            = clipKernel32.NewProc("GlobalUnlock")
	procGlobalAlloc             = clipKernel32.NewProc("GlobalAlloc")
	procGlobalSize              = clipKernel32.NewProc("GlobalSize")
	proclstrlenW                = clipKernel32.NewProc("lstrlenW")
	procOpenProcess             = clipKernel32.NewProc("OpenProcess")
	procQueryFullProcImageNameW = clipKernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle             = clipKernel32.NewProc("CloseHandle")

	procDragQueryFileW = clipShell32.NewProc("DragQueryFileW")
)

// 剪贴板相关常量。
const (
	cfDIB          = 8
	cfUnicodeText  = 13
	cfHDROP        = 15
	cfDIBV5        = 17
	wmClipbdUpdate = 0x031D
	hwndMessage    = ^uintptr(2) // (HWND)-3，message-only 窗口的父窗口
	gmemMoveable   = 0x0002
	// 密码管理器等敏感来源用这些注册格式名请求剪贴板工具不要记录。
	// 遵循 Windows 剪贴板历史的官方约定。
	fmtExcludeFromMonitor = "ExcludeClipboardContentFromMonitorProcessing"
	fmtViewerIgnore       = "ClipboardViewerIgnore"
	fmtCanIncludeHistory  = "CanIncludeInClipboardHistory"
)

var (
	clipWndClass     = "EasyShareClipboardListener"
	clipWndClassOnce sync.Once
	clipWndClassErr  error
	clipListenerHwnd uintptr // 监听窗口句柄（只在监听线程创建/销毁，Stop 时跨线程只读投递消息）
)

// startListener 起独立线程跑 message-only 窗口与消息循环。
// 可重入调用（插件卸载后重装会再次启动）；类注册与全局实例在首次时初始化。
func (s *Service) startListener() error {
	ready := make(chan error, 1)
	go s.runListener(ready)
	return <-ready
}

// runListener 监听线程主体：注册类 → message-only 窗口 → 注册剪贴板监听 → 消息循环。
func (s *Service) runListener(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	clipWndClassOnce.Do(func() {
		wc := struct {
			Size       uint32
			Style      uint32
			WndProc    uintptr
			ClsExtra   int32
			WndExtra   int32
			Instance   windows.Handle
			Icon       windows.Handle
			Cursor     windows.Handle
			Background windows.Handle
			MenuName   *uint16
			ClassName  *uint16
			IconSm     windows.Handle
		}{
			WndProc:  windows.NewCallback(clipWndProc),
			Instance: winuiGetModuleHandle(),
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		name, err := syscall.UTF16PtrFromString(clipWndClass)
		if err != nil {
			clipWndClassErr = err
			return
		}
		wc.ClassName = name
		atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			clipWndClassErr = fmt.Errorf("RegisterClassExW: %w", callErr)
		}
	})
	if clipWndClassErr != nil {
		ready <- clipWndClassErr
		return
	}

	// 窗口过程回调需要拿到 Service 实例：包级变量保存当前实例
	// （桌面端进程内只有一个剪切板服务）。
	clipServiceMu.Lock()
	clipService = s
	clipServiceMu.Unlock()

	className, _ := syscall.UTF16PtrFromString(clipWndClass)
	title, _ := syscall.UTF16PtrFromString("EasyShareClipboard")
	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0, // message-only 窗口无样式
		0, 0, 0, 0,
		hwndMessage, 0, uintptr(winuiGetModuleHandle()), 0,
	)
	if hwnd == 0 {
		ready <- fmt.Errorf("创建剪切板监听窗口: %w", callErr)
		return
	}
	clipListenerHwnd = hwnd

	if r, _, _ := procAddClipFmtListener.Call(hwnd); r == 0 {
		_, _, _ = procDestroyWindow.Call(hwnd)
		clipListenerHwnd = 0
		ready <- fmt.Errorf("AddClipboardFormatListener 失败")
		return
	}
	ready <- nil

	winuiRunMessageLoop()

	_, _, _ = procRemoveClipFmtListen.Call(hwnd)
	clipListenerHwnd = 0
}

// stopListener 投递 WM_CLOSE 让监听线程退出消息循环并销毁窗口。
func (s *Service) stopListener() {
	if hwnd := clipListenerHwnd; hwnd != 0 {
		_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
}

var (
	clipServiceMu sync.RWMutex
	clipService   *Service
	// 以下 procs 复用 internal/winui 已有的 DLL 封装风格，但为避免跨包导出面扩张，
	// 这里单独声明（user32/kernel32 是进程级单例 DLL，重复 NewLazySystemDLL 无副作用）。
	procRegisterClassExW = clipUser32.NewProc("RegisterClassExW")
	procCreateWindowExW  = clipUser32.NewProc("CreateWindowExW")
	procDestroyWindow    = clipUser32.NewProc("DestroyWindow")
	procPostMessageW     = clipUser32.NewProc("PostMessageW")
	wmClose              = uintptr(0x0010)
)

// clipWndProc 剪贴板监听窗口过程。
func clipWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch uintptr(msg) {
	case uintptr(wmClipbdUpdate):
		clipServiceMu.RLock()
		s := clipService
		clipServiceMu.RUnlock()
		if s != nil {
			s.handleClipboardUpdate()
		}
		return 0
	case uintptr(wmClose):
		_, _, _ = procDestroyWindow.Call(hwnd)
		winuiPostQuitMessage(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// handleClipboardUpdate 读取当前剪贴板内容并记录。运行在监听线程上。
func (s *Service) handleClipboardUpdate() {
	if !openClipboardRetry(clipListenerHwnd, 6, 15*time.Millisecond) {
		return // 剪贴板被其他进程长时间占用，放弃本条（下次复制仍会触发）
	}
	defer procCloseClipboard.Call()

	if excludedByFormat() {
		return // 敏感来源（密码管理器等）请求不记录
	}
	source := foregroundProcessName()

	switch {
	case formatAvailable(cfHDROP):
		if paths := readFilePaths(); len(paths) > 0 {
			hash := hashOf(stringsJoin(paths, "\n"))
			s.record(Entry{
				ID: NewID(), Kind: KindFiles, Files: paths,
				Size: int64(len(paths)), Source: source, CreatedAt: nowMillis(), Hash: hash,
			})
		}
	case formatAvailable(cfDIBV5) || formatAvailable(cfDIB):
		var dib []byte
		if formatAvailable(cfDIBV5) {
			dib = readGlobalBytes(cfDIBV5)
		} else {
			dib = readGlobalBytes(cfDIB)
		}
		if img, width, height, err := dibToPNG(dib); err == nil {
			sum := sha256.Sum256(img)
			s.record(Entry{
				ID: NewID(), Kind: KindImage, Width: width, Height: height,
				Size: int64(len(img)), Source: source, CreatedAt: nowMillis(),
				Hash: hex.EncodeToString(sum[:8]), imagePNG: img,
			})
		}
	case formatAvailable(cfUnicodeText):
		if text := readClipboardText(); text != "" {
			if len(text) > maxTextBytes {
				return // 程序性超大文本不记录
			}
			sum := sha256.Sum256([]byte(text))
			s.record(Entry{
				ID: NewID(), Kind: KindText, Text: text,
				Size: int64(len(text)), Source: source, CreatedAt: nowMillis(),
				Hash: hex.EncodeToString(sum[:8]),
			})
		}
	}
}

// --- 剪贴板读取辅助 ---

func openClipboardRetry(hwnd uintptr, attempts int, wait time.Duration) bool {
	for i := 0; i < attempts; i++ {
		if r, _, _ := procOpenClipboard.Call(hwnd); r != 0 {
			return true
		}
		time.Sleep(wait)
	}
	return false
}

func formatAvailable(format uint32) bool {
	r, _, _ := procIsClipFormatAvail.Call(uintptr(format))
	return r != 0
}

// excludedByFormat 检查敏感排除标记（须已 OpenClipboard）。
// 约定：ExcludeClipboardContentFromMonitorProcessing / ClipboardViewerIgnore 存在即排除；
// CanIncludeInClipboardHistory 存在且值为 0 时排除（与 Windows 剪贴板历史行为一致）。
func excludedByFormat() bool {
	for format := enumClipboardFormats(0); format != 0; format = enumClipboardFormats(format) {
		if format <= 14 { // 标准格式没有名字，跳过
			continue
		}
		name := formatName(format)
		if name == "" {
			continue
		}
		switch name {
		case fmtExcludeFromMonitor, fmtViewerIgnore:
			return true
		case fmtCanIncludeHistory:
			// 值为 4 字节 DWORD；0 表示请求不进入历史。
			if data := readGlobalBytesByName(format); len(data) >= 4 && binary.LittleEndian.Uint32(data) == 0 {
				return true
			}
		}
	}
	return false
}

func enumClipboardFormats(prev uint32) uint32 {
	r, _, _ := procEnumClipboardFormats.Call(uintptr(prev))
	return uint32(r)
}

func formatName(format uint32) string {
	buf := make([]uint16, 128)
	r, _, _ := procGetClipboardFormatNm.Call(uintptr(format), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:r])
}

// readGlobalBytesByName 读取指定格式（按格式 ID）的原始字节。
func readGlobalBytesByName(format uint32) []byte {
	h, _, _ := procGetClipboardData.Call(uintptr(format))
	if h == 0 {
		return nil
	}
	return globalBytes(h)
}

// readGlobalBytes 读取指定标准格式剪贴板数据的原始字节。
func readGlobalBytes(format uint32) []byte {
	h, _, _ := procGetClipboardData.Call(uintptr(format))
	if h == 0 {
		return nil
	}
	return globalBytes(h)
}

func globalBytes(hMem uintptr) []byte {
	size, _, _ := procGlobalSize.Call(hMem)
	if size == 0 {
		return nil
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return nil
	}
	defer procGlobalUnlock.Call(hMem)
	out := make([]byte, size)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size))
	return out
}

// readClipboardText 读 CF_UNICODETEXT（须已 OpenClipboard）。
func readClipboardText() string {
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)
	n, _, _ := proclstrlenW.Call(ptr)
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), n))
}

// readFilePaths 读 CF_HDROP 文件列表（须已 OpenClipboard）。
func readFilePaths() []string {
	h, _, _ := procGetClipboardData.Call(cfHDROP)
	if h == 0 {
		return nil
	}
	count, _, _ := procDragQueryFileW.Call(h, ^uintptr(0), 0, 0)
	if count == 0 || count > maxFilePaths {
		return nil
	}
	paths := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		n, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
		if n == 0 {
			continue
		}
		buf := make([]uint16, n+1)
		if r, _, _ := procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); r != 0 {
			paths = append(paths, windows.UTF16ToString(buf[:r]))
		}
	}
	return paths
}

// foregroundProcessName 取当前前台窗口的进程名（exe 文件名），失败返回空串。
func foregroundProcessName() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	_, _, _ = procGetWndThreadProcId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	const processQueryLimitedInformation = 0x1000
	hProc, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if hProc == 0 {
		return ""
	}
	defer procCloseHandle.Call(hProc)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcImageNameW.Call(hProc, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	full := windows.UTF16ToString(buf[:size])
	return filepath.Base(full)
}

// --- DIB → PNG ---

// dibToPNG 把剪贴板 DIB（BITMAPINFOHEADER/BITMAPV5HEADER + 像素）编码为 PNG。
// 支持 32bpp BGRA 与 24bpp BGR；其他位深返回错误（跳过记录）。
func dibToPNG(dib []byte) (pngBytes []byte, width, height int, err error) {
	if len(dib) < 40 {
		return nil, 0, 0, fmt.Errorf("DIB 太短")
	}
	biSize := binary.LittleEndian.Uint32(dib[0:4])
	if biSize > uint32(len(dib)) {
		return nil, 0, 0, fmt.Errorf("DIB 头超出数据")
	}
	w := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	hRaw := int32(binary.LittleEndian.Uint32(dib[8:12]))
	bpp := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := binary.LittleEndian.Uint32(dib[16:20])
	clrUsed := binary.LittleEndian.Uint32(dib[32:36])
	if compression != 0 && compression != 3 { // BI_RGB / BI_BITFIELDS
		return nil, 0, 0, fmt.Errorf("不支持的压缩方式 %d", compression)
	}
	topDown := hRaw < 0
	h := int(hRaw)
	if topDown {
		h = -h
	}
	if w <= 0 || h <= 0 || w*h > 100_000_000 {
		return nil, 0, 0, fmt.Errorf("尺寸异常 %dx%d", w, h)
	}
	offset := int(biSize) + int(clrUsed)*4
	if biSize == 40 && compression == 3 {
		offset += 12 // BITMAPINFOHEADER + BI_BITFIELDS：头后带 3 个掩码
	}
	if bpp == 32 {
		if len(dib) < offset+w*h*4 {
			return nil, 0, 0, fmt.Errorf("32bpp 数据不足")
		}
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		hasAlpha := false
		for y := 0; y < h; y++ {
			srcY := y
			if !topDown {
				srcY = h - 1 - y // bottom-up 翻转
			}
			row := dib[offset+srcY*w*4:]
			for x := 0; x < w; x++ {
				b, g, r, a := row[x*4], row[x*4+1], row[x*4+2], row[x*4+3]
				if a != 0 {
					hasAlpha = true
				}
				img.SetNRGBA(x, y, colorNRGBA(r, g, b, a))
			}
		}
		if !hasAlpha {
			// 大量截图 32bpp 但 alpha 全 0：视为不透明，否则显示成透明黑块。
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					c := img.NRGBAAt(x, y)
					img.SetNRGBA(x, y, colorNRGBA(c.R, c.G, c.B, 255))
				}
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, 0, 0, err
		}
		return buf.Bytes(), w, h, nil
	}
	if bpp == 24 {
		if len(dib) < offset+w*h*3 {
			return nil, 0, 0, fmt.Errorf("24bpp 数据不足")
		}
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			srcY := y
			if !topDown {
				srcY = h - 1 - y
			}
			row := dib[offset+srcY*w*3:]
			for x := 0; x < w; x++ {
				b, g, r := row[x*3], row[x*3+1], row[x*3+2]
				img.SetNRGBA(x, y, colorNRGBA(r, g, b, 255))
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, 0, 0, err
		}
		return buf.Bytes(), w, h, nil
	}
	return nil, 0, 0, fmt.Errorf("不支持的位深 %d", bpp)
}

// --- 回写（clipboard.write 能力与历史条目重新复制共用）---

// Write 回写剪贴板（Windows 实现）。WriteRequest 定义在 service.go。
func (s *Service) Write(req WriteRequest) error {
	var hash string
	var err error
	switch req.Kind {
	case KindText:
		if req.Text == "" {
			return fmt.Errorf("文本为空")
		}
		hash = hashOf(req.Text)
		err = writeTextToClipboard(req.Text)
	case KindImage:
		data, readErr := os.ReadFile(s.ImagePath(req.ImageFile))
		if readErr != nil {
			return fmt.Errorf("读图片: %w", readErr)
		}
		sum := sha256.Sum256(data)
		hash = hex.EncodeToString(sum[:8])
		err = writePNGToClipboard(data)
	case KindFiles:
		if len(req.Files) == 0 {
			return fmt.Errorf("文件列表为空")
		}
		hash = hashOf(stringsJoin(req.Files, "\n"))
		err = writeFilesToClipboard(req.Files)
	default:
		return fmt.Errorf("未知回写类型 %q", req.Kind)
	}
	if err != nil {
		return err
	}
	s.markSelfWrite(hash)
	return nil
}

func writeTextToClipboard(text string) error {
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	size := uintptr(len(utf16)) * 2
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("GlobalAlloc 失败")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return fmt.Errorf("GlobalLock 失败")
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(utf16)), utf16)
	procGlobalUnlock.Call(h)
	return withEmptyClipboard(func() error {
		r, _, callErr := procSetClipboardData.Call(cfUnicodeText, h)
		if r == 0 {
			return fmt.Errorf("SetClipboardData: %w", callErr)
		}
		return nil // 成功后系统接管内存，不要 GlobalFree
	})
}

// writePNGToClipboard PNG → 32bpp bottom-up DIB（BITMAPV5HEADER + BGRA），写 CF_DIBV5。
func writePNGToClipboard(pngBytes []byte) error {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return fmt.Errorf("解码 PNG: %w", err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	header := make([]byte, 124)
	binary.LittleEndian.PutUint32(header[0:], 124)            // bV5Size
	binary.LittleEndian.PutUint32(header[4:], uint32(w))      // bV5Width
	binary.LittleEndian.PutUint32(header[8:], uint32(h))      // bV5Height（正=bottom-up）
	binary.LittleEndian.PutUint16(header[12:], 1)             // bV5Planes
	binary.LittleEndian.PutUint16(header[14:], 32)            // bV5BitCount
	binary.LittleEndian.PutUint32(header[16:], 3)             // BI_BITFIELDS
	binary.LittleEndian.PutUint32(header[20:], uint32(w*h*4)) // bV5SizeImage
	binary.LittleEndian.PutUint32(header[40:], 0x00FF0000)    // RedMask
	binary.LittleEndian.PutUint32(header[44:], 0x0000FF00)    // GreenMask
	binary.LittleEndian.PutUint32(header[48:], 0x000000FF)    // BlueMask
	binary.LittleEndian.PutUint32(header[52:], 0xFF000000)    // AlphaMask

	pixels := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		dstY := h - 1 - y // bottom-up：最后一行写在最前
		for x := 0; x < w; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			off := (dstY*w + x) * 4
			pixels[off] = byte(b >> 8)
			pixels[off+1] = byte(g >> 8)
			pixels[off+2] = byte(r >> 8)
			pixels[off+3] = byte(a >> 8)
		}
	}
	payload := append(header, pixels...)
	total := uintptr(len(payload))
	hg, _, _ := procGlobalAlloc.Call(gmemMoveable, total)
	if hg == 0 {
		return fmt.Errorf("GlobalAlloc 失败")
	}
	ptr, _, _ := procGlobalLock.Call(hg)
	if ptr == 0 {
		return fmt.Errorf("GlobalLock 失败")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(payload)), payload)
	procGlobalUnlock.Call(hg)
	return withEmptyClipboard(func() error {
		r, _, callErr := procSetClipboardData.Call(cfDIBV5, hg)
		if r == 0 {
			return fmt.Errorf("SetClipboardData: %w", callErr)
		}
		return nil
	})
}

// writeFilesToClipboard 构造 DROPFILES + 双零结尾宽字符串列表，写 CF_HDROP。
func writeFilesToClipboard(paths []string) error {
	list := ""
	for _, p := range paths {
		list += p + "\x00"
	}
	list += "\x00"
	utf16, err := syscall.UTF16FromString(list)
	if err != nil {
		return err
	}
	const dropFilesHeader = 20 // DROPFILES 结构大小
	size := uintptr(dropFilesHeader) + uintptr(len(utf16))*2
	hg, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if hg == 0 {
		return fmt.Errorf("GlobalAlloc 失败")
	}
	ptr, _, _ := procGlobalLock.Call(hg)
	if ptr == 0 {
		return fmt.Errorf("GlobalLock 失败")
	}
	mem := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size)
	for i := range mem {
		mem[i] = 0
	}
	// DROPFILES: pFiles=20, pt=(0,0), fNC=0, fWide=1
	binary.LittleEndian.PutUint32(mem[0:], dropFilesHeader)
	binary.LittleEndian.PutUint32(mem[16:], 1) // fWide = TRUE
	copy(mem[dropFilesHeader:], unsafe.Slice(
		(*byte)(unsafe.Pointer(unsafe.SliceData(utf16))), len(utf16)*2))
	procGlobalUnlock.Call(hg)
	return withEmptyClipboard(func() error {
		r, _, callErr := procSetClipboardData.Call(cfHDROP, hg)
		if r == 0 {
			return fmt.Errorf("SetClipboardData: %w", callErr)
		}
		return nil
	})
}

// withEmptyClipboard 打开剪贴板 → Empty → 执行写入（须在清空与写入之间保持持有）。
func withEmptyClipboard(fn func() error) error {
	if !openClipboardRetry(0, 6, 15*time.Millisecond) {
		return fmt.Errorf("打开剪贴板失败（被占用）")
	}
	defer procCloseClipboard.Call()
	if r, _, _ := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard 失败")
	}
	return fn()
}

// --- 小工具 ---

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func stringsJoin(list []string, sep string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func colorNRGBA(r, g, b, a byte) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: a} }

// winui 复用（避免引入 internal/winui 到非 Windows 构建；此文件本身 windows-only）。
func winuiGetModuleHandle() windows.Handle {
	h, _, _ := procGetModuleHandleW.Call(0)
	return windows.Handle(h)
}

func winuiRunMessageLoop() {
	var msg struct {
		HWnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      struct{ X, Y int32 }
		Private uint32
	}
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func winuiPostQuitMessage(code int32) { procPostQuitMessage.Call(uintptr(code)) }

var (
	procGetModuleHandleW = clipKernel32.NewProc("GetModuleHandleW")
	procGetMessageW      = clipUser32.NewProc("GetMessageW")
	procTranslateMessage = clipUser32.NewProc("TranslateMessage")
	procDispatchMessageW = clipUser32.NewProc("DispatchMessageW")
	procPostQuitMessage  = clipUser32.NewProc("PostQuitMessage")
	procDefWindowProcW   = clipUser32.NewProc("DefWindowProcW")
)
