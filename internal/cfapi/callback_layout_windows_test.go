//go:build windows

package cfapi

import (
	"testing"
	"unsafe"
)

// 回调侧结构体的布局断言。
//
// 期望值来自 testdata/layout_check.c 的 C 实测输出（x86_64）：
//
//	CF_CALLBACK_INFO               size=152
//	    CallbackContext off=16   SyncRootIdentity off=56   FileId off=72
//	    FileIdentity   off=88    NormalizedPath   off=104  TransferKey off=112
//	    RequestKey     off=144
//	CF_CALLBACK_PARAMETERS_FETCH   size=56
//	    RequiredFileOffset off=8   RequiredLength off=16
//	CF_OPERATION_INFO              size=48
//	    ConnectionKey off=8   TransferKey off=16   RequestKey off=40
//	CF_CALLBACK_INFO ProcessInfo off=136（读取节流取进程名用，2026-10-16 补测）
//	CF_OPERATION_PARAMETERS        size=40   TransferData off=8
//
// 这些数字**不能手算**：CF_CALLBACK_INFO 里有 4 处填充，
// 靠肉眼对齐必错。改结构体后请重跑 layout_check.c 并同步这里的常量。

const (
	cSizeCallbackInfo          = 152
	cOffCallbackContext        = 16
	cOffCallbackSyncRootID     = 56
	cOffCallbackFileID         = 72
	cOffCallbackFileIdentity   = 88
	cOffCallbackNormalizedPath = 104
	cOffCallbackTransferKey    = 112
	cOffCallbackProcessInfo    = 136
	cOffCallbackRequestKey     = 144

	cSizeCallbackParamsFetch    = 56
	cOffParamRequiredFileOffset = 8
	cOffParamRequiredLength     = 16

	// CF_PROCESS_INFO —— 读取节流要读进程名，布局错一格就会读到野指针
	// CF_PROCESS_INFO —— 读取节流要读进程名，布局错一格就会读到野指针。
	// ⚠️ 这几个值**第一版是凭记忆写的**（size=32 / PID=24 / 臆造了 ProcessName），
	// 真机跑出来进程名是字面量 "UNKNOWN"、PID 是垃圾值。真实布局：
	// **没有 ProcessName**，PID 在 4，进程名在 8 的 ImagePath（全路径）。
	cSizeProcessInfo     = 48
	cOffProcessPID       = 4
	cOffProcessImagePath = 8
	cOffProcessPackage   = 16
	cOffProcessAppID     = 24
	cOffProcessCmdLine   = 32
	cOffProcessSessionID = 40

	cSizeOperationInfo      = 48
	cOffOpInfoConnectionKey = 8
	cOffOpInfoTransferKey   = 16
	cOffOpInfoRequestKey    = 40

	cSizeOperationParameters = 40
	cOffOpParamsTransferData = 8
)

func TestCallbackInfoLayout(t *testing.T) {
	require64(t)
	var ci CallbackInfo
	base := uintptr(unsafe.Pointer(&ci))

	if got := unsafe.Sizeof(ci); got != cSizeCallbackInfo {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizeCallbackInfo)
	}
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"CallbackContext", uintptr(unsafe.Pointer(&ci.CallbackContext)) - base, cOffCallbackContext},
		{"SyncRootIdentity", uintptr(unsafe.Pointer(&ci.SyncRootIdentity)) - base, cOffCallbackSyncRootID},
		{"FileId", uintptr(unsafe.Pointer(&ci.FileID)) - base, cOffCallbackFileID},
		{"FileIdentity", uintptr(unsafe.Pointer(&ci.FileIdentity)) - base, cOffCallbackFileIdentity},
		{"NormalizedPath", uintptr(unsafe.Pointer(&ci.NormalizedPath)) - base, cOffCallbackNormalizedPath},
		{"TransferKey", uintptr(unsafe.Pointer(&ci.TransferKey)) - base, cOffCallbackTransferKey},
		{"ProcessInfo", uintptr(unsafe.Pointer(&ci.ProcessInfo)) - base, cOffCallbackProcessInfo},
		{"RequestKey", uintptr(unsafe.Pointer(&ci.RequestKey)) - base, cOffCallbackRequestKey},
	} {
		if c.got != c.want {
			t.Fatalf("CallbackInfo.%s 偏移 = %d，C 实测 %d", c.name, c.got, c.want)
		}
	}
}

func TestCallbackParametersFetchLayout(t *testing.T) {
	require64(t)
	var p CallbackParametersFetch
	base := uintptr(unsafe.Pointer(&p))

	if got := unsafe.Sizeof(p); got != cSizeCallbackParamsFetch {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizeCallbackParamsFetch)
	}
	if got := uintptr(unsafe.Pointer(&p.RequiredFileOffset)) - base; got != cOffParamRequiredFileOffset {
		t.Fatalf("RequiredFileOffset 偏移 = %d，C 实测 %d", got, cOffParamRequiredFileOffset)
	}
	if got := uintptr(unsafe.Pointer(&p.RequiredLength)) - base; got != cOffParamRequiredLength {
		t.Fatalf("RequiredLength 偏移 = %d，C 实测 %d", got, cOffParamRequiredLength)
	}
}

// TestProcessInfoLayout 钉住 CF_PROCESS_INFO 的布局。
//
// 读取节流要拿 ProcessName 去比对进程名单；这个结构是**内核填的**，
// 偏移错一格就会把别的字段当指针解引用——典型后果是随机崩溃，
// 而不是干净报错。期望值全部来自 testdata/layout_check.c 的 C 实测。
func TestProcessInfoLayout(t *testing.T) {
	require64(t)
	var pi ProcessInfo
	base := uintptr(unsafe.Pointer(&pi))

	if got := unsafe.Sizeof(pi); got != cSizeProcessInfo {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizeProcessInfo)
	}
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"ProcessId", uintptr(unsafe.Pointer(&pi.ProcessId)) - base, cOffProcessPID},
		{"ImagePath", uintptr(unsafe.Pointer(&pi.ImagePath)) - base, cOffProcessImagePath},
		{"PackageName", uintptr(unsafe.Pointer(&pi.PackageName)) - base, cOffProcessPackage},
		{"ApplicationId", uintptr(unsafe.Pointer(&pi.ApplicationId)) - base, cOffProcessAppID},
		{"CommandLine", uintptr(unsafe.Pointer(&pi.CommandLine)) - base, cOffProcessCmdLine},
		{"SessionId", uintptr(unsafe.Pointer(&pi.SessionId)) - base, cOffProcessSessionID},
	} {
		if c.got != c.want {
			t.Fatalf("ProcessInfo.%s 偏移 = %d，C 实测 %d", c.name, c.got, c.want)
		}
	}
}

func TestOperationInfoLayout(t *testing.T) {
	require64(t)
	var oi OperationInfo
	base := uintptr(unsafe.Pointer(&oi))

	if got := unsafe.Sizeof(oi); got != cSizeOperationInfo {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizeOperationInfo)
	}
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"ConnectionKey", uintptr(unsafe.Pointer(&oi.ConnectionKey)) - base, cOffOpInfoConnectionKey},
		{"TransferKey", uintptr(unsafe.Pointer(&oi.TransferKey)) - base, cOffOpInfoTransferKey},
		{"RequestKey", uintptr(unsafe.Pointer(&oi.RequestKey)) - base, cOffOpInfoRequestKey},
	} {
		if c.got != c.want {
			t.Fatalf("OperationInfo.%s 偏移 = %d，C 实测 %d", c.name, c.got, c.want)
		}
	}
}

func TestOperationParametersLayout(t *testing.T) {
	require64(t)
	var op OperationParameters
	base := uintptr(unsafe.Pointer(&op))

	if got := unsafe.Sizeof(op); got != cSizeOperationParameters {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizeOperationParameters)
	}
	if got := uintptr(unsafe.Pointer(&op.Flags)) - base; got != cOffOpParamsTransferData {
		t.Fatalf("TransferData 起始（Flags）偏移 = %d，C 实测 %d", got, cOffOpParamsTransferData)
	}
}

// callbackRegistration 必须与 CF_CALLBACK_REGISTRATION 同布局：
// {int32 Type; void* Callback}，64 位下 16 字节/项。
func TestCallbackRegistrationLayout(t *testing.T) {
	require64(t)
	var cr callbackRegistration
	if got := unsafe.Sizeof(cr); got != 16 {
		t.Fatalf("sizeof = %d，期望 16（int32 + 4 填充 + 8 指针）", got)
	}
	if off := uintptr(unsafe.Pointer(&cr.Callback)) - uintptr(unsafe.Pointer(&cr)); off != 8 {
		t.Fatalf("Callback 偏移 = %d，期望 8", off)
	}
	// CF_CALLBACK_TYPE_NONE 必须是 0xFFFFFFFF（表终止项）
	if int32(CallbackTypeNone) != -1 {
		t.Fatalf("CallbackTypeNone = %d，期望 -1（0xFFFFFFFF）", CallbackTypeNone)
	}
}
