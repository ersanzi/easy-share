//go:build windows

package cfapi

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 本文件把前三层接起来，形成一次「挂载」：
//
//	云端清单（ListFunc） → 占位符树（BuildTree） → 读取时按需拉取（OpenFunc）
//
// ── 身份设计（关键决定）─────────────────────────────────────────
//
// FETCH_DATA 回调拿到的信息是：(占位符身份字节, 本地绝对路径)。要拉数据必须知道
// **云端路径**，而云端路径会随改名变化——所以**不能把路径写进身份**。
//
// 本实现的身份编码是 `esf:<fileId>`（fileId 是控制面 es_file 的稳定雪花 ID，
// 见 ADR-0001 的文件身份模型）。挂载时建立 `fileId → 云端路径` 的内存索引；
// 进程重启后同步根会被重新注册、树会重建，索引随之重建。
//
// 这条路线的代价与收益：
//   - 收益：改名后身份不变（符合 ADR-0001「路径只是属性」）；
//   - 代价：**索引只活在内存里**，重启期间若有改名，索引会指向旧路径。
//     对只读原型可接受（重启即重建）；做双向同步时必须落盘。
//
// WPS 的做法更重：它用独立的 `fs_metadata_table` 持久化
// `id ↔ fsFileId ↔ cloudFileId` 三向映射（取证 §5.1）。本片不引入那张表。

// ListFunc 返回云端清单（相对路径，不含同步根）。
//
// 实现方通常是 internal/drive.Client.Objects 的一层薄封装。
// ctx 由挂载层提供，带超时。
type ListFunc func(ctx context.Context) ([]CloudEntry, error)

// OpenFunc 按云端路径读取内容。
//
// offset/length 是期望区间；实现方可以忽略 length 直接返回从 offset 起的
// 一段，但**必须至少覆盖 length**（否则读取方拿到短读会重试）。
// 返回的 ReadCloser 由调用方关闭。
type OpenFunc func(ctx context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error)

// mount 是一次挂载的全部状态。
type Mount struct {
	rootPath string
	session  *Session

	open OpenFunc

	mu    sync.RWMutex
	index map[string]string // fileId(十进制) → 云端相对路径

	// observer 是可选的取数观察钩子（测试与诊断用）。
	// 放在这里而不是塞进 Fetcher：Fetcher 是「生产契约」，
	// 而观察是旁路需求，不该改变它的签名。
	observer func(FetchRequest)

	// limiter 是读取节流器（切片 5 第 8 步）。
	//
	// 存在的理由：回调接上后**本机所有程序**读该目录都会触发下载，
	// 索引器与杀软会无差别扫盘。没有它，用户装好客户端第一次打开同步根
	// 就会被满速拉走整个目录。
	limiter *Limiter

	lastSync time.Time
}

// SetFetchObserver 注册一个取数观察钩子（传 nil 注销）。
//
// 用途：在**真实回调线程**上观察每次取数的进程名与区间。
// 读取节流是否生效，只能在这里看出来——生产代码不关心观察，
// 但「谁在读」这个信息一旦丢掉，节流就无从调试。
func (m *Mount) SetFetchObserver(fn func(FetchRequest)) {
	m.mu.Lock()
	m.observer = fn
	m.mu.Unlock()
}

// MountOptions 是 MountSpace 的参数。
type MountOptions struct {
	// RootPath 是本地同步根目录（必须已存在）。
	RootPath string
	// Provider 标识提供给系统（显示名/版本/GUID）。**同一空间应长期用同一 GUID**。
	Provider ProviderInfo
	// List 拉取云端清单。
	List ListFunc
	// Open 按需读取内容。
	Open OpenFunc
	// FetchTimeout 是单次拉取的上限。0 表示用默认值（30 秒）。
	//
	// 注意当前实现是**同步回调**，这个超时直接决定回调线程被占多久——
	// 见 callback_windows.go 里「已知局限」一节。
	FetchTimeout time.Duration

	// SyncRootIdentity 是同步根身份字节（可含空间名/用户标识）。
	SyncRootIdentity []byte
}

// DefaultFetchTimeout 是 Fetcher 的默认超时。
//
// 取 30 秒：足够拉普通文档，又不至于让「回调线程被占住」的时间失控。
// 大文件场景需要改成异步派发（见 callback_windows.go 的已知局限）。
const DefaultFetchTimeout = 30 * time.Second

// MountSpace 完成一次完整挂载：注册同步根 → 连接回调 → 拉清单 → 建占位符树。
//
// 返回的 *Mount 需在登出/退出时 Close（它会断开连接并注销同步根）。
func MountSpace(opt MountOptions) (*Mount, error) {
	if opt.RootPath == "" {
		return nil, fmt.Errorf("cfapi: RootPath 不能为空")
	}
	if opt.List == nil || opt.Open == nil {
		return nil, fmt.Errorf("cfapi: List 与 Open 都不能为 nil")
	}
	timeout := opt.FetchTimeout
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}

	m := &Mount{
		rootPath: opt.RootPath,
		open:     opt.Open,
		index:    map[string]string{},
		limiter:  NewLimiter(),
	}

	// ① 注册同步根
	sr, err := NewSyncRoot(opt.Provider, opt.SyncRootIdentity, []byte(identityPrefix), nil)
	if err != nil {
		return nil, err
	}
	if err := RegisterSyncRoot(opt.RootPath, &sr.Registration, &sr.Policies, RegisterFlagNone); err != nil {
		return nil, fmt.Errorf("cfapi: 注册同步根 %s 失败: %w", opt.RootPath, err)
	}

	// ② 连接并注册回调
	sess, err := Connect(opt.RootPath, m.fetcher(timeout),
		ConnectFlagRequireProcessInfo|ConnectFlagRequireFullFilePath)
	if err != nil {
		// 连接失败要把同步根撤掉，否则系统里留一个没有回调的根
		_ = UnregisterSyncRoot(opt.RootPath)
		return nil, fmt.Errorf("cfapi: 连接同步根失败: %w", err)
	}
	m.session = sess

	// ③ 拉清单建树
	if err := m.Sync(opt.List); err != nil {
		// 建树失败不撤根——树可以稍后重试（Sync 是公开的），
		// 但要把错误如实带出去让调用方决定。
		return m, err
	}
	return m, nil
}

// Sync 重新拉取云端清单并重建占位符树，同时刷新身份索引。
//
// 可重复调用（例如定时刷新）。**不做增量**——增量需要服务端的 spaceVersion
// 与变更日志（路线图切片 2），本片是「全量重建」的最小形态。
func (m *Mount) Sync(list ListFunc) error {
	m.mu.Lock()
	listFn := list
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	entries, err := listFn(ctx)
	if err != nil {
		return fmt.Errorf("cfapi: 拉取云端清单失败: %w", err)
	}

	stats := BuildTree(m.rootPath, entries)

	// 清理幽灵占位符（云端已删、本地还在）。
	//
	// **必须在 BuildTree 之后**：先建/更新出所有云端仍存在的条目，
	// 再清理剩下的——反过来会把「本轮刚建出来的」也当成幽灵删掉。
	//
	// 只删未水合的；已水合的保留并计数（那份内容是本地仅存，
	// 删掉就是销毁用户数据，见 removeGhosts 的说明）。
	stats.Removed, stats.SkippedHydrated = removeGhosts(m.rootPath, entries)

	// 更新索引：fileId → 云端路径。只收**创建成功**的条目，
	// 失败的条目不进索引（否则读取时才知道它没建出来）。
	m.mu.Lock()
	idx := make(map[string]string, len(entries))
	for _, e := range entries {
		if id, ok := decodeIdentity(e.Identity); ok {
			idx[id] = e.Path
		}
	}
	m.index = idx
	m.lastSync = time.Now()
	m.mu.Unlock()

	if len(stats.Failures) > 0 {
		return fmt.Errorf("cfapi: 建树有 %d 条失败: %w", len(stats.Failures), stats)
	}
	return nil
}

// Close 断开连接并注销同步根。可重复调用。
func (m *Mount) Close() error {
	if m == nil {
		return nil
	}
	var firstErr error
	if m.session != nil {
		if err := m.session.Close(); err != nil {
			firstErr = err
		}
	}
	// 注销同步根：不做的话系统里会留一个指向不存在提供方的根
	if err := UnregisterSyncRoot(m.rootPath); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// LastSync 返回上次成功同步的时间。
func (m *Mount) LastSync() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastSync
}

// fetcher 产出给 CfApi 的取数函数。
//
// 它把「占位符身份」翻译成「云端路径」，再调 OpenFunc 取数据。
// 这是同步实现——见 callback_windows.go 的已知局限。
func (m *Mount) fetcher(timeout time.Duration) Fetcher {
	return func(req FetchRequest) FetchResult {
		id, ok := decodeIdentity(req.FileIdentity)
		if !ok {
			return FetchResult{Err: fmt.Errorf("无法解析占位符身份（%d 字节）", len(req.FileIdentity))}
		}
		m.mu.RLock()
		cloudPath, found := m.index[id]
		m.mu.RUnlock()
		if !found {
			return FetchResult{Err: fmt.Errorf("fileId %s 不在索引中（可能未同步成功）", id)}
		}

		// 系统要的区间：优先满足 Required，忽略 Optional（那是性能优化，不是正确性）
		offset := req.RequiredOffset
		length := req.RequiredLength
		if length <= 0 {
			// 有些读取路径不给长度（例如「整份读」）——此时用文件大小兜底。
			length = req.FileSize - offset
		}
		if length <= 0 {
			return FetchResult{Err: fmt.Errorf("请求区间非法：offset=%d length=%d", offset, length)}
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		// 观察钩子：在真实回调线程上，**取数之前**报告「谁在读什么」。
		// 读取节流要靠这个信息，丢了就没法调试。
		m.mu.RLock()
		obs := m.observer
		m.mu.RUnlock()
		if obs != nil {
			obs(req)
		}

		// 读取节流：按发起进程决定要不要先等一会儿。
		// **放在 open 之前**才有意义——数据已经下载完再睡，流量已经花掉了。
		// 等待有上限（见 limiter_windows.go），因为本回调是同步的，
		// 占着回调线程不放会拖垮整个同步根的正常读取。
		if wait := m.limiter.Delay(req.ProcessName, time.Now()); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return FetchResult{Err: ctx.Err()}
			}
		}

		rc, err := m.open(ctx, cloudPath, offset, length)
		if err != nil {
			return FetchResult{Err: fmt.Errorf("读取云端 %s 失败: %w", cloudPath, err)}
		}
		defer rc.Close()

		buf := make([]byte, length)
		n, err := io.ReadFull(rc, buf)
		if err != nil && err != io.ErrUnexpectedEOF {
			return FetchResult{Err: fmt.Errorf("读取云端 %s 内容失败: %w", cloudPath, err)}
		}
		if int64(n) < length {
			// 短读：如实回报实际字节数，让系统自己决定是否重试剩余区间。
			// 这里不补零——补零会让上层读到静默损坏的数据。
			buf = buf[:n]
		}
		return FetchResult{Data: buf, Offset: offset}
	}
}

// ── 身份编码 ──────────────────────────────────────────────────

// identityPrefix 是占位符身份的统一前缀，便于识别来源与版本演进。
const identityPrefix = "esf:"

// EncodeIdentity 把控制面 fileId 编码成占位符身份字节。
//
// 编码方案：`esf:<十进制 fileId>`。选十进制而非二进制，是因为它在调试时
// 可读（资源管理器属性、进程内存里都能一眼认出），代价只是几字节冗余。
func EncodeIdentity(fileID int64) []byte {
	return []byte(identityPrefix + strconv.FormatInt(fileID, 10))
}

// decodeIdentity 解析身份字节，返回 fileId 的十进制字符串（索引的键）。
//
// 返回字符串而不是 int64：索引的键就是这个字符串，
// 且 fileId 可能来自不同空间（未来需要空间前缀），保持字符串最灵活。
func decodeIdentity(b []byte) (string, bool) {
	s := string(b)
	if !strings.HasPrefix(s, identityPrefix) {
		return "", false
	}
	id := s[len(identityPrefix):]
	if id == "" {
		return "", false
	}
	return id, true
}
