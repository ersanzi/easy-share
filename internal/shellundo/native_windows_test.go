//go:build windows

package shellundo

import (
	"errors"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestHRESULTChecksSignedLowWord(t *testing.T) {
	for _, hr := range []uintptr{0, 1, 0x00040001} {
		if err := checkHRESULT(hr, "成功"); err != nil {
			t.Fatalf("0x%X: %v", hr, err)
		}
	}
	for _, hr := range []uintptr{0x80004005, 0x80070005, ^uintptr(0)} {
		var failure *HRESULTError
		if err := checkHRESULT(hr, "失败"); !errors.As(err, &failure) || failure.Code != uint32(hr) {
			t.Fatalf("未保留 HRESULT: 0x%X / %v", hr, err)
		}
	}
}

func TestNativeRecycleRejectsUnconfirmedPaths(t *testing.T) {
	for _, paths := range [][]string{nil, {`C:\`}, {`C:\root\..\outside.txt`}, {`\\server\share\a`}, {`C:\root\a:stream`}, {`C:relative`}} {
		if err := NativeRecycle(paths); err == nil {
			t.Fatalf("应拒绝: %q", paths)
		}
	}
}

type fakeFileOperation struct {
	table             [23]uintptr
	object            comObject
	calls             []int
	flags             uintptr
	owner             uintptr
	failSlot          int
	failHR            uintptr
	aborted           int32
	sink              *deleteSink
	postHR            uintptr
	noRecycleItem     bool
	itemReleases      int
	operationReleases int
}

func newFakeFileOperation() *fakeFileOperation {
	fake := &fakeFileOperation{failSlot: -1}
	fake.object.table = &fake.table
	fake.table[2] = syscall.NewCallback(func(this uintptr) uintptr { fake.operationReleases++; return 0 })
	fake.table[fileOpSetFlags] = syscall.NewCallback(func(this, flags uintptr) uintptr { fake.flags = flags; return fake.result(fileOpSetFlags) })
	fake.table[fileOpSetOwner] = syscall.NewCallback(func(this, owner uintptr) uintptr { fake.owner = owner; return fake.result(fileOpSetOwner) })
	fake.table[fileOpDelete] = syscall.NewCallback(func(this, item, sink uintptr) uintptr {
		fake.sink = (*deleteSink)(unsafe.Pointer(sink))
		return fake.result(fileOpDelete)
	})
	fake.table[fileOpPerform] = syscall.NewCallback(func(this uintptr) uintptr {
		if fake.postHR != 0 || fake.noRecycleItem {
			created := uintptr(1)
			if fake.noRecycleItem {
				created = 0
			}
			sinkPostDelete(uintptr(unsafe.Pointer(fake.sink)), 0, 0, fake.postHR, created)
		}
		return fake.result(fileOpPerform)
	})
	fake.table[fileOpAborted] = syscall.NewCallback(func(this, out uintptr) uintptr {
		*(*int32)(unsafe.Pointer(out)) = fake.aborted
		return fake.result(fileOpAborted)
	})
	return fake
}

func (fake *fakeFileOperation) result(slot int) uintptr {
	fake.calls = append(fake.calls, slot)
	if fake.failSlot == slot {
		return fake.failHR
	}
	return 0
}
func (fake *fakeFileOperation) newOperation() (*comObject, error) { return &fake.object, nil }
func (fake *fakeFileOperation) newItem(path string) (*comObject, error) {
	table := new([23]uintptr)
	table[2] = syscall.NewCallback(func(this uintptr) uintptr { fake.itemReleases++; return 0 })
	return &comObject{table: table}, nil
}
func (fake *fakeFileOperation) execute() error {
	err := recycleSTA([]string{`C:\owned\a.txt`, `C:\owned\b.txt`}, 123, fake.newOperation, fake.newItem)
	runtime.KeepAlive(fake)
	return err
}

func TestNativeRecycleCOMSlotsAndUndoFlags(t *testing.T) {
	fake := newFakeFileOperation()
	if err := fake.execute(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.calls, []int{5, 9, 18, 18, 21, 22}) {
		t.Fatalf("ABI slots: %v", fake.calls)
	}
	if fake.flags&0x20080000 != 0x20080000 || fake.flags&(0x4|0x10|0x400) != 0 {
		t.Fatalf("撤销/回收/UI flags=0x%X", fake.flags)
	}
	if fake.owner != 123 || fake.itemReleases != 2 || fake.operationReleases != 1 {
		t.Fatalf("owner/releases: %+v", fake)
	}
}

func TestNativeRecyclePropagatesEveryOperationHRESULT(t *testing.T) {
	for _, slot := range []int{5, 9, 18, 21, 22} {
		t.Run(string(rune('A'+slot)), func(t *testing.T) {
			fake := newFakeFileOperation()
			fake.failSlot = slot
			fake.failHR = 0x80070005
			var failure *HRESULTError
			if err := fake.execute(); !errors.As(err, &failure) || failure.Code != 0x80070005 {
				t.Fatalf("slot %d: %v", slot, err)
			}
			if slot < 21 {
				for _, call := range fake.calls {
					if call == 21 {
						t.Fatalf("排队失败仍 Perform: %v", fake.calls)
					}
				}
			}
			if slot == 21 && fake.calls[len(fake.calls)-1] != 22 {
				t.Fatal("Perform 失败后仍须查询 aborted")
			}
			if fake.operationReleases != 1 {
				t.Fatal("COM operation 泄漏")
			}
		})
	}
}

func TestNativeRecyclePropagatesCreationFailureBeforePerform(t *testing.T) {
	fake := newFakeFileOperation()
	wanted := errors.New("Shell item failure")
	err := recycleSTA([]string{`C:\owned\a.txt`}, 0, fake.newOperation, func(string) (*comObject, error) { return nil, wanted })
	if !errors.Is(err, wanted) || !reflect.DeepEqual(fake.calls, []int{5, 9}) {
		t.Fatalf("item error: %v / %v", err, fake.calls)
	}
	err = recycleSTA([]string{`C:\owned\a.txt`}, 0, func() (*comObject, error) { return nil, wanted }, fake.newItem)
	if !errors.Is(err, wanted) {
		t.Fatal(err)
	}
}

func TestNativeRecycleCancellationAndPerItemFailure(t *testing.T) {
	fake := newFakeFileOperation()
	fake.aborted = 1
	if err := fake.execute(); !errors.Is(err, ErrAborted) {
		t.Fatalf("S_OK 不能掩盖用户取消: %v", err)
	}
	fake = newFakeFileOperation()
	fake.postHR = 0x80070020
	var failure *HRESULTError
	if err := fake.execute(); !errors.As(err, &failure) || failure.Code != 0x80070020 {
		t.Fatalf("逐项 HRESULT 被吞: %v", err)
	}
	fake = newFakeFileOperation()
	fake.noRecycleItem = true
	if err := fake.execute(); err == nil {
		t.Fatal("未进入回收站不能返回可撤销成功")
	}
}

func TestCloudReparseAllowlist(t *testing.T) {
	for i := uint32(0); i < 16; i++ {
		if !allowedCloudTag(0x9000001a | i<<12) {
			t.Fatalf("CLOUD_%d 被拒绝", i)
		}
	}
	for _, tag := range []uint32{0xa000000c, 0xa0000003, 0x8000001b, 0x9000001b, 0} {
		if allowedCloudTag(tag) {
			t.Fatalf("未知或重定向 tag 放行: 0x%X", tag)
		}
	}
}

func TestNativeRecyclePreDeleteRefusesPermanentDeletion(t *testing.T) {
	sink := newDeleteSink()
	if hr := sinkPreDelete(uintptr(unsafe.Pointer(sink)), 0, 0); int32(uint32(hr)) >= 0 || sink.failure() == nil {
		t.Fatal("Shell 未请求回收时必须拒绝执行")
	}
	sink = newDeleteSink()
	if hr := sinkPreDelete(uintptr(unsafe.Pointer(sink)), 0x80, 0); hr != 0 || sink.failure() != nil {
		t.Fatal("原生回收请求不应被拒绝")
	}
	runtime.KeepAlive(sink)
}

func TestNativeRecycleRunsInRealCOMSTAWithMissingItem(t *testing.T) {
	// 不删任何文件：只有实际 CoCreateInstance/SHCreateItemFromParsingName 路径，缺失项必失败。
	root := canonicalTestDirectory(t)
	var gotHRESULT *HRESULTError
	err := NativeRecycle([]string{root + `\not-created.txt`})
	if err == nil {
		t.Fatal("缺失文件必须在元数据验证处失败")
	}
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hr, _, _ := procUndoInitialize.Call(0, 2)
		if err := checkHRESULT(hr, "test CoInitializeEx(STA)"); err != nil {
			result <- err
			return
		}
		defer windows.CoUninitialize()
		result <- recycleSTA([]string{root + `\not-created.txt`}, 0, createFileOperation, func(path string) (*comObject, error) {
			item, err := createShellItem(path)
			if err == nil {
				releaseCOM(item)
				return nil, errors.New("Shell 意外接受缺失测试项；停止，不能进入 Perform/UI")
			}
			return nil, err
		})
	}()
	err = <-result
	if !errors.As(err, &gotHRESULT) || gotHRESULT.Operation != "SHCreateItemFromParsingName(IShellItem)" {
		t.Fatalf("真实 COM 缺失项失败应保留 SHCreateItem HRESULT: %v", err)
	}
	t.Logf("真实 STA/CoCreateInstance/IShellItem 失败码: 0x%08X", gotHRESULT.Code)
}
