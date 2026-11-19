//go:build windows

package shellundo

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iidUnknown    = windows.GUID{Data1: 0, Data2: 0, Data3: 0, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidDeleteSink = windows.GUID{Data1: 0x04b0f1a7, Data2: 0x9490, Data3: 0x44bc, Data4: [8]byte{0x96, 0xe1, 0x42, 0x96, 0xa3, 0x12, 0x52, 0xe2}}
	deleteTable   = [19]uintptr{
		syscall.NewCallback(sinkQuery), syscall.NewCallback(sinkAddRef), syscall.NewCallback(sinkRelease),
		syscall.NewCallback(sinkStart), syscall.NewCallback(sinkFinish),
		syscall.NewCallback(sinkPreRename), syscall.NewCallback(sinkPostRename),
		syscall.NewCallback(sinkPreMove), syscall.NewCallback(sinkPostMove),
		syscall.NewCallback(sinkPreCopy), syscall.NewCallback(sinkPostCopy),
		syscall.NewCallback(sinkPreDelete), syscall.NewCallback(sinkPostDelete),
		syscall.NewCallback(sinkPreNew), syscall.NewCallback(sinkPostNew),
		syscall.NewCallback(sinkProgress), syscall.NewCallback(sinkTimer),
		syscall.NewCallback(sinkTimer), syscall.NewCallback(sinkTimer),
	}
)

type deleteSink struct {
	table *[19]uintptr // COM vtable 必须在首字段。
	refs  atomic.Uint32
	mu    sync.Mutex
	err   error
}

func newDeleteSink() *deleteSink {
	sink := &deleteSink{table: &deleteTable}
	sink.refs.Store(1)
	return sink
}

func (sink *deleteSink) record(err error) {
	if err != nil {
		sink.mu.Lock()
		sink.err = errors.Join(sink.err, err)
		sink.mu.Unlock()
	}
}

func (sink *deleteSink) failure() error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.err
}

func sinkQuery(this, riid, out uintptr) uintptr {
	if riid == 0 || out == 0 {
		return 0x80004003 // E_POINTER
	}
	*(*uintptr)(unsafe.Pointer(out)) = 0
	id := *(*windows.GUID)(unsafe.Pointer(riid))
	if id != iidUnknown && id != iidDeleteSink {
		return 0x80004002 // E_NOINTERFACE
	}
	*(*uintptr)(unsafe.Pointer(out)) = this
	sinkAddRef(this)
	return 0
}
func sinkAddRef(this uintptr) uintptr {
	return uintptr((*deleteSink)(unsafe.Pointer(this)).refs.Add(1))
}
func sinkRelease(this uintptr) uintptr {
	return uintptr((*deleteSink)(unsafe.Pointer(this)).refs.Add(^uint32(0)))
}
func sinkStart(this uintptr) uintptr { return 0 }
func sinkFinish(this, hr uintptr) uintptr {
	(*deleteSink)(unsafe.Pointer(this)).record(checkHRESULT(hr, "IFileOperationProgressSink.FinishOperations"))
	return 0
}
func sinkPreRename(this, flags, item, name uintptr) uintptr                          { return 0 }
func sinkPostRename(this, flags, item, name, hr, created uintptr) uintptr            { return 0 }
func sinkPreMove(this, flags, item, destination, name uintptr) uintptr               { return 0 }
func sinkPostMove(this, flags, item, destination, name, hr, created uintptr) uintptr { return 0 }
func sinkPreCopy(this, flags, item, destination, name uintptr) uintptr               { return 0 }
func sinkPostCopy(this, flags, item, destination, name, hr, created uintptr) uintptr { return 0 }
func sinkPreDelete(this, flags, item uintptr) uintptr {
	if flags&0x80 == 0 { // TSF_DELETE_RECYCLE_IF_POSSIBLE；禁止排队为永久删除。
		(*deleteSink)(unsafe.Pointer(this)).record(fmt.Errorf("Windows Shell 未请求回收，拒绝永久删除"))
		return 0x80004005
	}
	return 0
}
func sinkPostDelete(this, flags, item, hr, created uintptr) uintptr {
	sink := (*deleteSink)(unsafe.Pointer(this))
	if err := checkHRESULT(hr, "IFileOperationProgressSink.PostDeleteItem"); err != nil {
		sink.record(err)
		return uintptr(uint32(hr))
	}
	if created == 0 {
		sink.record(fmt.Errorf("Windows Shell 未返回回收站项目，删除不能视为可撤销成功"))
		return 0x80004005 // E_FAIL，停止后续排队项。
	}
	return 0
}
func sinkPreNew(this, flags, destination, name uintptr) uintptr { return 0 }
func sinkPostNew(this, flags, destination, name, template, attributes, hr, created uintptr) uintptr {
	return 0
}
func sinkProgress(this, total, completed uintptr) uintptr { return 0 }
func sinkTimer(this uintptr) uintptr                      { return 0 }
