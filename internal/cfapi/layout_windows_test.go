//go:build windows

package cfapi

import (
	"testing"
	"unsafe"
)

// 本文件把「C 结构体布局」钉成可执行断言。
//
// 为什么必须测：CfApi 是 FFI 边界。字段顺序或填充错一格，内核会按错位的偏移读
// 指针/整数，表现是随机崩溃或静默错值，而不是干净报错。
//
// **期望值不是手算的**：由 testdata/layout_check.c 用真实 C 编译器（MinGW gcc）
// 编译运行取得，实测输出（x86_64）：
//
//	FILE_BASIC_INFO            size=40  FileAttributes=32
//	CF_FS_METADATA             size=48  FileSize=40
//	CF_SYNC_REGISTRATION       size=72  ProviderName=8   ProviderId=52
//	CF_SYNC_POLICIES           size=24  Hydration=4      InSync=12
//	CF_PLACEHOLDER_CREATE_INFO size=88  FsMetadata=8  FileIdentity=56  Flags=68
//	                                    Result=72  CreateUsn=80
//
// 复现：D:\Develop\mingw64\bin\gcc.exe internal/cfapi/testdata/layout_check.c -o x && ./x
//
// 另一路佐证（WPS 取证 §2.1b，**旁证不是主证**）：WPS 的 32 位注册代码里出现
// 立即数 0x2C = 44，且它把该值当作「结构体基数」再叠加两段字符串字节数写进 StructSize。
//
// ⚠️ 本机 MinGW 无 32 位 multilib（`gcc -m32` 链接失败），故 **44 这个 32 位尺寸
// 未经我方编译实测**，只是与 C 布局的手工推算一致（4×7 + 16 = 44）。因此本文件
// **不对 32 位布局做断言**（require64 会 skip）——需要 32 位时应先补一次编译实测，
// 而不是照抄注释里的数字。
const (
	cSizeSyncRegistration       = 72
	cOffSyncRegistrationName    = 8
	cOffSyncRegistrationID      = 52
	cSizeSyncPolicies           = 24
	cOffSyncPoliciesHydration   = 4
	cOffSyncPoliciesInSync      = 12
	cSizePlaceholderCreateInfo  = 88
	cOffPlaceholderFsMetadata   = 8
	cOffPlaceholderFileIdentity = 56
	cOffPlaceholderFlags        = 68
	cOffPlaceholderResult       = 72
	cOffPlaceholderCreateUsn    = 80
	cSizeFsMetadata             = 48
	cSizeFileBasicInfo          = 40
	cOffFileBasicAttributes     = 32
)

// 本仓是 64 位目标（Wails 桌面端）。32 位下布局不同，本测试只在 64 位断言实测值。
func require64(t *testing.T) {
	t.Helper()
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("本用例的期望值取自 64 位实测；32 位下布局不同，需另取")
	}
}

func TestSyncRegistrationLayout(t *testing.T) {
	require64(t)
	var r SyncRegistration
	base := uintptr(unsafe.Pointer(&r))

	if got := unsafe.Sizeof(r); got != cSizeSyncRegistration {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizeSyncRegistration)
	}
	if got := uintptr(unsafe.Pointer(&r.ProviderName)) - base; got != cOffSyncRegistrationName {
		t.Fatalf("ProviderName 偏移 = %d，C 实测 %d", got, cOffSyncRegistrationName)
	}
	if got := uintptr(unsafe.Pointer(&r.ProviderID)) - base; got != cOffSyncRegistrationID {
		t.Fatalf("ProviderID 偏移 = %d，C 实测 %d", got, cOffSyncRegistrationID)
	}
}

func TestSyncPoliciesLayout(t *testing.T) {
	require64(t)
	var p SyncPolicies
	base := uintptr(unsafe.Pointer(&p))

	if got := unsafe.Sizeof(p); got != cSizeSyncPolicies {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizeSyncPolicies)
	}
	if got := uintptr(unsafe.Pointer(&p.Hydration)) - base; got != cOffSyncPoliciesHydration {
		t.Fatalf("Hydration 偏移 = %d，C 实测 %d", got, cOffSyncPoliciesHydration)
	}
	if got := uintptr(unsafe.Pointer(&p.InSync)) - base; got != cOffSyncPoliciesInSync {
		t.Fatalf("InSync 偏移 = %d，C 实测 %d", got, cOffSyncPoliciesInSync)
	}
}

func TestPlaceholderCreateInfoLayout(t *testing.T) {
	require64(t)
	var p PlaceholderCreateInfo
	base := uintptr(unsafe.Pointer(&p))

	if got := unsafe.Sizeof(p); got != cSizePlaceholderCreateInfo {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizePlaceholderCreateInfo)
	}
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"FsMetadata", uintptr(unsafe.Pointer(&p.FsMetadata)) - base, cOffPlaceholderFsMetadata},
		{"FileIdentity", uintptr(unsafe.Pointer(&p.FileIdentity)) - base, cOffPlaceholderFileIdentity},
		{"Flags", uintptr(unsafe.Pointer(&p.Flags)) - base, cOffPlaceholderFlags},
		{"Result", uintptr(unsafe.Pointer(&p.Result)) - base, cOffPlaceholderResult},
		{"CreateUsn", uintptr(unsafe.Pointer(&p.CreateUsn)) - base, cOffPlaceholderCreateUsn},
	} {
		if c.got != c.want {
			t.Fatalf("%s 偏移 = %d，C 实测 %d", c.name, c.got, c.want)
		}
	}
}

func TestFileBasicInfoLayout(t *testing.T) {
	require64(t)
	var bi FileBasicInfo
	base := uintptr(unsafe.Pointer(&bi))

	if got := unsafe.Sizeof(bi); got != cSizeFileBasicInfo {
		t.Fatalf("FILE_BASIC_INFO sizeof = %d，C 实测 %d", got, cSizeFileBasicInfo)
	}
	if got := uintptr(unsafe.Pointer(&bi.FileAttributes)) - base; got != cOffFileBasicAttributes {
		t.Fatalf("FileAttributes 偏移 = %d，C 实测 %d", got, cOffFileBasicAttributes)
	}
	if got := unsafe.Sizeof(FileBasicInfo{}); got != cSizeFileBasicInfo {
		t.Fatalf("sizeof = %d，C 实测 %d", got, cSizeFileBasicInfo)
	}
	if got := unsafe.Sizeof(FsMetadata{}); got != cSizeFsMetadata {
		t.Fatalf("CF_FS_METADATA sizeof = %d，C 实测 %d", got, cSizeFsMetadata)
	}
}

// FILETIME 纪元差必须与 WPS 反编译出的立即数一致（取证 §3.3b）。
func TestTimeToFiletime(t *testing.T) {
	if filetimeEpochDiff != 116444736000000000 {
		t.Fatalf("纪元差 = %d，期望 116444736000000000", filetimeEpochDiff)
	}
	if got := TimeToFiletime(0); got != 0 {
		t.Fatalf("TimeToFiletime(0) = %d，期望 0（哨兵：未知时间不转换）", got)
	}
	if got := TimeToFiletime(1); got != filetimeEpochDiff+10000000 {
		t.Fatalf("TimeToFiletime(1) = %d", got)
	}
	const y2026 = 1767225600 // 2026-01-01 00:00:00 UTC
	if got, want := TimeToFiletime(y2026), int64(y2026)*10000000+filetimeEpochDiff; got != want {
		t.Fatalf("TimeToFiletime(2026-01-01) = %d，期望 %d", got, want)
	}
}
