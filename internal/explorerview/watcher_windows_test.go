//go:build windows

package explorerview

import (
	"testing"
	"unsafe"
)

func TestExplorerViewScopeRejectsOtherFolders(t *testing.T) {
	root := `C:\root\cloud`
	for _, item := range []struct {
		path string
		want bool
	}{
		{root, true}, {root + `\docs`, true}, {`C:\root\cloud-other`, false}, {root + `\..\other`, false}, {`D:\root\cloud`, false}, {`relative`, false},
	} {
		if got := InScope(root, item.path); got != item.want {
			t.Fatalf("scope %q=%v", item.path, got)
		}
	}
}

func TestExplorerPortraitGeometryFitsWorkArea(t *testing.T) {
	for _, dpi := range []int32{96, 120, 144, 192} {
		width, height := PortraitSize(dpi, 1920, 1040)
		if width >= height || width <= 0 || height <= 0 || width >= 1920 || height >= 1040 {
			t.Fatalf("geometry dpi=%d %dx%d", dpi, width, height)
		}
	}
}

func TestExplorerColumnInfoSDKLayout(t *testing.T) {
	if unsafe.Sizeof(columnInfo{}) != 184 || unsafe.Offsetof(columnInfo{}.Name) != 24 || unsafe.Sizeof(propertyKey{}) != 20 {
		t.Fatal("列信息结构不符SDK")
	}
}
