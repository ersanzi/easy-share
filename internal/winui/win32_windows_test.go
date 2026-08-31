//go:build windows

package winui

import (
	"testing"
	"unsafe"
)

// Shell_NotifyIcon 用 CbSize 校验结构版本，字段顺序或对齐与 C 不一致会导致调用
// 静默失败（图标不出现、悬停无事件），且没有明确报错。amd64 下 NOTIFYICONDATAW
// 的预期大小是 976 字节，这里锁死以防后续改字段时踩坑。
func TestNotifyIconDataSizeMatchesWin32(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("仅在 64 位下校验固定大小")
	}
	if got := unsafe.Sizeof(NotifyIconData{}); got != 976 {
		t.Fatalf("NotifyIconData 大小 = %d，期望 976（与 NOTIFYICONDATAW 一致）", got)
	}
}

// 校验关键字段偏移，定位问题时比只看总大小更直接。
func TestNotifyIconDataFieldOffsets(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("仅在 64 位下校验固定偏移")
	}
	var d NotifyIconData
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"HWnd", unsafe.Offsetof(d.HWnd), 8},
		{"UID", unsafe.Offsetof(d.UID), 16},
		{"UCallbackMessage", unsafe.Offsetof(d.UCallbackMessage), 24},
		{"HIcon", unsafe.Offsetof(d.HIcon), 32},
		{"SzTip", unsafe.Offsetof(d.SzTip), 40},
		{"UVersion", unsafe.Offsetof(d.UVersion), 816},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s 偏移 = %d，期望 %d", c.name, c.got, c.want)
		}
	}
}

func TestNotifyIconIdentifierSize(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("仅在 64 位下校验固定大小")
	}
	// DWORD + 填充 + HWND + UINT + 填充 + GUID = 40
	if got := unsafe.Sizeof(NotifyIconIdentifier{}); got != 40 {
		t.Fatalf("NotifyIconIdentifier 大小 = %d，期望 40", got)
	}
}

// NewNotifyIconData 必须自动填好 CbSize，否则 Shell_NotifyIcon 直接失败。
func TestNewNotifyIconDataFillsSizeAndTip(t *testing.T) {
	d := NewNotifyIconData(0x1234, 7, "EasyShare")
	if d.CbSize != uint32(unsafe.Sizeof(NotifyIconData{})) {
		t.Errorf("CbSize = %d，未填为结构体大小", d.CbSize)
	}
	if d.HWnd != 0x1234 || d.UID != 7 {
		t.Errorf("HWnd/UID 未正确写入：%x/%d", d.HWnd, d.UID)
	}
	if d.UCallbackMessage != WMTrayCallback {
		t.Errorf("回调消息号 = %d，期望 %d", d.UCallbackMessage, WMTrayCallback)
	}
	if got := utf16ToString(d.SzTip[:]); got != "EasyShare" {
		t.Errorf("SzTip = %q，期望 EasyShare", got)
	}
}

// 提示文字超过 128 个 UTF-16 码元时必须截断且保留结尾 NUL，不能越界写。
func TestCopyUTF16TruncatesOverlongTip(t *testing.T) {
	long := make([]byte, 0, 400)
	for i := 0; i < 400; i++ {
		long = append(long, 'A')
	}
	d := NewNotifyIconData(0, 1, string(long))
	if d.SzTip[len(d.SzTip)-1] != 0 {
		t.Fatal("截断后未保留结尾 NUL")
	}
	if got := len(utf16ToString(d.SzTip[:])); got != len(d.SzTip)-1 {
		t.Fatalf("截断长度 = %d，期望 %d", got, len(d.SzTip)-1)
	}
}

func utf16ToString(b []uint16) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	r := make([]rune, 0, n)
	for _, c := range b[:n] {
		r = append(r, rune(c))
	}
	return string(r)
}

// 拖动依赖的消息号必须与 Win32 头文件一致：写错不会编译失败，
// 只会表现为「拖不动」，很难从现象反推到常量。
func TestWindowDragConstantsMatchWin32(t *testing.T) {
	cases := map[string]struct{ got, want uintptr }{
		"WM_NCLBUTTONDOWN": {WMNCLButtonDown, 0x00A1},
		"HTCAPTION":        {HTCaption, 2},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = 0x%X，应为 0x%X", name, c.got, c.want)
		}
	}
}
