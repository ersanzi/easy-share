//go:build windows

// Package cfapi 封装 Windows Cloud Files API（CfApi / cldapi.dll）的最小调用面，
// 用于把云盘内容以「按需占位符」形态呈现给资源管理器：列表零下载、点开才拉数据。
//
// 设计要点
//
//  1. **动态加载**：cldapi.dll 随 Win10 1709+ 提供，且本包要能在缺失时「加载失败即
//     降级」。故全程 LoadLibrary/GetProcAddress，不做静态导入。WPS 云盘的
//     ksyncengine.dll 做法相同（见
//     docs/intelligence/2026-10-15-wps-cfapi-placeholder-reverse.md §2.1b）。
//
//  2. **结构体以 Windows SDK 的 cfapi.h 为准**：本包所有 C 结构体都是照
//     cfapi.h（本机 10.0.26100.0）逐字段誊写的，不凭记忆构造。FFI 布局错一格，
//     内核会读到垃圾指针，表现为随机崩溃而非干净报错——因此 layout_windows_test.go
//     里对每个结构体都钉了大小与字段偏移断言。
//
//  3. **只声明用到的 API 面**：CfApi 结构体跨版本有增补，用不到的字段宁可晚加
//     也不要猜。
package cfapi

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 动态加载状态。dll 的解析结果在进程内缓存一次。
var (
	loadOnce sync.Once
	dll      *windows.LazyDLL
	loadErr  error
)

// cldapi 返回 cldapi.dll 句柄，首次调用时尝试加载。
//
// 加载失败（旧系统 / 组件缺失）返回错误，调用方应据此降级——CfApi 是本包的
// 可选增强，不能让它的缺失阻断整个网盘功能。
func cldapi() (*windows.LazyDLL, error) {
	loadOnce.Do(func() {
		dll = windows.NewLazySystemDLL("cldapi.dll")
		// Load() 是实际触发点；只 NewLazySystemDLL 不会验证存在性。
		if err := dll.Load(); err != nil {
			loadErr = fmt.Errorf("cfapi: 加载 cldapi.dll 失败（需要 Win10 1709+）: %w", err)
		}
	})
	return dll, loadErr
}

// Available 报告本机是否可用 CfApi。上层据此决定走占位符还是退回 WebDAV。
func Available() bool {
	_, err := cldapi()
	return err == nil
}

// proc 取一个 CfApi 导出函数。
//
// 失败返回错误而不是让调用点 panic——CfApi 各版本导出的函数集不完全相同。
func proc(name string) (*windows.LazyProc, error) {
	d, err := cldapi()
	if err != nil {
		return nil, err
	}
	p := d.NewProc(name)
	if err := p.Find(); err != nil {
		return nil, fmt.Errorf("cfapi: 解析 %s 失败: %w", name, err)
	}
	return p, nil
}

// ── 同步根注册 ────────────────────────────────────────────────

// SyncRegistration 对应 CF_SYNC_REGISTRATION（cfapi.h）。
//
//	typedef struct CF_SYNC_REGISTRATION {
//	    ULONG StructSize;
//	    LPCWSTR ProviderName;
//	    LPCWSTR ProviderVersion;
//	    LPCVOID SyncRootIdentity;
//	    DWORD SyncRootIdentityLength;
//	    LPCVOID FileIdentity;
//	    DWORD FileIdentityLength;
//	    GUID ProviderId;
//	} CF_SYNC_REGISTRATION;
//
// 字段间**不要手写填充字段**：Go 会按 C 的规则自动插入对齐填充，
// 手写填充等于重复填充，会把结构体撑大（曾踩：32 位下算出 56 而真值是 44）。
type SyncRegistration struct {
	StructSize             uint32
	ProviderName           *uint16
	ProviderVersion        *uint16
	SyncRootIdentity       unsafe.Pointer
	SyncRootIdentityLength uint32
	FileIdentity           unsafe.Pointer
	FileIdentityLength     uint32
	ProviderID             windows.GUID
}

// 各子策略类型（cfapi.h）均为 USHORT 对，用 uint16 表示。
type (
	// HydrationPolicy 对应 CF_HYDRATION_POLICY。
	HydrationPolicy struct {
		Primary  uint16
		Modifier uint16
	}
	// PopulationPolicy 对应 CF_POPULATION_POLICY。
	PopulationPolicy struct {
		Primary  uint16
		Modifier uint16
	}
	// InSyncPolicy 对应 CF_INSYNC_POLICY（位掩码，按 DWORD 传）。
	InSyncPolicy uint32
	// HardlinkPolicy 对应 CF_HARDLINK_POLICY。
	HardlinkPolicy uint32
	// PlaceholderManagementPolicy 对应 CF_PLACEHOLDER_MANAGEMENT_POLICY。
	PlaceholderManagementPolicy uint32
)

// SyncPolicies 对应 CF_SYNC_POLICIES（cfapi.h）。
//
//	typedef struct CF_SYNC_POLICIES {
//	    ULONG StructSize;
//	    CF_HYDRATION_POLICY Hydration;
//	    CF_POPULATION_POLICY Population;
//	    CF_INSYNC_POLICY InSync;
//	    CF_HARDLINK_POLICY HardLink;
//	    CF_PLACEHOLDER_MANAGEMENT_POLICY PlaceholderManagement;
//	} CF_SYNC_POLICIES;
type SyncPolicies struct {
	StructSize            uint32
	Hydration             HydrationPolicy
	Population            PopulationPolicy
	InSync                InSyncPolicy
	HardLink              HardlinkPolicy
	PlaceholderManagement PlaceholderManagementPolicy
}

// CF_HYDRATION_POLICY_PRIMARY 取值。
const (
	HydrationPolicyPartial     = 0
	HydrationPolicyProgressive = 1
	HydrationPolicyFull        = 2
	HydrationPolicyAlwaysFull  = 3
)

// CF_POPULATION_POLICY_PRIMARY 取值。
const (
	PopulationPolicyPartial    = 0
	PopulationPolicyFull       = 1
	PopulationPolicyAlwaysFull = 2
)

// 注册标志（CF_REGISTER_FLAGS）。
const (
	RegisterFlagNone      = 0x00000000
	RegisterFlagUpdate    = 0x00000001
	RegisterFlagNoConnect = 0x00000002
)

// ConnectFlag 是 CF_CONNECT_FLAGS 中本包用到的位。
//
// WPS 取证 §3.2b 实测其连接标志取值为 6 = REQUIRE_PROCESS_INFO | REQUIRE_FULL_FILE_PATH。
// REQUIRE_PROCESS_INFO 是读取节流（知道「是谁在读」）的前置条件，必须在连接时就置起。
const (
	ConnectFlagNone                = 0x00000000
	ConnectFlagRequireProcessInfo  = 0x00000002
	ConnectFlagRequireFullFilePath = 0x00000004
)

// ── 占位符 ────────────────────────────────────────────────────

// FileBasicInfo 对应 FILE_BASIC_INFO（winbase.h）。
//
//	typedef struct _FILE_BASIC_INFO {
//	    LARGE_INTEGER CreationTime;
//	    LARGE_INTEGER LastAccessTime;
//	    LARGE_INTEGER LastWriteTime;
//	    LARGE_INTEGER ChangeTime;
//	    DWORD FileAttributes;
//	} FILE_BASIC_INFO;
//
// 各时间字段是 FILETIME（100ns，自 1601-01-01）。
type FileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
}

// FsMetadata 对应 CF_FS_METADATA（cfapi.h）。
//
//	typedef struct CF_FS_METADATA {
//	    FILE_BASIC_INFO BasicInfo;
//	    LARGE_INTEGER FileSize;
//	} CF_FS_METADATA;
type FsMetadata struct {
	BasicInfo FileBasicInfo
	FileSize  int64
}

// PlaceholderCreateInfo 对应 CF_PLACEHOLDER_CREATE_INFO（cfapi.h）。
//
//	typedef struct CF_PLACEHOLDER_CREATE_INFO {
//	    LPCWSTR RelativeFileName;
//	    CF_FS_METADATA FsMetadata;
//	    LPCVOID FileIdentity;
//	    DWORD FileIdentityLength;
//	    CF_PLACEHOLDER_CREATE_FLAGS Flags;
//	    HRESULT Result;
//	    USN CreateUsn;
//	} CF_PLACEHOLDER_CREATE_INFO;
//
// 注意 **FileSize 与时间戳都在 FsMetadata 里**，不是本结构体的直接字段——这一点与
// 直觉相反，WPS 的填充代码也印证了（它先把 CF_FS_METADATA 填好再整体传入）。
type PlaceholderCreateInfo struct {
	RelativeFileName   *uint16
	FsMetadata         FsMetadata
	FileIdentity       unsafe.Pointer
	FileIdentityLength uint32
	Flags              uint32
	Result             int32 // HRESULT，出参：调用方必须逐条检查
	CreateUsn          int64 // USN，出参
}

// CF_PLACEHOLDER_CREATE_FLAGS 取值。
const (
	PlaceholderCreateFlagNone                      = 0x00000000
	PlaceholderCreateFlagDisableOnDemandPopulation = 0x00000001
	PlaceholderCreateFlagMarkInSync                = 0x00000002
	PlaceholderCreateFlagSupersede                 = 0x00000004
	PlaceholderCreateFlagAlwaysFull                = 0x00000008
)

// CfCreatePlaceholders 的 CF_CREATE_FLAGS。
const (
	CreateFlagNone             = 0x00000000
	CreateFlagUpdateOnConflict = 0x00000001
)

// 文件属性（FILE_ATTRIBUTE_*），本包只用这两个。
//
// WPS 取证 §3.3b 实测：目录写 0x10、文件写 0x80，是二分而非复杂派生。
const (
	FileAttributeDirectory = 0x00000010
	FileAttributeNormal    = 0x00000080
	// FileAttributeReadOnly 是 FILE_ATTRIBUTE_READONLY (0x1)。
	//
	// 用途不只是「显示成只读」：它是**只读原型唯一的执行层依托**。
	// cfapi 只注册 FETCH_DATA / CANCEL_FETCH_DATA 两个回调，**没有任何写回调**，
	// 因此用户对占位符的编辑不会被上报——本地改了、云端不知道，
	// 而下次重建若带 SUPERSEDE 就会把改动覆盖成空内容（实测见
	// write_probe_windows_test.go 的 TestResyncSameRoot）。
	// 用只读属性把写操作**在文件系统层挡下来**，用户会看到明确的「拒绝访问」，
	// 好过「保存成功但会丢」。
	FileAttributeReadOnly = 0x00000001
)

// CF_OPEN_FILE_FLAGS 取值（CfOpenFileWithOplock 用）。
const (
	OpenFileFlagNone         = 0x00000000
	OpenFileFlagExclusive    = 0x00000001
	OpenFileFlagWriteAccess  = 0x00000002
	OpenFileFlagDeleteAccess = 0x00000004
	OpenFileFlagForeground   = 0x00000008
)

// ── 回调 ──────────────────────────────────────────────────────

// CallbackType 是 CF_CALLBACK_TYPE。取值来自 WPS 取证 §2.1b 的实测表。
type CallbackType int32

const (
	CallbackTypeFetchData              CallbackType = 0
	CallbackTypeCancelFetchData        CallbackType = 2
	CallbackTypeNotifyDelete           CallbackType = 6
	CallbackTypeNotifyDehydrate        CallbackType = 9
	CallbackTypeNotifyDeleteCompletion CallbackType = 11
	CallbackTypeNone                   CallbackType = -1 // 0xFFFFFFFF，表终止项
)

// filetimeEpochDiff 是 1601→1970 的 100ns 差。
//
// 该常量在 WPS 的 ksyncengine.dll 里以立即数 0x19db1ded53e8000 多次出现
// （取证 §3.3b），用于把 Unix 秒转成 FILETIME。
const filetimeEpochDiff = 116444736000000000

// TimeToFiletime 把 Unix 秒转成 FILETIME。
//
// 参数是秒而非 time.Time：调用方拿到的常是「元数据里 0 表示未知」这类哨兵值，
// 走 time.Time 反而要多绕一层。0 原样返回（对应 1601 年），语义由调用方负责。
func TimeToFiletime(unixSeconds int64) int64 {
	if unixSeconds == 0 {
		return 0
	}
	return unixSeconds*10000000 + filetimeEpochDiff
}

// ── 构造与登记 ────────────────────────────────────────────────

// ProviderInfo 描述一个同步根提供方（对应 WPS 的 ProviderName/ProviderVersion/ProviderID）。
type ProviderInfo struct {
	// Name 是提供方显示名，出现在「设置 → 同步你的设置」等系统界面里。
	Name string
	// Version 是提供方版本号（自由格式字符串，便于排查）。
	Version string
	// ID 是提供方的稳定 GUID。**同一提供方必须长期使用同一个 GUID**，
	// 否则系统会把它当成不同的提供方。
	ID windows.GUID
}

// SyncRootResult 是一次同步根注册所需的、由调用方持有的内存。
//
// Go 与 C 的内存模型不同：CF_SYNC_REGISTRATION 里的指针必须指向**在
// CfRegisterSyncRoot 整个调用期间存活**的缓冲区。若把 UTF-16 缓冲写成临时
// 变量，GC 可能在调用中被移动/回收。本类型把那些缓冲区与结构体绑在一起，
// 由调用方持有一个变量即可保证生命周期。
type SyncRootResult struct {
	Registration SyncRegistration
	Policies     SyncPolicies

	// 以下缓冲必须与 Registration/Policies 同生命周期，故一并持有。
	providerName    []uint16
	providerVersion []uint16
	syncRootID      []byte
	fileID          []byte
}

// NewSyncRoot 构造注册同步根所需的结构体与缓冲。
//
// syncRootIdentity 与 fileIdentity 是两段任意字节：前者标识「这个同步根」，
// 后者是提供方对文件身份的字典序前缀（本仓用固定前缀 + fileId 编码）。
// 两者都由调用方给出，本函数只负责保证生命周期与 StructSize 正确。
//
// policies 传 nil 时用一组适配「只读按需」的默认：Full 水合 + Full 填充。
// 选 Full 而非 Partial，是因为第一片只做只读呈现，不需要渐进式下载；
// 等做双向同步时再按 WPS 的做法细化。
func NewSyncRoot(provider ProviderInfo, syncRootIdentity, fileIdentity []byte, policies *SyncPolicies) (*SyncRootResult, error) {
	name, err := syscall.UTF16FromString(provider.Name)
	if err != nil {
		return nil, fmt.Errorf("cfapi: ProviderName 转换失败: %w", err)
	}
	ver, err := syscall.UTF16FromString(provider.Version)
	if err != nil {
		return nil, fmt.Errorf("cfapi: ProviderVersion 转换失败: %w", err)
	}

	r := &SyncRootResult{
		providerName:    name,
		providerVersion: ver,
		syncRootID:      syncRootIdentity,
		fileID:          fileIdentity,
	}
	r.Registration = SyncRegistration{
		// StructSize 必须等于真实大小：用 unsafe.Sizeof 而非字面量，
		// 这样改字段就不会忘记同步它。
		StructSize:             uint32(unsafe.Sizeof(SyncRegistration{})),
		ProviderName:           &name[0],
		ProviderVersion:        &ver[0],
		SyncRootIdentity:       bytePtr(syncRootIdentity),
		SyncRootIdentityLength: uint32(len(syncRootIdentity)),
		FileIdentity:           bytePtr(fileIdentity),
		FileIdentityLength:     uint32(len(fileIdentity)),
		ProviderID:             provider.ID,
	}
	if policies != nil {
		p := *policies
		p.StructSize = uint32(unsafe.Sizeof(SyncPolicies{}))
		r.Policies = p
	} else {
		r.Policies = SyncPolicies{
			StructSize: uint32(unsafe.Sizeof(SyncPolicies{})),
			Hydration:  HydrationPolicy{Primary: HydrationPolicyFull},
			Population: PopulationPolicy{Primary: PopulationPolicyFull},
		}
	}
	return r, nil
}

// bytePtr 返回字节切片首地址；空切片返回 nil。
//
// 调用方（NewSyncRoot 的使用者）负责让切片在整个 CfApi 调用期间存活——
// 这正是把缓冲挂到 SyncRootResult 上的原因。
func bytePtr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Pointer(&b[0])
}

// ── 占位符构造 ────────────────────────────────────────────────

// PlaceholderSpec 描述一个待创建的占位符（调用方视角，不含 C 内存细节）。
//
// 与 CF_PLACEHOLDER_CREATE_INFO 的差别：这里的时间是 Unix 秒、其余是 Go 字符串，
// 转换与内存生命周期由 NewPlaceholders 统一处理。
type PlaceholderSpec struct {
	// RelativeName 是**相对 rootPath 的单个名字**，不含路径分隔符。
	//
	// ⚠️ 实测约束（见 dirplaceholder_windows_test.go）：传入 "a/b.txt" 会让
	// 整批调用失败并返回 0x8007007B（ERROR_INVALID_NAME）。内核语义是
	// 「rootPath 决定父目录，本字段只给该目录下的一个名字」。
	//
	// 因此**建树必须逐层**：先建父目录占位符，再以该父目录为 rootPath 建子项。
	// 编排层（见 placeholder.go 的设计说明）负责这件事。
	RelativeName string
	// Size 是云端真实大小（**不是 0**）——资源管理器据此显示大小而磁盘占用为 0。
	Size int64
	// ModifiedUnix 是云端最后修改时间（Unix 秒）。0 表示未知。
	ModifiedUnix int64
	// CreatedUnix 是创建时间（Unix 秒）。0 表示未知。
	CreatedUnix int64
	// IsDir 决定 FileAttributes 与 Flags 的取值。
	IsDir bool
	// Identity 是该占位符的稳定身份字节（通常由 fileId 编码而来）。
	Identity []byte
	// ReadOnly 为 true 时给占位符打 FILE_ATTRIBUTE_READONLY。
	//
	// 只读原型**必须**用它：cfapi 不注册写回调，用户编辑不会被上报，
	// 而重建（带 SUPERSEDE）会把本地改动覆盖掉。用文件系统层的只读位
	// 把写挡在门外，用户得到明确的「拒绝访问」，而不是「保存成功但会丢」。
	ReadOnly bool
}

// Placeholders 持有一批占位符的 C 侧表示。
//
// 与 SyncRootResult 同理：名字与身份缓冲必须与结构体同生命周期，
// 否则 CfCreatePlaceholders 期间 GC 会搬走它们。
type Placeholders struct {
	Infos []PlaceholderCreateInfo

	names      [][]uint16
	identities [][]byte
}

// NewPlaceholders 把调用方视角的 spec 批量转成 C 结构体。
//
// 取值依据（WPS 取证 §3.3b 实测，非猜测）：
//   - FileAttributes：目录 0x10(DIRECTORY) / 文件 0x80(NORMAL)
//   - Flags：文件 MARK_IN_SYNC；目录另加 DISABLE_ON_DEMAND_POPULATION
//     （目录内容由我们自己的列表任务驱动，不让系统替它按需填充）
//   - FileSize / 时间戳填进 FsMetadata（不是结构体直接字段）
//   - FsMetadata 的 ChangeTime 与 LastAccessTime 一并填成修改时间，
//     避免系统把它们当作 0（1601 年）而显示异常
func NewPlaceholders(specs []PlaceholderSpec) *Placeholders {
	p := &Placeholders{
		Infos:      make([]PlaceholderCreateInfo, 0, len(specs)),
		names:      make([][]uint16, 0, len(specs)),
		identities: make([][]byte, 0, len(specs)),
	}
	for _, s := range specs {
		// UTF16FromString 仅在字符串含 NUL 时失败；失败则跳过该条，
		// 让调用方通过 Infos 长度变化感知（批量接口允许部分成功）。
		name, err := syscall.UTF16FromString(s.RelativeName)
		if err != nil {
			continue
		}
		identity := s.Identity
		if identity == nil {
			identity = make([]byte, 0)
		}
		p.names = append(p.names, name)
		p.identities = append(p.identities, identity)

		attributes := uint32(FileAttributeNormal)
		flags := uint32(PlaceholderCreateFlagMarkInSync)
		if s.IsDir {
			attributes = FileAttributeDirectory
			flags |= PlaceholderCreateFlagDisableOnDemandPopulation
		}
		if s.ReadOnly {
			attributes |= FileAttributeReadOnly
		}
		lastWrite := TimeToFiletime(s.ModifiedUnix)
		p.Infos = append(p.Infos, PlaceholderCreateInfo{
			RelativeFileName: &name[0],
			FsMetadata: FsMetadata{
				BasicInfo: FileBasicInfo{
					CreationTime:   TimeToFiletime(s.CreatedUnix),
					LastAccessTime: lastWrite,
					LastWriteTime:  lastWrite,
					ChangeTime:     lastWrite,
					FileAttributes: attributes,
				},
				FileSize: s.Size,
			},
			FileIdentity:       bytePtr(identity),
			FileIdentityLength: uint32(len(identity)),
			Flags:              flags,
		})
	}
	return p
}

// ── 调用 ──────────────────────────────────────────────────────

// 更新标志（CF_UPDATE_FLAGS，cfapi.h）。
//
// 只声明本包用得到的。**取值照抄头文件，不凭记忆**——
// 2026-10-16 在 CF_PROCESS_INFO 上刚栽过一次（见 callback_windows.go）。
const (
	UpdateFlagNone       = 0x00000000
	UpdateFlagMarkInSync = 0x00000002
	// UpdateFlagDisableOnDemandPopulation 目录用（与创建时的 Directory 分支一致）。
	UpdateFlagDisableOnDemandPopulation = 0x00000010
)

// UpdatePlaceholder 刷新已有占位符的元数据（大小/时间/身份），**不下载内容**。
//
// 这是「远端清单变了怎么办」的正解（路线图切片 5 第 5 步，WPS 取证 §9 第 5 条：
// `_handleCloudModifyTask` → `KCfApiSyncFolderUpdatePlaceholderTask`）：
// **只更元数据，绝不触发下载**。
//
// <p><b>为什么不能用「删掉再建」（`CfCreatePlaceholders` + `SUPERSEDE`）代替</b>：
// 那会**清掉本地已水合的内容**。KI-14 实测过这条路的终点——
// 用户的本地改动被覆盖成 20 个 0 字节。当时我刻意没有启用 SUPERSEDE，
// 正是因为那是数据丢失路径。本函数才是让它可重复调用的正解：
// 占位符还在、内容还在，只把大小与时间对齐云端。
//
// <p>调用前必须先用 {@link OpenPlaceholderHandle} 拿到句柄。
// FsMetadata 传 nil 表示不改元数据（只改身份或标志）。
func UpdatePlaceholder(handle syscall.Handle, fsMeta *FsMetadata, identity []byte,
	flags uint32) error {
	p, err := proc("CfUpdatePlaceholder")
	if err != nil {
		return err
	}

	var metaPtr uintptr
	if fsMeta != nil {
		m := *fsMeta
		m.BasicInfo.FileAttributes |= FileAttributeReadOnly
		metaPtr = uintptr(unsafe.Pointer(&m))
	}
	var idPtr uintptr
	if len(identity) > 0 {
		idPtr = uintptr(unsafe.Pointer(&identity[0]))
	}
	r1, _, _ := p.Call(
		uintptr(handle),
		metaPtr,
		idPtr,
		uintptr(len(identity)),
		0, // DehydrateRangeArray
		0, // DehydrateRangeCount
		uintptr(flags),
		0, // UpdateUsn
		0, // Overlapped（同步调用）
	)
	return checkHResult(r1, "CfUpdatePlaceholder")
}

// FileReadAttributes 是 FILE_READ_ATTRIBUTES (0x80)。
//
// **元数据操作必须用它，不能用 GENERIC_READ**——2026-10-16 实测：
// 用 GENERIC_READ 打开占位符句柄**会直接触发水合**（下载整个文件）。
// 若元数据同步走那条路，每次同步都会把整个文件拉下来，
// 「列表零下载」的核心承诺就废了（见 handle_probe_windows_test.go）。
//
// CfUpdatePlaceholder 是纯元数据操作，属性访问权足够：
// 实测用 FILE_READ_ATTRIBUTES 打开 + 更新，磁盘分配全程为 0。
const FileReadAttributes = 0x0080

// OpenPlaceholderHandle 打开同步根内某个文件的句柄，供 CfApi 的「按句柄」接口使用
// （UpdatePlaceholder / DehydratePlaceholder / SetPinState）。
//
// **用 FILE_READ_ATTRIBUTES 而不是 GENERIC_READ**：只请求属性访问权，
// 不请求数据访问权，内核因此**不会水合**该占位符。
// 这不是省权限的小优化——GENERIC_READ 实测会让「改元数据」变成
// 「下载整个文件」（见上方常量注释）。
//
// FILE_FLAG_BACKUP_SEMANTICS 允许打开**目录**（少了它目录会报拒绝访问）。
// 调用方负责 CloseHandle。
func OpenPlaceholderHandle(absPath string) (syscall.Handle, error) {
	pathPtr, err := syscall.UTF16PtrFromString(absPath)
	if err != nil {
		return 0, fmt.Errorf("cfapi: 路径转换失败: %w", err)
	}
	h, err := syscall.CreateFile(
		pathPtr,
		FileReadAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("cfapi: 打开占位符 %s 失败: %w", absPath, err)
	}
	return h, nil
}

// RegisterSyncRoot 把 rootPath 注册为同步根。
func RegisterSyncRoot(rootPath string, reg *SyncRegistration, policies *SyncPolicies, flags uint32) error {
	p, err := proc("CfRegisterSyncRoot")
	if err != nil {
		return err
	}
	pathPtr, err := syscall.UTF16PtrFromString(rootPath)
	if err != nil {
		return fmt.Errorf("cfapi: 同步根路径转换失败: %w", err)
	}
	r1, _, _ := p.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(reg)),
		uintptr(unsafe.Pointer(policies)),
		uintptr(flags),
	)
	return checkHResult(r1, "CfRegisterSyncRoot")
}

// UnregisterSyncRoot 注销同步根（卸载/登出时调用）。
func UnregisterSyncRoot(rootPath string) error {
	p, err := proc("CfUnregisterSyncRoot")
	if err != nil {
		return err
	}
	pathPtr, err := syscall.UTF16PtrFromString(rootPath)
	if err != nil {
		return fmt.Errorf("cfapi: 同步根路径转换失败: %w", err)
	}
	r1, _, _ := p.Call(uintptr(unsafe.Pointer(pathPtr)))
	return checkHResult(r1, "CfUnregisterSyncRoot")
}

// CreatePlaceholders 批量创建占位符。
//
// 调用后**必须逐条检查 infos[i].Result**：批量调用允许部分成功
// （WPS 取证 §3.3 的做法）。返回 processed 是内核实际处理的条目数。
func CreatePlaceholders(rootPath string, infos []PlaceholderCreateInfo, flags uint32) (uint32, error) {
	if len(infos) == 0 {
		return 0, nil
	}
	p, err := proc("CfCreatePlaceholders")
	if err != nil {
		return 0, err
	}
	pathPtr, err := syscall.UTF16PtrFromString(rootPath)
	if err != nil {
		return 0, fmt.Errorf("cfapi: 同步根路径转换失败: %w", err)
	}
	var processed uint32
	r1, _, _ := p.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&infos[0])),
		uintptr(len(infos)),
		uintptr(flags),
		uintptr(unsafe.Pointer(&processed)),
	)
	if err := checkHResult(r1, "CfCreatePlaceholders"); err != nil {
		return processed, err
	}
	return processed, nil
}

// checkHResult 把 CfApi 返回值转成 Go error。
//
// 非 0 即失败；具体码值原样带出便于对照文档排查。
func checkHResult(r1 uintptr, api string) error {
	if r1 == 0 {
		return nil
	}
	return fmt.Errorf("cfapi: %s 失败: 0x%08x", api, uint32(r1))
}
