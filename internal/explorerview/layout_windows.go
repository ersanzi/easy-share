//go:build windows

package explorerview

import (
	"fmt"
	"unsafe"

	"github.com/go-ole/go-ole"
)

type Layout struct {
	Mode    uint32        `json:"mode"`
	Keys    []propertyKey `json:"keys"`
	Columns []columnInfo  `json:"columns"`
}

// 磁贴与导航根可命中不同Shell bag；只在新导航一次恢复本目录布局，不持续强制用户视图。
func layoutInterfaces(window *ole.IDispatch) (*shellObject, *shellObject, func(), error) {
	provider, err := query((*shellObject)(unsafe.Pointer(window)), iidServiceProvider)
	if err != nil {
		return nil, nil, func() {}, err
	}
	var browser *shellObject
	err = result(call(provider, 3, uintptr(unsafe.Pointer(sidTopLevelBrowser)), uintptr(unsafe.Pointer(iidShellBrowser)), uintptr(unsafe.Pointer(&browser))), "获取布局浏览器")
	release(provider)
	if err != nil {
		return nil, nil, func() {}, err
	}
	if browser == nil {
		return nil, nil, func() {}, fmt.Errorf("Explorer 导航浏览器尚未就绪")
	}
	var view *shellObject
	err = result(call(browser, 15, uintptr(unsafe.Pointer(&view))), "获取布局视图")
	release(browser)
	if err != nil {
		return nil, nil, func() {}, err
	}
	folder, err := query(view, iidFolderView)
	if err != nil {
		release(view)
		return nil, nil, func() {}, err
	}
	columns, err := query(view, iidColumnManager)
	release(view)
	if err != nil {
		release(folder)
		return nil, nil, func() {}, err
	}
	return folder, columns, func() { release(columns); release(folder) }, nil
}

func readLayout(window *ole.IDispatch) (Layout, error) {
	var layout Layout
	folder, columns, close, err := layoutInterfaces(window)
	if err != nil {
		return layout, err
	}
	defer close()
	if err := result(call(folder, 3, uintptr(unsafe.Pointer(&layout.Mode))), "读取目录视图模式"); err != nil {
		return layout, err
	}
	var count uint32
	if err := result(call(columns, 5, 2, uintptr(unsafe.Pointer(&count))), "读取可见列数"); err != nil {
		return layout, err
	}
	if count == 0 || count > 512 {
		return layout, nil
	}
	layout.Keys = make([]propertyKey, count)
	layout.Columns = make([]columnInfo, count)
	if err := result(call(columns, 6, 2, uintptr(unsafe.Pointer(&layout.Keys[0])), uintptr(count)), "读取可见列"); err != nil {
		return layout, err
	}
	for i, key := range layout.Keys {
		layout.Columns[i] = columnInfo{Size: uint32(unsafe.Sizeof(columnInfo{})), Mask: 0x1 | 0x10}
		if err := result(call(columns, 4, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&layout.Columns[i]))), "读取列宽"); err != nil {
			return layout, err
		}
	}
	return layout, nil
}
func restoreLayout(window *ole.IDispatch, layout Layout) error {
	if len(layout.Keys) == 0 || len(layout.Keys) != len(layout.Columns) {
		return nil
	}
	folder, columns, close, err := layoutInterfaces(window)
	if err != nil {
		return err
	}
	defer close()
	if err := result(call(folder, 4, uintptr(layout.Mode)), "恢复用户目录视图"); err != nil {
		return err
	}
	if err := result(call(columns, 7, uintptr(unsafe.Pointer(&layout.Keys[0])), uintptr(len(layout.Keys))), "恢复用户列顺序"); err != nil {
		return err
	}
	for i, key := range layout.Keys {
		state := layout.Columns[i]
		if err := result(call(columns, 3, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&state))), "恢复用户列宽"); err != nil {
			return err
		}
	}
	return nil
}
