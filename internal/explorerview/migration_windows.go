//go:build windows

package explorerview

import "golang.org/x/sys/windows"

// 仅迁移早期本程序生成的五列默认布局；保留每个用户列宽，不动自定义列顺序/视图。
func migrateDefaultStatusColumn(layout Layout) (Layout, bool) {
	common := windows.GUID{Data1: 0xb725f130, Data2: 0x47ef, Data3: 0x101a, Data4: [8]byte{0xa5, 0xf1, 0x02, 0x60, 0x8c, 0x9e, 0xeb, 0xac}}
	provider := windows.GUID{Data1: 0xe77e90df, Data2: 0x6271, Data3: 0x4f5b, Data4: [8]byte{0x83, 0x4f, 0x2d, 0xd1, 0xf2, 0x45, 0xdd, 0xa4}}
	if layout.Mode != 4 || len(layout.Keys) != 5 || len(layout.Columns) != 5 {
		return layout, false
	}
	for index, pid := range map[int]uint32{0: 10, 2: 14, 3: 4, 4: 12} {
		if layout.Keys[index] != (propertyKey{ID: common, PID: pid}) {
			return layout, false
		}
	}
	if layout.Keys[1] != (propertyKey{ID: provider, PID: 3}) {
		return layout, false
	}
	layout.Keys = append([]propertyKey(nil), layout.Keys...)
	layout.Keys[1] = propertyKey{ID: provider, PID: 2}
	return layout, true
}
