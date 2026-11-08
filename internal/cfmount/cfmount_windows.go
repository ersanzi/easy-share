//go:build windows

// Package cfmount 把 internal/drive（控制面云盘客户端）适配成
// internal/cfapi（Windows 按需占位符）所需的接口。
//
// 分层的理由：
//   - internal/cfapi 是**纯 CfApi 绑定**，不该知道控制面长什么样（否则无法单测，
//     也会让 CfApi 这层被 HTTP/JSON 细节污染）；
//   - internal/drive 是**纯 HTTP 客户端**，不该知道占位符、句柄、回调；
//   - 本包是两者之间唯一的桥，职责就是把「云端清单/内容」翻译成 cfapi 的输入。
//
// 与 internal/spacedav 的关系：spacedav 把空间暴露成 **WebDAV**（走 HTTP 协议，
// 资源管理器当网络位置访问，全量物化）；本包把空间暴露成 **CfApi 占位符**
// （走文件系统回调，按需水合）。两者是**同一份云盘内容的两种本地呈现**，
// 共存不冲突——但生产上应当只启用其一，否则用户会看到两个入口指向同一批文件。
package cfmount

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
	"easyshare/internal/httprange"
)

// TokenFunc 返回当前登录令牌；空串表示未登录。
//
// 与 spacedav.TokenFunc 同样的理由：会话会过期、会换账号，而挂载是长期的，
// 所以每次操作现取，退出登录后立即失去访问能力。
type TokenFunc func() string

// Lister 是本包需要的列举能力（*drive.Client 满足）。
//
// 收窄成接口便于单测：测试里塞假实现，不必起 HTTP 服务。
type Lister interface {
	Objects(ctx context.Context, token, space string) ([]drive.Object, error)
	Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error)
}

// Presigner 是**可选**能力：能签发下载用预签名 URL 的客户端。
//
// *drive.Client 满足（PresignGet 是导出方法）。有了它，本包就能自己发带
// `Range` 的 GET，从而**只下需要的区间**而不是整份对象——见 range_windows.go
// 开头对「为什么客户端改就够了」的说明。
//
// 做成可选接口而不是加进 Lister：老调用方（单测的假实现）不必被迫实现它，
// 没有它也能工作（只是退回整份读取）。
type Presigner interface {
	PresignGet(ctx context.Context, token, space, path string) (string, error)
}

// Config 是一次挂载所需的全部参数。
type Config struct {
	// RootPath 是本地同步根目录（必须已存在）。
	RootPath string
	// Space 是控制面空间类型：drive.SpacePersonal / drive.SpaceShared。
	Space string
	// Client 是控制面客户端（生产传 *drive.Client）。
	Client Lister
	// Token 取当前令牌。
	Token TokenFunc
	// ProviderName 是出现在系统界面里的提供方名（如「EasyShare 网盘」）。
	ProviderName string
	// ProviderID 是提供方的稳定 GUID。
	//
	// **个人空间与共享空间必须用不同 GUID**：否则系统会把两个同步根
	// 当成同一个提供方的两个根，注销时可能互相影响。
	ProviderID windows.GUID

	// FetchTimeout 是单次拉取上限；0 用 cfapi 默认值。
	FetchTimeout time.Duration
	// State 仅为可选增量视图；可写挂载仍以本轮服务器全量为安全依据。
	State *ListState
	// Writable 仅在接入条件写回及保护同步后设置。
	Writable bool
	// CacheIdleFor 为自动回收空闲期；0 使用五分钟。
	CacheIdleFor time.Duration
}

func modifiedUnixOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// Entries 把控制面对象列表转成 cfapi 的清单条目。
//
// 显式 Directory 对象（包括空目录）与路径推导父目录统一去重。
// 文件身份只接受有效 FileId；无效文件在本兼容入口跳过，List 会返回错误。
// 此入口保持只读；可写投影由 List(Config{Writable: true}) 显式开启。
func Entries(objects []drive.Object) []cfapi.CloudEntry {
	return entries(objects, false)
}

// entries 对无有效 FileId 的文件不生成占位符；List 会先返回明确错误。
func entries(objects []drive.Object, writable bool) []cfapi.CloudEntry {
	out := make([]cfapi.CloudEntry, 0, len(objects))
	seenDir := map[string]bool{}

	for _, o := range objects {
		if !o.Directory && o.FileId <= 0 {
			continue
		}
		// 补齐父目录；显式目录自身也参与同一去重集合。
		dirs := ancestorDirs(o.Path)
		if o.Directory {
			dirs = append(dirs, o.Path)
		}
		// 先补齐该文件路径上的所有父目录（去重）
		for _, dir := range dirs {
			if seenDir[dir] {
				continue
			}
			seenDir[dir] = true
			out = append(out, cfapi.CloudEntry{
				Path:  dir,
				IsDir: true,
				// 目录没有自己的 mtime（对象存储里不存在这个对象）——
				// 用 0 让 cfapi 走「未知时间」分支，而不是编一个假时间。
				ModifiedUnix: 0,
				// 显式和隐式目录统一使用路径派生身份，避免目录被补账
				// 或清单顺序变化时切换身份；目录无需文件 hydration。
				Identity: dirIdentity(dir),
				// 只读：见下方清单条目处的说明，目录同理
				ReadOnly: !writable,
			})
		}
		if o.Directory {
			continue
		}
		out = append(out, cfapi.CloudEntry{
			Path: o.Path,
			Size: o.Size,
			// 零时间要落成 0（cfapi 的「未知」哨兵），不能直接给 time.Time{}.Unix()：
			// 那是 -62135596800，换算成 FILETIME 会变成负数，
			// 内核直接拒绝建占位符（0x80070057）。增量事件流只带身份不带时间，
			// 所以「没有修改时间」是常态而不是异常。
			ModifiedUnix: modifiedUnixOf(o.LastModified),
			IsDir:        false,
			Identity:     fileIdentity(o),
			// **整片只读**：cfapi 只注册 FETCH_DATA / CANCEL_FETCH_DATA，没有写回调——
			// 用户对占位符的编辑不会被上报。若不置只读，用户会得到
			// 「保存成功、云端不知道、下次重建被覆盖」的结果（实测见
			// internal/cfapi/write_probe_windows_test.go 的 TestResyncSameRoot）。
			// 置位后写入得到明确的「拒绝访问」，且**不影响按需读取**（hydration 已实测正常）。
			// 双向同步（切片 5b）落地时要连同写回调一起放开这里。
			ReadOnly: !writable,
		})
	}
	return out
}

// List 产出 cfapi 需要的 ListFunc。
//
// 传了 `Config.State` 且客户端支持增量时走**增量列举**（路线图切片 2 的第二个生产落点）：
// 占位符同步根每 5 分钟对一次账，若每次都要全量清单，万级空间就是一次全量列举。
// 钉盘那套「空间版本 + 变更流水」的核心价值就在这里——第一次全量拿底，
// 之后只问「那个号之后发生了啥」。
//
// 没传 State、或客户端/旧控制面不支持增量时，行为与引入增量之前完全一致（每次全量）。
func List(cfg Config) cfapi.ListFunc {
	return func(ctx context.Context) ([]cfapi.CloudEntry, error) {
		token := cfg.Token()
		if token == "" {
			// 未登录不该让挂载点显示成空目录——返回错误让上层决定卸载
			return nil, fmt.Errorf("cfmount: 未登录")
		}

		changes, canIncremental := cfg.Client.(ChangeLister)
		if cfg.Writable || cfg.State == nil || !canIncremental {
			objects, err := cfg.Client.Objects(ctx, token, cfg.Space)
			if err != nil {
				return nil, fmt.Errorf("cfmount: 列举空间 %s 失败: %w", cfg.Space, err)
			}
			return projectEntries(objects, cfg.Writable)
		}

		base, since := cfg.State.snapshot()
		var sincePtr *int64
		// 只有**手里有底**（上一次的全量视图还在）时才允许走增量：
		// 增量流只描述差异，没有底就拼不出完整视图，其余对象会**静默消失**
		// ——这条判据是 internal/spacedav 在真机上踩出来的（见其 objects() 同处注释）。
		if since > 0 && base != nil {
			sincePtr = &since
		}

		result, err := changes.Changes(ctx, token, cfg.Space, sincePtr)
		if err != nil {
			// 增量失败不代表空间读不到：旧控制面可能还没有 /changes。
			// 退回全量，并让 State 重新以全量为底，行为与未引入增量时一致。
			objects, listErr := cfg.Client.Objects(ctx, token, cfg.Space)
			if listErr != nil {
				return nil, fmt.Errorf("cfmount: 列举空间 %s 失败: %w", cfg.Space, listErr)
			}
			cfg.State.set(objects, 0)
			return projectEntries(objects, cfg.Writable)
		}
		view := result.Files
		if sincePtr != nil && result.Files == nil {
			view = drive.ApplyChanges(base, result.Changes)
		}
		out, err := projectEntries(view, cfg.Writable)
		if err != nil {
			return nil, err
		}
		cfg.State.set(view, result.NewestSpaceVersion.Int64())
		return out, nil
	}
}

// projectEntries 不给缺失文件身份的对象制造无法读回的占位符。
func projectEntries(objects []drive.Object, writable bool) ([]cfapi.CloudEntry, error) {
	for _, o := range objects {
		if !o.Directory && o.FileId <= 0 {
			return nil, fmt.Errorf("cfmount: 文件 %q 缺少有效 FileId，无法创建可水合占位符", o.Path)
		}
	}
	return entries(objects, writable), nil
}

// ChangeLister 是**可选**能力：支持增量列举的客户端（`*drive.Client` 满足）。
//
// 与 Presigner 同一模式做成可选接口：单测里的假 Lister 不必被迫实现它，
// 缺它也只是退回全量列举。
type ChangeLister interface {
	Changes(ctx context.Context, token, space string, sinceVersion *int64) (*drive.Changes, error)
}

// ListState 是增量列举的跨调用状态：上一次拿到的完整视图 + 读到的空间版本。
//
// 为什么要显式传进来而不是包内全局：同一进程可能同时挂个人空间与共享空间，
// 两者的清单与游标绝不能互相串。
//
// 版本只存在内存里，刻意不落盘：跨进程重启后第一次必然是全量（底没了），
// 落盘的版本号在那一刻用不上；而**与 WebDAV 挂载共用一个版本文件是危险的**——
// 两个消费方各自维护视图，共用会让一方把另一方的游标推前，
// 另一方再去要增量就漏掉中间的事件，症状是「云端有文件、占位符里没有」且不报错。
type ListState struct {
	mu      sync.Mutex
	objects []drive.Object
	version int64
}

// NewListState 建一个空的增量状态（第一次列举会走全量拿底）。
func NewListState() *ListState { return &ListState{} }

// snapshot 返回上一次的完整视图与空间版本；objects 为 nil 表示还没有底。
func (s *ListState) snapshot() ([]drive.Object, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects, s.version
}

// set 用新的完整视图与空间版本覆盖状态。
//
// version 传 0 表示「这次是全量兜底，没有可信的增量游标」——下次仍走全量拿底。
func (s *ListState) set(objects []drive.Object, version int64) {
	s.mu.Lock()
	s.objects = objects
	s.version = version
	s.mu.Unlock()
}

// Open 产出 cfapi 需要的 OpenFunc：**只取被读的那段区间**。
//
// 为什么能做到：控制面签发的预签名 URL 只签 bucket + key
// （`DriveStorage.presignGetAt` 没把任何请求头纳入签名），SigV4 的语义是
// 「签名时未列出的头不参与校验」——所以客户端可以自由加 `Range` 头。
// 2026-10-18 活栈实测（`scripts/diag/presign-range-probe.mjs`）：3 MB 对象请求
// `bytes=1000000-1000010` 回 **206 / 11 字节**，`content-range` 与对象总长都对。
//
// 服务端忽略 Range 回 200 时不会出错：`httprange` 会整份读再切区间（只是慢），
// 语义不变。没有 `Presigner` 的客户端（含单测假实现）直接走 `OpenWholeObject`。
//
// 历史：这里曾长期写着「每次取整份对象、缺陷根在控制面接口」——那是**修完之后
// 没删的旧注释**，与下面五行代码矛盾。改注释前请先看 `internal/cfmount/README.md` §1。
func Open(cfg Config) cfapi.OpenFunc {
	// 若客户端支持预签名，走**带 Range 的直取**（只下需要的区间）。
	if p, ok := cfg.Client.(Presigner); ok {
		return func(ctx context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
			token := cfg.Token()
			if token == "" {
				return nil, fmt.Errorf("cfmount: 未登录")
			}
			// 每次现取 URL：预签名有过期时间，缓存会拿到失效链接。
			rf := httprange.NewFetcher(func(ctx context.Context) (string, error) {
				return p.PresignGet(ctx, token, cfg.Space, cloudPath)
			})
			return rf.Fetch(ctx, offset, length)
		}
	}
	// 否则退回整份读取（老客户端/假实现）。
	return OpenWholeObject(cfg)
}

// OpenWholeObject 是**不依赖 Range** 的读取实现：整份下载后在内存里切区间。
//
// 保留它是为了两件事：
//  1. 没有 Presigner 的客户端（含单测假实现）仍能工作；
//  2. Range 路径出问题时可作为对照与回退点。
//
// 代价见函数名：读 1 GB 文件里的 1 字节也要下 1 GB。
func OpenWholeObject(cfg Config) cfapi.OpenFunc {
	return func(ctx context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
		token := cfg.Token()
		if token == "" {
			return nil, fmt.Errorf("cfmount: 未登录")
		}
		rc, _, err := cfg.Client.Open(ctx, token, cfg.Space, cloudPath)
		if err != nil {
			return nil, fmt.Errorf("cfmount: 打开 %s 失败: %w", cloudPath, err)
		}
		// 整份读进内存再切区间：见函数注释里说明的缺陷与修复方向。
		all, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("cfmount: 读取 %s 失败: %w", cloudPath, err)
		}
		if offset < 0 || offset > int64(len(all)) {
			return nil, fmt.Errorf("cfmount: %s 的偏移 %d 越界（大小 %d）", cloudPath, offset, len(all))
		}
		end := offset + length
		if end > int64(len(all)) {
			end = int64(len(all))
		}
		return io.NopCloser(bytes.NewReader(all[offset:end])), nil
	}
}

// ancestorDirs 返回路径上的所有祖先目录（不含自身），按由浅到深排列。
//
// 例："a/b/c.txt" → ["a", "a/b"]。
func ancestorDirs(p string) []string {
	norm := normalizeSlashes(p)
	var out []string
	for i := 0; i < len(norm); i++ {
		if norm[i] == '/' {
			out = append(out, norm[:i])
		}
	}
	return out
}

// fileIdentity 返回一个文件的占位符身份。
//
// **必须防 fileId=0 的碰撞**：控制面的 `fileId` 是装箱类型，在「存储里出现
// 不合规路径（历史脏数据）」时会返回 **null**（见 DriveFileService.list 的
// reconcileAndMap 私有步骤的 catch 分支），Go 侧反序列化后就是 **0**。
// 若直接 `EncodeIdentity(0)`，
// 所有这类文件会共享身份 `esf:0`——fetch 回调按身份反查路径时会全部指错，
// 表现为「打开 A 文件读到 B 的内容」，是静默的数据错误。
//
// fileId 不可用时返回 nil；不得使用无法被 DecodeIdentity 解析的 esp: 伪身份。
// entries 跳过此类文件，List 对整份清单报错，防止制造永远无法水合的占位符。
func fileIdentity(o drive.Object) []byte {
	if o.FileId > 0 {
		return cfapi.EncodeIdentity(o.FileId.Int64())
	}
	return nil // 无法 DecodeIdentity 的伪身份不能用于 hydration。
}

// dirIdentity 为目录派生一个身份字节。
//
// 用固定前缀 + 路径的哈希，而非路径明文：身份会被写进占位符，
// 明文路径会让「改名」在系统侧看起来像换了个文件（虽然目录身份本就没有
// fileId 支撑，但至少不要让路径明文外泄到系统元数据里）。
func dirIdentity(dir string) []byte {
	return []byte("esd:" + shortHash(dir))
}
