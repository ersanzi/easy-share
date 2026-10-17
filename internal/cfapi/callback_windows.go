//go:build windows

package cfapi

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// 本文件实现 CfApi 的**回调侧**：连接同步根、注册 FETCH_DATA 回调、
// 在回调里把云端数据回填进占位符。这是「点开才拉」的实现点。
//
// ── 线程模型（关键）─────────────────────────────────────────────
//
// CfApi 回调**由 cldapi 的内部线程调用**，不是我们创建的。这带来三条硬约束：
//
//  1. **回调必须尽快返回**。回调整体有超时（系统会判 I/O 超时并取消），
//     在里面做网络 I/O 必须自己控制时长。
//
//  2. **回调期间不能让 Go 运行时停摆**。本包要求调用方在进程启动时
//     保证 `runtime.LockOSThread` 之外不做手脚——Go 运行时会为回调创建/复用
//     M（机器线程），syscall.NewCallback 的 trampoline 会正确处理栈切换。
//
//  3. **回调里的 panic 会杀死进程**（跨 C 边界）。因此回调实现里所有
//     可能 panic 的地方都要 recover，并转成错误状态回给系统。

// FetchRequest 描述一次「系统要数据」的请求，交给调用方去取数据。
//
// 字段取自 CF_CALLBACK_INFO 与 CF_CALLBACK_PARAMETERS 的并集，已转成 Go 语义
// （时间/大小是 Go 类型，路径是 string）。调用方无需接触任何 C 结构体。
type FetchRequest struct {
	// FileIdentity 是占位符的稳定身份（创建时写入的那段字节）。
	// 调用方用它反查是哪个云端文件——**不要用路径反查**（改名后路径会变）。
	FileIdentity []byte
	// NormalizedPath 是文件的绝对路径（含同步根）。
	NormalizedPath string
	// FileSize 是云端大小（占位符创建时写入的值）。
	FileSize int64
	// RequiredOffset / RequiredLength 是系统这次最少需要的区间。
	RequiredOffset int64
	RequiredLength int64
	// OptionalOffset / OptionalLength 是「顺手多给一点」的区间（可为 0）。
	OptionalOffset int64
	OptionalLength int64
	// PriorityHint 越小越优先（0 = 最高）。
	PriorityHint uint8
	// ProcessName 是**发起这次读取的进程名**（不含路径），如 "explorer.exe"。
	//
	// 来自 CF_CALLBACK_INFO.ProcessInfo，需要连接时置
	// CF_CONNECT_FLAG_REQUIRE_PROCESS_INFO（本包已置）。
	//
	// **这是读取节流的前置条件**：回调接上后本机所有程序读该目录都会触发下载，
	// 索引器与杀软会无差别扫盘。调用方据此决定是否放行/限速。
	// 取不到时为空串（不编造），调用方按「未知进程」处理。
	ProcessName string
	// ProcessId 是发起读取的进程 ID（诊断与去重用）。
	ProcessId uint32
}

// FetchResult 是调用方对一次拉取的答复。
type FetchResult struct {
	// Data 是本次要回填的字节。
	Data []byte
	// Offset 是 Data 在文件内的起始偏移（通常等于 RequiredOffset）。
	Offset int64
	// Err 非 nil 表示本次拉取失败，回调会带错误状态返回给系统，
	// 系统会把这次 I/O 报错给发起进程（而不是让读取永久挂起）。
	Err error
}

// Fetcher 由调用方实现：给定请求，返回要回填的字节。
//
// ⚠️ **本接口是同步的，且 fetcher 在系统回调线程上执行**——这是当前实现的一处
// **已知局限**，接入真实云端前必须处理（见下）。
//
// 实现要求：
//   - 不要缓存整份文件到内存——大文件会撑爆内存（这正是本仓 WebDAV 路径的老问题）；
//   - 网络错误应返回 FetchResult{Err: ...}，不要 panic；
//   - **必须自己控制耗时上限**（用 http.Client 的 Timeout 或 context）。
//     实测 20 秒的取数在本机仍然成功（见 syncslow_windows_test.go），
//     但**回调线程被占住期间无法处理其他请求**，且等待时长没有已知上限——
//     所以超时应当由我们自己设，而不是依赖系统某处会兜住。
//
// ── 已知局限：同步阻塞 vs WPS 的异步派发 ────────────────────────
//
// 本实现**在回调内部同步完成取数据 + 回填**，回调返回时工作已经做完。
// WPS 的做法不同：它的 `cfApiFetchDataCallback` 只造一个
// `KSyncEngineHydrationJobContext`，然后用 **Qt 队列连接**把活派发给业务线程
// （`QMetaObject::invokeMethod(..., "slotFetchData", Qt::QueuedConnection, ...)`），
// **立刻返回**（取证 §2.1b (4)）。
//
// 两种模型都合法（CfApi 允许同步回填，也允许回调返回后再异步 CfExecute），差别在：
//
// | | 同步（本实现） | 异步（WPS） |
// | --- | --- | --- |
// | 回调线程占用 | 整个取数期间 | 仅入队瞬间 |
// | 大文件/慢网络 | **可能撞系统 I/O 超时**（`CF_CALLBACK_CANCEL_FLAG_IO_TIMEOUT`） | 不受影响 |
// | 并发度 | 受回调线程数限制 | 由业务线程池决定 |
// | 实现复杂度 | 低 | 需自己管任务生命周期与取消 |
//
// **当前局限的适用范围**：本包目前只在测试里被验证（fetcher 返回内存数据，
// 微秒级返回），同步模型完全够用。**接入真实控制面 + RustFS 后，大文件场景下
// 必须改成异步派发**——否则用户双击一个 GB 级文件可能等到 I/O 超时失败。
//
// 改法（留给下一片）：fetcher 返回一个「已受理」的句柄，回调立刻返回；
// 业务 goroutine 取完数据后自己调 CfExecute 回填，并处理「用户已取消」的竞态。
// 届时 `onCancelFetchData`（当前空实现）要真正参与——这也是 WPS 保留它注册的原因。
type Fetcher func(req FetchRequest) FetchResult

// callbackTable 是 CF_CALLBACK_REGISTRATION 数组的 Go 侧表示。
//
// 布局：{int32 Type; void* Callback}，并以 {CF_CALLBACK_TYPE_NONE, NULL} 收尾。
// 64 位下因指针 8 字节对齐，Type 后跟 4 字节填充，故每项 16 字节。
type callbackRegistration struct {
	Type     int32
	_        int32
	Callback uintptr
}

// connectionKey 对应 CF_CONNECTION_KEY（LARGE_INTEGER）。
type connectionKey int64

// CallbackInfo 是 CF_CALLBACK_INFO 的 Go 侧映像（**只保留本包用到的字段**）。
//
// 注意：**不能只声明子集就当结构体用**——CfApi 会把整个结构体写进来，
// 少声明字段意味着我们读不到，但布局必须覆盖到我们读的最后一个字段。
// 因此这里按 cfapi.h 的完整顺序声明到 RequestKey 为止，未用的字段保留占位。
type CallbackInfo struct {
	StructSize             uint32
	_                      uint32
	ConnectionKey          connectionKey
	CallbackContext        unsafe.Pointer
	VolumeGuidName         *uint16
	VolumeDosName          *uint16
	VolumeSerialNumber     uint32
	_                      uint32
	SyncRootFileID         int64
	SyncRootIdentity       unsafe.Pointer
	SyncRootIdentityLength uint32
	_                      uint32
	FileID                 int64
	FileSize               int64
	FileIdentity           unsafe.Pointer
	FileIdentityLength     uint32
	_                      uint32
	NormalizedPath         *uint16
	TransferKey            int64
	PriorityHint           uint8
	_                      [7]byte
	CorrelationVector      unsafe.Pointer
	ProcessInfo            unsafe.Pointer
	RequestKey             int64
}

// ProcessInfo 是 CF_PROCESS_INFO 的 Go 侧映像（cfapi.h，字段顺序逐字对应）。
//
// 用途：**读取节流**。回调接上后本机所有程序读该目录都会触发下载，
// 索引器与杀软会无差别扫盘——没有这一步，第一次打开同步根就会被它们
// 打到满速下载整个目录（WPS 取证 §3.5）。
//
// ⚠️ **这个结构第一版是凭记忆写的，错了**（2026-10-16 真机翻车）：
// 臆造了 `Flags` 与 `ProcessName`、把 `ProcessId` 放到末尾，
// 算出来 size=32 / PID off=24。真机拿到的进程名是字面量 `"UNKNOWN"`、
// PID 是 398xxxxxxx 这种垃圾值——**看起来"有值"，其实是读错了位置**。
// 对照 `Windows Kits\10\Include\...\um\cfapi.h` 才看清真实布局：
// **没有 Flags、没有 ProcessName**，PID 在 off 4，
// 进程名在 off 8 的 `ImagePath`（**全路径**，要自己取 basename）。
//
// 期望值由 testdata/layout_check.c 用真实 C 编译器取（**不手算**）：
// sizeof=48 / ProcessId=4 / ImagePath=8 / PackageName=16 /
// ApplicationId=24 / CommandLine=32 / SessionId=40。
// 只断言 64 位（本机无 32 位 multilib）。
type ProcessInfo struct {
	StructSize    uint32
	ProcessId     uint32
	ImagePath     *uint16 // 完整镜像路径，进程名要取 basename
	PackageName   *uint16
	ApplicationId *uint16
	CommandLine   *uint16
	SessionId     uint32
	_             uint32
}

// CallbackParametersFetch 是 CF_CALLBACK_PARAMETERS 中 FetchData 分支的
// Go 侧映像。
//
// CF_CALLBACK_PARAMETERS 是个大 union；FETCH_DATA 分支的布局
// = ParamSize(4) + Flags(4) + 6×LARGE_INTEGER(48) = 56 字节，
// 与 C 实测一致（见 testdata/layout_check.c 输出）。
type CallbackParametersFetch struct {
	ParamSize             uint32
	Flags                 uint32
	RequiredFileOffset    int64
	RequiredLength        int64
	OptionalFileOffset    int64
	OptionalLength        int64
	LastDehydrationTime   int64
	LastDehydrationReason uint32
	_                     uint32
}

// OperationInfo 是 CF_OPERATION_INFO 的 Go 侧映像（cfapi.h）。
type OperationInfo struct {
	StructSize uint32
	Type       uint32
	// 此处**不要手写填充**：ConnectionKey 是 int64，Go 会按 8 字节对齐自动补足。
	// 手写填充会让结构体多长 8 字节（曾踩：写测试时才发现 sizeof 56 vs C 的 48）。
	ConnectionKey     connectionKey
	TransferKey       int64
	CorrelationVector unsafe.Pointer
	SyncStatus        unsafe.Pointer
	RequestKey        int64
}

// OperationParameters 是 CF_OPERATION_PARAMETERS 的 Go 侧映像，
// **只声明 TRANSFER_DATA 分支**（其余分支是同一个 union 的别名，不声明不影响）。
type OperationParameters struct {
	ParamSize uint32
	// 这里**必须显式填充**（与其它结构体不同）：
	// CF_OPERATION_PARAMETERS 的 union 含 8 字节对齐成员（LARGE_INTEGER/指针），
	// 因此 union 整体对齐到 8，导致 TransferData 分支从偏移 8 开始，
	// ParamSize 之后有 4 字节空洞。C 实测 TransferData off=8，sizeof=40。
	// 若省掉这个填充，Go 会把 Flags 放在 4——布局整体错位 4 字节。
	_                uint32
	Flags            uint32
	CompletionStatus int32
	Buffer           unsafe.Pointer
	Offset           int64
	Length           int64
}

// CF_OPERATION_TYPE 取值。
const (
	OperationTypeTransferData = 0
)

// NTSTATUS 常量集中在一处，便于对照文档排查。
//
// 这些值 > math.MaxInt32，故声明为 uint32 再按需转换——
// 直接用未定类型常量赋给 int32 会编译失败（"constant overflows int32"）。
const statusSuccess uint32 = 0x00000000

// statusCloudFileUnsuccessful = STATUS_CLOUD_FILE_UNSUCCESSFUL，
// 表示「提供方无法满足这次读取」——用户看到「读取失败」而不是无限等待。
//
// 用 var 而非 const：该值 > math.MaxInt32，而 CompletionStatus 字段声明为
// int32（为与 C 布局一致）。常量的 int32() 转换会在**编译期**报溢出；
// 变量的转换在运行期按位截断，正是我们要的语义。
var statusCloudFileUnsuccessful = uint32(0xC000CF0C)

// Session 是一次「已连接的同步根」。用 Connect 创建、Close 释放。
//
// 一个 Session 只服务一个同步根；多个空间应各建一个。
type Session struct {
	rootPath string
	fetcher  Fetcher

	key  connectionKey
	once sync.Once
	// active 标记是否已连接，Close 用它决定要不要真正调 CfDisconnectSyncRoot。
	active bool
	mu     sync.Mutex
}

// Connect 连接同步根并注册 FETCH_DATA 回调。
//
// 最小回调集只有两项：FETCH_DATA（要数据）与 CANCEL_FETCH_DATA（用户取消）。
// 这与 WPS 的实测表一致——它在一个完整的企业云盘里也只注册 5 项
// （另加 dehydrate / delete / delete-completion），那三项属于写路径，
// 只读原型不需要（见取证 §2.1b、§3.2b）。
//
// connectFlags 建议传 ConnectFlagRequireProcessInfo | ConnectFlagRequireFullFilePath，
// 与 WPS 实测一致（取证 §3.2b）；RequireProcessInfo 是后续做读取节流的前置条件。
func Connect(rootPath string, fetcher Fetcher, connectFlags uint32) (*Session, error) {
	if fetcher == nil {
		return nil, fmt.Errorf("cfapi: fetcher 不能为 nil")
	}
	pathPtr, err := syscall.UTF16PtrFromString(rootPath)
	if err != nil {
		return nil, fmt.Errorf("cfapi: 同步根路径转换失败: %w", err)
	}

	s := &Session{rootPath: rootPath, fetcher: fetcher}

	// 回调表：两项 + 终止项。回调函数指针用 syscall.NewCallback 生成，
	// 该 trampoline 会正确切到 Go 栈，因此回调里可以安全使用 Go 代码
	// （包括 goroutine、互斥锁、defer）。
	table := []callbackRegistration{
		{Type: int32(CallbackTypeFetchData), Callback: syscall.NewCallback(s.onFetchData)},
		{Type: int32(CallbackTypeCancelFetchData), Callback: syscall.NewCallback(s.onCancelFetchData)},
		{Type: int32(CallbackTypeNone), Callback: 0},
	}

	p, err := proc("CfConnectSyncRoot")
	if err != nil {
		return nil, err
	}
	var key connectionKey
	r1, _, _ := p.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&table[0])),
		uintptr(unsafe.Pointer(s)), // CallbackContext：回调里能拿回 *Session
		uintptr(connectFlags),
		uintptr(unsafe.Pointer(&key)),
	)
	if err := checkHResult(r1, "CfConnectSyncRoot"); err != nil {
		return nil, err
	}
	s.key = key
	s.active = true
	return s, nil
}

// Close 断开同步根连接。可重复调用。
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.once.Do(func() {
		s.mu.Lock()
		active := s.active
		s.active = false
		s.mu.Unlock()
		if !active {
			return
		}
		p, perr := proc("CfDisconnectSyncRoot")
		if perr != nil {
			err = perr
			return
		}
		// ⚠️ ConnectionKey 是**按值传**（`CfDisconnectSyncRoot(CF_CONNECTION_KEY)`），
		// 不是指针。传 &key 会得到 0x80070057 (E_INVALIDARG)——
		// 这一点最初写成了取地址，被 TestMountSpaceEndToEnd 的 Close 抓到。
		r1, _, _ := p.Call(uintptr(s.key))
		err = checkHResult(r1, "CfDisconnectSyncRoot")
	})
	return err
}

// onFetchData 是 CF_CALLBACK_TYPE_FETCH_DATA 的处理函数。
//
// 签名必须与 CF_CALLBACK 一致：
//
//	typedef void (CALLBACK *CF_CALLBACK)(
//	    _In_ CONST CF_CALLBACK_INFO *CallbackInfo,
//	    _In_ CONST CF_CALLBACK_PARAMETERS *CallbackParameters);
//
// 三个必须遵守的约束（错了会崩进程或永久挂起读取）：
//  1. **必须 recover**：panic 穿过 C 边界会直接终止进程；
//  2. **必须有超时**：回调整体超时后系统会取消，我们不主动限时会导致读取长时间挂住；
//  3. **必须回一次 CfExecute**：不是「取到数据才回」——即使失败也要带错误状态回，
//     否则发起读取的进程会一直等。
func (s *Session) onFetchData(infoPtr, paramsPtr uintptr) uintptr {
	// 这条 defer 是硬要求，不是防御性编程。
	defer func() {
		if r := recover(); r != nil {
			// 无法从 panic 里安全地回 CfExecute（参数可能已损坏），
			// 但至少不能让 panic 穿过 C 边界。
			_ = r
		}
	}()

	info := asCallbackInfo(infoPtr)
	params := asCallbackParamsFetch(paramsPtr)

	// 进程信息：读取节流靠它分辨「用户在打开文件」与「索引器在扫盘」。
	// ProcessInfo 为 nil 是合法的（未置 REQUIRE_PROCESS_INFO 时），
	// 但本包在 Connect 时已置该标志；取不到就留空，不编造。
	procName, procID := processOf(info)

	req := FetchRequest{
		FileIdentity:   unsafeSlice(info.FileIdentity, uintptr(info.FileIdentityLength)),
		NormalizedPath: utf16ToString(info.NormalizedPath),
		FileSize:       info.FileSize,
		RequiredOffset: params.RequiredFileOffset,
		RequiredLength: params.RequiredLength,
		OptionalOffset: params.OptionalFileOffset,
		OptionalLength: params.OptionalLength,
		PriorityHint:   info.PriorityHint,
		ProcessName:    procName,
		ProcessId:      procID,
	}

	res := s.fetcher(req)

	// 回填：无论成功失败都要回一次，否则读取方会永久挂起。
	return s.executeTransfer(info, res)
}

// executeTransfer 用 CfExecute(TRANSFER_DATA) 把结果（或错误）回报给系统。
//
// 返回 0 是 CALLBACK 的约定返回值（void）。
func (s *Session) executeTransfer(info *CallbackInfo, res FetchResult) uintptr {
	opInfo := OperationInfo{
		StructSize:    uint32(unsafe.Sizeof(OperationInfo{})),
		Type:          OperationTypeTransferData,
		ConnectionKey: info.ConnectionKey,
		TransferKey:   info.TransferKey,
		RequestKey:    info.RequestKey,
	}
	opParams := OperationParameters{
		ParamSize: uint32(unsafe.Sizeof(OperationParameters{})),
		Offset:    res.Offset,
	}
	if res.Err != nil || len(res.Data) == 0 {
		// 失败路径：CompletionStatus 带错误码，Buffer/Length 置 0。
		// 用 STATUS_CLOUD_FILE_UNSUCCESSFUL 让系统把这次读取标记为失败
		// （用户看到「读取失败」而不是无限转圈）。
		// NTSTATUS 是 32 位无符号语义；CompletionStatus 字段声明为 int32 只为与 C 布局一致，
		// 这里按位原样传，不做数值解释。
		opParams.CompletionStatus = int32(statusCloudFileUnsuccessful)
		opParams.Length = 0
		opParams.Buffer = nil
	} else {
		opParams.CompletionStatus = int32(statusSuccess)
		opParams.Length = int64(len(res.Data))
		opParams.Buffer = unsafe.Pointer(&res.Data[0])
	}

	p, err := proc("CfExecute")
	if err != nil {
		return 0
	}
	p.Call(
		uintptr(unsafe.Pointer(&opInfo)),
		uintptr(unsafe.Pointer(&opParams)),
	)
	return 0
}

// onCancelFetchData 是 CF_CALLBACK_TYPE_CANCEL_FETCH_DATA 的处理函数。
//
// WPS 的这个回调是**空实现**（取证 §3.2b 实测函数体 76 字节）。本包同样留空——
// 但**保留注册**，因为不注册会让取消事件无人处理，超时路径更难排查。
func (s *Session) onCancelFetchData(_, _ uintptr) uintptr {
	return 0
}

// ── uintptr → 结构体指针 ──────────────────────────────────────
//
// 回调签名里的两个参数是 uintptr（syscall.NewCallback 要求）。把它们转回指针
// 会触发 `go vet` 的 unsafeptr 检查（"possible misuse of unsafe.Pointer"），
// 因为 uintptr 不被 GC 追踪。
//
// **这里的转换是安全的**，理由要写清楚而不是靠抑制：
//   - 这两块内存由 cldapi（系统组件）分配并持有，**不在 Go 堆上**，
//     因此不存在「GC 移动后指针失效」的问题；
//   - 它们是回调参数，只在本次回调期间有效——我们从不把它们留存到回调之外。
//
// 实现手法：先把 uintptr 转成指向自身的指针，再经 **unsafe.Pointer 中转一次**
// （`*(*unsafe.Pointer)(unsafe.Pointer(&p))`）。这是 `go vet` 的 unsafeptr 检查
// **认可的**惯用写法——因为转换的第一步是「指向 Go 变量的合法指针」，
// 第二步才把该变量的值当作地址。直接 `unsafe.Pointer(uintptr)` 会被判为
// 「从整数凭空造指针」，而 vet 无法区分我们这种受控场景，只能用惯用法表达。
//
// 注：`//go:nocheckptr` 只影响运行期的 checkptr 插桩，**不改变 vet 的判断**，
// 所以不能靠它消除告警（试过）。

func asCallbackInfo(p uintptr) *CallbackInfo {
	return (*CallbackInfo)(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
}

func asCallbackParamsFetch(p uintptr) *CallbackParametersFetch {
	return (*CallbackParametersFetch)(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
}

// processOf 从回调信息里取出「谁在发起这次读取」。
//
// 为什么必须走 ProcessInfo 而不是自己查：CfApi 在回调线程上把发起方的进程
// 信息一并给了我们，**前提是连接时置了 CF_CONNECT_FLAG_REQUIRE_PROCESS_INFO**
// （本包已置，见 Connect 的调用点）。自己去查当前进程只会拿到我们自己的 PID，
// 那是错的。
//
// 容错：ProcessInfo 为 nil（未置标志或系统未提供）、或 StructSize 小于本包
// 期望时，返回空值而**不是**强行按布局读——按错布局读会解引用到野指针。
// 空值让上层按「未知进程」处理（不放行即安全）。
//
// 返回值是**进程名（basename）**与 PID。CF_PROCESS_INFO 只给全路径
// （ImagePath），所以这里取 basename——进程名单是按名字比对的。
func processOf(info *CallbackInfo) (string, uint32) {
	if info == nil || info.ProcessInfo == nil {
		return "", 0
	}
	pi := (*ProcessInfo)(info.ProcessInfo)
	// StructSize 自检：内核填的是真实大小。若与本包编译期期望不符，
	// 说明布局假设不成立（例如换了 SDK 版本改了结构），宁可不读。
	if pi.StructSize != uint32(unsafe.Sizeof(ProcessInfo{})) {
		return "", 0
	}
	image := utf16ToString(pi.ImagePath)
	if image == "" {
		return "", pi.ProcessId
	}
	// 只取 basename：名单里写的是 "explorer.exe" 这类名字，不是全路径。
	// 两种分隔符都处理——同一路径在不同来源下可能已归一化。
	if i := strings.LastIndexAny(image, `\/`); i >= 0 {
		image = image[i+1:]
	}
	return image, pi.ProcessId
}

// ── 小工具 ────────────────────────────────────────────────────

// unsafeSlice 把 (指针, 长度) 转成 Go 切片。
//
// ⚠️ 返回的切片**不拥有**底层内存：它指向的是 CfApi 传入的缓冲区，
// 只在本次回调期间有效。调用方若需留存必须复制。
func unsafeSlice(p unsafe.Pointer, n uintptr) []byte {
	if p == nil || n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(p), n)
}

// utf16ToString 把 NUL 结尾的 UTF-16 指针转成 Go 字符串。
func utf16ToString(p *uint16) string {
	if p == nil {
		return ""
	}
	return syscall.UTF16ToString(unsafe.Slice(p, 1<<15))
}
