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
}

// Entries 把控制面对象列表转成 cfapi 的清单条目。
//
// 两个转换要点：
//  1. **目录要自己补**：控制面返回的是扁平对象列表，只有文件（对象存储里没有目录）。
//     而 CfApi 建树需要显式的目录条目——否则逐层建树时找不到父目录。
//     本函数从文件路径反推出所有中间目录。
//  2. **身份用 fileId 编码**：cfapi.EncodeIdentity(fileId)。
//     占位符身份必须稳定且与路径无关（改名后仍能反查），fileId 满足这一点。
//     见 ADR-0001 的文件身份模型。
func Entries(objects []drive.Object) []cfapi.CloudEntry {
	out := make([]cfapi.CloudEntry, 0, len(objects))
	seenDir := map[string]bool{}

	for _, o := range objects {
		// 先补齐该文件路径上的所有父目录（去重）
		for _, dir := range ancestorDirs(o.Path) {
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
				// 目录没有 fileId（控制面的 es_file 目录层尚未覆盖目录对象，
				// 见路线图切片 4「目录对象」）。用路径派生一个**看起来稳定**的
				// 身份：同一路径在同一会话内一致即可，不承诺跨会话稳定。
				Identity: dirIdentity(dir),
				// 只读：见下方清单条目处的说明，目录同理
				ReadOnly: true,
			})
		}
		out = append(out, cfapi.CloudEntry{
			Path:         o.Path,
			Size:         o.Size,
			ModifiedUnix: o.LastModified.Unix(),
			IsDir:        false,
			Identity:     fileIdentity(o),
			// **整片只读**：cfapi 只注册 FETCH_DATA / CANCEL_FETCH_DATA，没有写回调——
			// 用户对占位符的编辑不会被上报。若不置只读，用户会得到
			// 「保存成功、云端不知道、下次重建被覆盖」的结果（实测见
			// internal/cfapi/write_probe_windows_test.go 的 TestResyncSameRoot）。
			// 置位后写入得到明确的「拒绝访问」，且**不影响按需读取**（hydration 已实测正常）。
			// 双向同步（切片 5b）落地时要连同写回调一起放开这里。
			ReadOnly: true,
		})
	}
	return out
}

// List 产出 cfapi 需要的 ListFunc。
//
// 每次调用都问控制面要全量清单——对齐现状（控制面无增量接口），
// 增量是路线图切片 2 的事。
func List(cfg Config) cfapi.ListFunc {
	return func(ctx context.Context) ([]cfapi.CloudEntry, error) {
		token := cfg.Token()
		if token == "" {
			// 未登录不该让挂载点显示成空目录——返回错误让上层决定卸载
			return nil, fmt.Errorf("cfmount: 未登录")
		}
		objects, err := cfg.Client.Objects(ctx, token, cfg.Space)
		if err != nil {
			return nil, fmt.Errorf("cfmount: 列举空间 %s 失败: %w", cfg.Space, err)
		}
		return Entries(objects), nil
	}
}

// Open 产出 cfapi 需要的 OpenFunc。
//
// ⚠️ **当前实现每次都取整份对象、在内存里切区间**。原因：控制面的预签名 GET
// 不带 Range 参数，而 CfApi 的回调要的是任意区间。
//
// 代价：读一个 1 GB 文件里的 1 字节，也要下载 1 GB。这对小文件无感，
// 对常见文档（几 MB）可接受，**但对大文件是明确的性能缺陷**。
//
// 正确的做法有两条（都留给后续切片）：
//  1. 控制面支持签发**带 Range 的预签名 GET**（S3 的 presignGetObject 可以
//     通过 getObjectRequest().range(...) 表达）——最直接；
//  2. 或客户端直接对存储发 Range 请求（需要绕过预签名，不可行）。
//
// 所以这条缺陷的**根在控制面接口**，修的时候要从 platform-drive 改起。
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
// 因此 fileId 不可用时退回**按路径派生**的身份（前缀 `esp:` 与 fileId 的 `esf:`
// 区分开，便于日志里一眼看出是哪一类）。代价是这类身份不跨会话稳定——
// 但它们本来就是脏数据兜底路径，且下次列表补账成功后会拿到真 fileId。
func fileIdentity(o drive.Object) []byte {
	if o.FileId > 0 {
		return cfapi.EncodeIdentity(o.FileId.Int64())
	}
	return []byte("esp:" + shortHash(o.Path))
}

// dirIdentity 为目录派生一个身份字节。
//
// 用固定前缀 + 路径的哈希，而非路径明文：身份会被写进占位符，
// 明文路径会让「改名」在系统侧看起来像换了个文件（虽然目录身份本就没有
// fileId 支撑，但至少不要让路径明文外泄到系统元数据里）。
func dirIdentity(dir string) []byte {
	return []byte("esd:" + shortHash(dir))
}
