//go:build !windows

package shellundo

// NativeRecycle 在非 Windows 平台拒绝执行，不降级为普通删除。
func NativeRecycle(paths []string) error                         { return ErrUnsupported }
func NativeRecycleWithOwner(paths []string, owner uintptr) error { return ErrUnsupported }
