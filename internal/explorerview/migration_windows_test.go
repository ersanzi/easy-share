//go:build windows

package explorerview

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestDefaultTextStatusMigrationPreservesUserWidths(t *testing.T) {
	common := windows.GUID{Data1: 0xb725f130, Data2: 0x47ef, Data3: 0x101a, Data4: [8]byte{0xa5, 0xf1, 0x02, 0x60, 0x8c, 0x9e, 0xeb, 0xac}}
	provider := windows.GUID{Data1: 0xe77e90df, Data2: 0x6271, Data3: 0x4f5b, Data4: [8]byte{0x83, 0x4f, 0x2d, 0xd1, 0xf2, 0x45, 0xdd, 0xa4}}
	layout := Layout{Mode: 4, Keys: []propertyKey{{common, 10}, {provider, 3}, {common, 14}, {common, 4}, {common, 12}}, Columns: []columnInfo{{Width: 272}, {Width: 160}, {Width: 144}, {Width: 120}, {Width: 80}}}
	next, ok := migrateDefaultStatusColumn(layout)
	if !ok || next.Keys[1].PID != 2 || next.Columns[0].Width != 272 || next.Columns[1].Width != 160 || layout.Keys[1].PID != 3 {
		t.Fatal("默认文字列迁移必须保留宽度及原输入")
	}
	layout.Keys[2], layout.Keys[3] = layout.Keys[3], layout.Keys[2]
	if _, ok := migrateDefaultStatusColumn(layout); ok {
		t.Fatal("不能覆盖用户自定义列顺序")
	}
}
