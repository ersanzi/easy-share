//go:build windows

package winui

import (
	"runtime"
	"testing"
)

func TestPetPaintCacheInitializesCornersAndPreservesDC(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	getDC := user32.NewProc("GetDC")
	releaseDC := user32.NewProc("ReleaseDC")
	getPixel := gdi32.NewProc("GetPixel")
	getBkMode := gdi32.NewProc("GetBkMode")
	getTextColor := gdi32.NewProc("GetTextColor")
	dc, _, _ := getDC.Call(0)
	if dc == 0 {
		t.Fatal("GetDC failed")
	}
	defer releaseDC.Call(0, dc)
	const hwnd = uintptr(0x12345678)
	defer ReleasePetPaintCache(hwnd)
	palette := PetPalette{Surface: 0x00fbf9f8, Border: 0x00e8e6e5, Ink: 0x003e2e26}
	cached := takePetPaintCache(hwnd, dc, 144, 96, 96, false, palette)
	if cached == 0 {
		t.Fatal("cache creation failed")
	}
	for _, p := range [][2]uintptr{{0, 0}, {143, 0}, {0, 95}, {143, 95}, {72, 60}} {
		color, _, _ := getPixel.Call(cached, p[0], p[1])
		if uint32(color) != palette.Surface {
			t.Fatalf("pixel %v = %#x, want %#x", p, color, palette.Surface)
		}
	}
	procSetBkMode.Call(cached, 2)
	procSetTextColor.Call(cached, 0x00123456)
	paintPetDirect(cached, Rect{Right: 144, Bottom: 96}, 96, true, palette)
	mode, _, _ := getBkMode.Call(cached)
	color, _, _ := getTextColor.Call(cached)
	if mode != 2 || color != 0x00123456 {
		t.Fatalf("DC state leaked: mode=%d color=%#x", mode, color)
	}
	if got := takePetPaintCache(hwnd, dc, 144, 96, 96, false, palette); got != cached {
		t.Fatal("unchanged cache not reused")
	}
	palette.Surface = 0x00123456
	changed := takePetPaintCache(hwnd, dc, 144, 96, 96, true, palette)
	color, _, _ = getPixel.Call(changed, 0, 0)
	if uint32(color) != palette.Surface {
		t.Fatal("palette change not repainted")
	}
}

func TestPetCornerRadius(t *testing.T) {
	for dpi, want := range map[int32]int32{96: 14, 120: 18, 144: 21, 192: 28} {
		if got := PetCornerRadius(dpi); got != want {
			t.Fatalf("radius(%d)=%d want %d", dpi, got, want)
		}
	}
}
