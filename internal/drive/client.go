// Package drive 是桌面端访问「个人云盘」的客户端，走账号控制面（RuoYi）的存储授权接口。
//
// 与 internal/cloud 的区别：cloud 用编译期静态凭据直连 RustFS（KI-2），本包不持任何
// 存储凭据，只拿控制面按登录用户签发的短期预签名 URL，再直传/直取对象存储。
// 用户命名空间前缀由控制面强制，客户端只认相对路径（见 ADR-0007 不变量 1、2）。
package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// pcClientID 是 RuoYi 内置 PC 客户端标识，与 internal/account 保持一致。
const pcClientID = "e5cd7e4891bf95d1d19206ce24a7b32e"

// 空间类型。客户端只能指定「个人还是共享」，真实的对象键前缀由控制面按权限产出——
// 客户端拼不出前缀，也就跨不到别的空间。
const (
	SpacePersonal = "personal"
	SpaceShared   = "shared"
)

// Object 是云盘中的一个对象。Path 是相对路径，用户命名空间对客户端不可见。
// FileId 是控制面 es_file 目录层的稳定身份（2026-09-06 起），回收站/版本/
// 分享重命名稳定都挂它；存量对象首次列举时由控制面惰性补账，亦有值。
type Object struct {
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified"`
	// FileId 用 FlexInt64：RuoYi 的 JacksonConfig 给所有 Long 注册了
	// BigNumberSerializer——**超出 JS 安全整数（2^53-1）时序列化成字符串**，
	// 未超出时仍是数字。雪花 ID 必然超范围，所以真实响应长这样：
	//
	//	"fileId":"2110952103974817794"
	//
	// 直接声明 int64 会让 encoding/json **整个解析失败**
	// （json: cannot unmarshal string into ... of type int64），
	// 后果是 Objects() 返回 error → 文件列表 /「此电脑」挂载盘 / cfmount /
	// 快搜文件路**全部拿不到数据**。2026-10-16 真机联调首次暴露。
	FileId FlexInt64 `json:"fileId"`
	// VersionId 是该文件当前的版本号（路线图切片 1 的 CAS）。
	//
	// 客户端**下次写同一路径时应当把它带回**（PresignPut 的 expectedVersion）：
	// 服务端比对不上就拒绝签发，从而避免「两个客户端先后写同一路径，
	// 后者静默覆盖前者」——2026-10-16 实测确认过那个行为。
	VersionId FlexInt64 `json:"versionId"`
	// ContentMd5 是该文件的内容 MD5（切片 3 冲突判定 + 切片 6 秒传）。
	// 未上报（存量行/旧客户端上传）为空串。
	ContentMd5 string `json:"contentMd5"`
	// CreatedAt 是首次登记时间（es_file.create_time）。
	//
	// 用指针而非 time.Time：控制面对「存量未登记对象」返回 null，
	// 值类型接 null 会解析失败；指针能区分「没有这个信息」与「零值时间」。
	CreatedAt *time.Time `json:"createdAt"`
	// UploadByName 是上传者显示名（控制面批量 join sys_user，nickName 优先）。
	// 未登记行或用户已删为空串，界面按「没有就不显示」处理。
	UploadByName string `json:"uploadByName"`
}

// FlexInt64 是能同时接受 **JSON 数字与 JSON 字符串**的 int64。
//
// 为什么需要它：控制面（RuoYi）对 Long 的序列化是**两态**的——
// 值在 JS 安全整数范围内发数字，超出则发字符串。雪花 ID 与配额字节数
// 几乎总是超范围，于是同一个字段在不同响应里可能是两种形态；
// 而 Go 的 encoding/json 遇到类型不匹配会**让整个 Unmarshal 失败**，
// 不是跳过该字段——一个 ID 字段的类型问题会打掉整份列表。
//
// 因此这里两种都收：数字走 json.Number，字符串解析成整数。
// 空字符串/null 视为 0（对应服务端「未登记」的语义，见 cfmount 对 fileId=0 的处理）。
type FlexInt64 int64

// UnmarshalJSON 实现 json.Unmarshaler。
func (v *FlexInt64) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" || s == `""` {
		*v = 0
		return nil
	}
	// 字符串形式：去掉引号再解析
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		if s == "" {
			*v = 0
			return nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("解析整数字段失败（值 %s）：%w", s, err)
	}
	*v = FlexInt64(n)
	return nil
}

// Int64 取原始 int64（调用方读值用，避免到处显式转换）。
func (v FlexInt64) Int64() int64 { return int64(v) }

// Client 连接控制面的存储授权接口。baseURL 形如 http://localhost:8090。
type Client struct {
	baseURL string
	http    *http.Client
	// transfer 用于直传/直取对象存储：大文件耗时远超控制面调用，故单独放宽超时。
	transfer *http.Client

	// Sessions 大文件会话持久化（Multipart 断点续传）。nil 时大文件走
	// 单请求上传，行为与 2026-09-06 之前一致；生产装配见 app.go driveClient。
	Sessions *SessionStore
}

// SetTransferTransportForTest 替换直传/直取用的传输层（**仅供测试**）。
//
// 为什么需要它：有些性质只在「重试真的发生」时才成立——
// 例如「重试必须复用已签发的 URL，否则版本会被虚增」。
// 没有注入点时，happy path 第一次就成功，那条断言永远走不到
// （第一版用例正是如此：回退成错误的实现，它依然通过）。
func (c *Client) SetTransferTransportForTest(rt http.RoundTripper) {
	c.transfer = &http.Client{Timeout: c.transfer.Timeout, Transport: rt}
}

// New 创建云盘客户端。
func New(baseURL string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		http:     &http.Client{Timeout: 15 * time.Second},
		transfer: &http.Client{Timeout: 30 * time.Minute},
	}
}

// ruoyiResp 是 RuoYi 统一响应包装。
type ruoyiResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// presignData 是预签名接口的返回体。
//
// Deduped 是秒传命中标记（切片 6）：true 表示服务端已用既有对象复制到目标键，
// url 为空、客户端跳过字节上传。
type presignData struct {
	URL     string `json:"url"`
	Path    string `json:"path"`
	Deduped bool   `json:"deduped"`
}

// Objects 列举指定空间内的对象。space 传空按个人空间。
func (c *Client) Objects(ctx context.Context, token, space string) ([]Object, error) {
	return c.objects(ctx, token, space, false)
}

// ObjectsWithDirs 与 Objects 相同，但包含目录标记对象（路径以 / 结尾的 0 字节占位）。
//
// 只有网盘页用这个变体：它需要把空文件夹显示出来。挂载盘 / WebDAV / 快搜
// 继续走 Objects——它们各有既定目录语义，多一种 0 字节「文件」只会造成混乱。
// 目录标识就是路径尾斜杠，服务端没有单独的 isDir 字段（前端 driveFolder.ts
// 同款判断），两侧共用一套规则。
func (c *Client) ObjectsWithDirs(ctx context.Context, token, space string) ([]Object, error) {
	return c.objects(ctx, token, space, true)
}

func (c *Client) objects(ctx context.Context, token, space string, includeDirs bool) ([]Object, error) {
	var objects []Object
	endpoint := "/easyshare/drive/objects?space=" + url.QueryEscape(normalizeSpace(space))
	if includeDirs {
		endpoint += "&includeDirs=true"
	}
	if err := c.call(ctx, http.MethodGet, endpoint, token, nil, &objects); err != nil {
		return nil, err
	}
	return objects, nil
}

// Mkdir 新建文件夹（目录标记对象，路径以 / 结尾的 0 字节占位）。
//
// path 是目录的空间内相对路径（不含首尾斜杠）。重复创建同一路径幂等成功，
// 与本地文件系统行为对齐。目录没有 es_file 行，故没有 fileId；
// 删除目录即删标记对象（控制面软删找不到行时直接删对象）。
func (c *Client) Mkdir(ctx context.Context, token, space, path string) (string, error) {
	body, err := json.Marshal(map[string]string{"path": path, "space": normalizeSpace(space)})
	if err != nil {
		return "", err
	}
	var created struct {
		Path string `json:"path"`
	}
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/mkdir", token, body, &created); err != nil {
		return "", err
	}
	return created.Path, nil
}

// 变更事件类型（与服务端 EsFileChangeLog 的 event_type 一一对应）。
const (
	ChangeCreated  = "created"
	ChangeUpdated  = "updated"
	ChangeDeleted  = "deleted"
	ChangeRestored = "restored"
	ChangeMoved    = "moved"
	ChangePurged   = "purged"
)

// Change 是一次文件生命周期变更（增量列举，路线图切片 2）。
type Change struct {
	SpaceVersion FlexInt64 `json:"spaceVersion"`
	Event        string    `json:"event"`
	FileId       FlexInt64 `json:"fileId"`
	Path         string    `json:"path"`
	OldPath      string    `json:"oldPath,omitempty"`
	// Size / LastModified 是服务端为该 active 行补的元数据。
	// 变更日志本身只记身份；没有它们，挂载盘增量合并会得到 0 字节占位对象。
	Size int64 `json:"size"`
	// 服务端返回 LocalDateTime 形如 `2026-10-17 01:43:23`（无 T、无时区）。
	// time.Time 直接解析 RFC3339 会失败；先用字符串收，再由 accessor 转时间。
	RawLastModified string `json:"lastModified"`
	lastModified    time.Time
}

// LastModified 返回服务端补的修改时间；无法解析时返回零值（挂载盘退回全量校准）。
func (c Change) LastModified() time.Time {
	if !c.lastModified.IsZero() || c.RawLastModified == "" {
		if c.RawLastModified == "" {
			return time.Time{}
		}
		v, err := time.ParseInLocation("2006-01-02 15:04:05", c.RawLastModified, time.Local)
		if err != nil {
			return time.Time{}
		}
		c.lastModified = v
	}
	return c.lastModified
}

// Changes 是增量列举结果。
//
// Files 与 Changes 互斥：不带 sinceVersion 时服务端返回全量重建（Files 非空）；
// 带了则只返回差异流（Changes 非空）。NewestSpaceVersion 是下次增量应带的版本号。
type Changes struct {
	NewestSpaceVersion FlexInt64 `json:"newestSpaceVersion"`
	SinceVersion       *int64    `json:"sinceVersion"`
	Files              []Object  `json:"files"`
	Changes            []Change  `json:"changes"`
}

// Changes 增量列举（路线图切片 2）。
//
// sinceVersion == nil 表示全量重建：返回当前完整文件列表 + newestSpaceVersion。
// 客户端持久化 newestSpaceVersion 后，下次传入它即可只拉变更。
func (c *Client) Changes(ctx context.Context, token, space string, sinceVersion *int64) (*Changes, error) {
	endpoint := "/easyshare/drive/changes?space=" + url.QueryEscape(normalizeSpace(space))
	if sinceVersion != nil {
		endpoint += "&sinceVersion=" + strconv.FormatInt(*sinceVersion, 10)
	}
	var result Changes
	if err := c.call(ctx, http.MethodGet, endpoint, token, nil, &result); err != nil {
		return nil, fmt.Errorf("获取增量变更失败：%w", err)
	}
	return &result, nil
}

// PresignPut 取上传用预签名 URL（不带并发校验，见 PresignPutIfVersion）。
//
// size 必须是真实字节数：控制面按它判配额，只有签发这一刻能拦住写入（签出后字节直传对象
// 存储，控制面不在数据路径上）。size 为负（长度未知）时按 0 上报，配额只能拦住已经写满的
// 空间，拦不住这一次。
func (c *Client) PresignPut(ctx context.Context, token, space, path string, size int64) (string, error) {
	return c.PresignPutIfVersion(ctx, token, space, path, size, nil)
}

// UploadResult 是一次上传的结果（切片 6 起秒传可能跳过字节上传）。
type UploadResult struct {
	// Deduped 为 true 表示服务端判定内容已存在（同空间同 MD5），
	// 本次上传没有传输任何字节——进度回调会一次性跳到 100%。
	Deduped bool
}

// PresignPutIfVersion 与 PresignPut 相同，但带上乐观并发校验（路线图切片 1 的 CAS）。
//
// expectedVersion 是调用方看到的那一版（通常来自 Objects() 的 VersionId）：
//   - nil → 不校验，行为与 PresignPut 完全一致（旧调用方/新建场景）；
//   - 0   → 「我认为这是首次登记」；服务端若已有该路径则拒绝；
//   - n>0 → 服务端当前版本不是 n 则拒绝。
//
// 为什么需要它：没有校验时，两个客户端先后写同一路径是静默覆盖——后写的赢，
// 先写的内容连同任何提示一起消失（2026-10-16 真机实测确认）。
// 服务端拒绝时错误信息里带当前版本，供上层提示「已被他人修改，请刷新后重试」。
func (c *Client) PresignPutIfVersion(ctx context.Context, token, space, path string,
	size int64, expectedVersion *int64) (string, error) {
	url, _, err := c.presignPutFull(ctx, token, space, path, size, expectedVersion, "")
	return url, err
}

// presignPutFull 是 presign-put 的完整调用（CAS + 秒传哈希一起带）。
//
// 返回 URL 与秒传标记：deduped=true 时 URL 为空、调用方跳过字节上传。
// contentMd5 为空串时不带该字段（服务端不判秒传，行为与旧调用方一致）。
func (c *Client) presignPutFull(ctx context.Context, token, space, path string,
	size int64, expectedVersion *int64, contentMd5 string) (string, bool, error) {
	if size < 0 {
		size = 0
	}
	payload := map[string]any{
		"path":  path,
		"space": normalizeSpace(space),
		"size":  size,
	}
	// 只在给了期望版本时才带这个字段：不带给服务端「不做校验」的语义，
	// 从而保证新旧客户端行为一致（升级不必同时上线）。
	if expectedVersion != nil {
		payload["expectedVersion"] = *expectedVersion
	}
	if contentMd5 != "" {
		payload["contentMd5"] = contentMd5
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	var data presignData
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/presign-put", token, body, &data); err != nil {
		return "", false, err
	}
	if data.Deduped {
		return "", true, nil
	}
	if data.URL == "" {
		return "", false, fmt.Errorf("控制面未返回预签名地址")
	}
	return data.URL, false, nil
}

// PresignGet 取下载用预签名 URL。
func (c *Client) PresignGet(ctx context.Context, token, space, path string) (string, error) {
	return c.presign(ctx, token, "/easyshare/drive/presign-get", space, path)
}

// Delete 删除指定空间内的对象（按路径，过渡期兼容）。
func (c *Client) Delete(ctx context.Context, token, space, path string) error {
	body, err := json.Marshal(map[string]string{"path": path, "space": normalizeSpace(space)})
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodDelete, "/easyshare/drive/object", token, body, nil)
}

// DeleteTree 递归删除目录子树：目录内每个文件各自软删进回收站，
// 内部目录标记对象一并清掉。path 必须是带尾斜杠的目录键（服务端只认这种形态）。
//
// 与 Delete 的分工：Delete 只动路径自身（对目录即只清标记对象，子文件不受影响），
// 递归语义必须显式声明——否则「删一个文件夹」在两个调用方眼里含义不同，
// 挂载盘/WebDAV 走的正是非递归那条路。
func (c *Client) DeleteTree(ctx context.Context, token, space, path string) error {
	body, err := json.Marshal(map[string]any{
		"path": path, "space": normalizeSpace(space), "recursive": true,
	})
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodDelete, "/easyshare/drive/object", token, body, nil)
}

// DeleteByFileID 按稳定 fileId 删除（切片 1：写路径改走身份，不再以路径为主键）。
//
// 为什么优先用 fileId：路径只是属性，改名/移动后它变了而身份不变。
// 按路径删会遇到「删之前别人把另一个文件改名到这个路径」——删掉的是别人的文件。
// 服务端 resolveDeletePath 对 fileId 做归属校验（必须属于当前空间与 owner），
// 因此这个键也不可用于越权删除。
//
// fileID 为 0（目录层未登记/脏数据）时退回按路径删。
func (c *Client) DeleteByFileID(ctx context.Context, token, space string, fileID int64, path string) error {
	payload := map[string]any{"space": normalizeSpace(space)}
	if fileID > 0 {
		payload["fileId"] = fileID
	}
	if path != "" {
		payload["path"] = path
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodDelete, "/easyshare/drive/object", token, body, nil)
}

// Move 改名/移动：服务端复制 + 删源，字节不出对象存储。
//
// 与旧做法（客户端「下载 + 上传 + 删除」）的区别不只是快：旧做法会**换掉 fileId**
// （上传到新键 = 新登记），分享链接、任务引用、知识索引全部失效；
// 服务端 move 只改路径属性，**fileId 不变**。
func (c *Client) Move(ctx context.Context, token, space, from, to string) error {
	body, err := json.Marshal(map[string]string{
		"from": from, "to": to, "space": normalizeSpace(space),
	})
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/easyshare/drive/move", token, body, nil)
}

// Star 设置或取消星标（starred=false 为取消）。
func (c *Client) Star(ctx context.Context, token, space, path string, starred bool) error {
	body, err := json.Marshal(map[string]any{
		"path": path, "starred": starred, "space": normalizeSpace(space),
	})
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/easyshare/drive/star", token, body, nil)
}

// Stars 取当前空间下该用户的星标路径集合。
//
// 只回路径不回文件信息：星标是在列表里打标的辅助视图，
// 列表本身已经带完整对象，前端按路径匹配即可。
func (c *Client) Stars(ctx context.Context, token, space string) ([]string, error) {
	var paths []string
	endpoint := "/easyshare/drive/stars?space=" + url.QueryEscape(normalizeSpace(space))
	if err := c.call(ctx, http.MethodGet, endpoint, token, nil, &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

// Touch 记录一次最近访问。
//
// 埋点语义：调用方可以吞掉错误——记录「最近」失败不该阻断用户正在做的打开/下载。
func (c *Client) Touch(ctx context.Context, token, space, path string) error {
	body, err := json.Marshal(map[string]string{
		"path": path, "space": normalizeSpace(space),
	})
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/easyshare/drive/touch", token, body, nil)
}

// Recents 取最近访问路径（时间倒序）。
func (c *Client) Recents(ctx context.Context, token, space string, limit int) ([]string, error) {
	var paths []string
	endpoint := "/easyshare/drive/recents?space=" + url.QueryEscape(normalizeSpace(space)) +
		"&limit=" + strconv.Itoa(limit)
	if err := c.call(ctx, http.MethodGet, endpoint, token, nil, &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

// normalizeSpace 兜空空间名。空字符串按个人空间——旧调用方不传时行为不变。
func normalizeSpace(space string) string {
	if space == "" {
		return SpacePersonal
	}
	return space
}

// Upload 换取预签名 URL 后把内容直传对象存储。size 为负表示长度未知。
//
// 不带 CAS（等价于 expectedVersion=nil）。要防「静默覆盖他人修改」用 UploadIfVersion。
func (c *Client) Upload(ctx context.Context, token, space, path string, body io.Reader, size int64) error {
	_, err := c.upload(ctx, token, space, path, body, size, nil, "")
	return err
}

// UploadIfVersion 与 Upload 相同，但带乐观并发校验（路线图切片 1 的 CAS）。
//
// expectedVersion：nil 不校验、0 = 「我认为文件还不存在」、n>0 = 「我看到的是第 n 版」。
// 版本不匹配时**在签发阶段就被拒绝**，字节根本不会写入对象存储——
// 这是与「先写后比对」的关键差别：拒绝发生在数据路径之前。
func (c *Client) UploadIfVersion(ctx context.Context, token, space, path string,
	body io.Reader, size int64, expectedVersion *int64) error {
	_, err := c.upload(ctx, token, space, path, body, size, expectedVersion, "")
	return err
}

// upload 是单请求直传的内部实现；contentMd5 非空时随签发上报做秒传判定。
func (c *Client) upload(ctx context.Context, token, space, path string,
	body io.Reader, size int64, expectedVersion *int64, contentMd5 string) (UploadResult, error) {
	// 单请求上传固定从流的开头读，重试时必须能**重建** reader。
	bodyFn := func() io.Reader { return body }
	if seeker, ok := body.(io.Seeker); ok {
		bodyFn = func() io.Reader {
			_, _ = seeker.Seek(0, io.SeekStart)
			return body
		}
	}
	result, err := c.putOnceWithRetry(ctx, "上传到对象存储", token, space, path, size,
		expectedVersion, contentMd5, bodyFn)
	return result, err
}

// presignFn 每次尝试重新签发预签名 URL（仅分片路径需要，见 putOnceWithRetry 的说明）。
type presignFn func() (string, error)

// putOnceWithRetry 单请求直传，带**停滞检测 + 重试**。
//
// <p><b>为什么必须重试，而且必须复用同一个 URL</b>：
//
// <p>① **重试**：`transfer` 客户端的超时是 30 分钟（给整次传输的），
// 对一次 PUT 是上万倍余量。连接挂住时它会一直等到 30 分钟——
// 实测（2026-10-16，8 MiB 单请求，8 次里 1 次挂死）：
// 挂住的那次给 60 秒也不动，**没有任何自愈**。所以这层必须自己判停滞。
//
// <p>② **复用 URL**（关键，且容易写错）：控制面在**签发预签名时就登记并推进版本**
// （`registerOnPresign`）。实测确认：连着签发两次、一个字节都没写，
// 数据库里 `version_id` 已经变成 **2**。因此若重试时重新签发，
// 版本会被虚增，CAS 语义随之失真；更糟的是**旧 URL 仍在有效期内且指向同一对象**，
// 两条路径都能写，语义彻底乱掉。
// 所以重试**只重用第一次签发的 URL**：登记只发生一次，版本只推进一次。
//
// <p>`presignFn` 参数是留给分片路径的：分片的每个 part 各自有独立 URL、
// 且不做版本登记，重新签发是安全的。
func (c *Client) putOnceWithRetry(ctx context.Context, what, token, space, path string,
	size int64, expectedVersion *int64, contentMd5 string,
	bodyFn func() io.Reader) (UploadResult, error) {
	target, deduped, err := c.presignPutFull(ctx, token, space, path, size, expectedVersion, contentMd5)
	if err != nil {
		return UploadResult{}, err
	}
	if deduped {
		// 秒传命中：服务端已复制既有对象，字节路径整段跳过
		return UploadResult{Deduped: true}, nil
	}
	return UploadResult{}, c.putURLWithRetry(ctx, what, path, target, size, bodyFn)
}

// putURLWithRetry 针对一个**已签发的** URL 做「停滞检测 + 重试」。
//
// 与分片路径共用同一套判据：只要还有字节在流动就一直等，
// 彻底不动（uploadStall）才判连接死亡并重试——这样慢链路不会被误杀。
func (c *Client) putURLWithRetry(ctx context.Context, what, path, target string,
	size int64, bodyFn func() io.Reader) error {
	var lastErr error
	for attempt := 0; attempt < singlePutRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		guard := newStallReader(bodyFn(), uploadStall, cancelAttempt)
		req, buildErr := http.NewRequestWithContext(attemptCtx, http.MethodPut, target, guard)
		if buildErr != nil {
			guard.stop()
			cancelAttempt()
			return buildErr
		}
		// 预签名刻意不签 Content-Type，故这里不设该头，避免签名不匹配。
		if size >= 0 {
			req.ContentLength = size
		}
		resp, doErr := c.transfer.Do(req)
		guard.stop()
		if doErr != nil {
			cancelAttempt()
			lastErr = doErr
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cancelAttempt()
		if resp.StatusCode/100 != 2 {
			lastErr = fmt.Errorf("对象存储返回 %s", resp.Status)
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("上传未成功")
	}
	return fmt.Errorf("%s失败（%s，已重试 %d 次）：%w", what, path, singlePutRetries, lastErr)
}

// Open 换取预签名 URL 后打开对象内容，调用方负责关闭。
func (c *Client) Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error) {
	target, err := c.PresignGet(ctx, token, space, path)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("读取对象存储失败：%w", err)
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("下载 %s 失败：对象存储返回 %s", path, resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

// presign 调用预签名接口并取出 URL。
func (c *Client) presign(ctx context.Context, token, endpoint, space, path string) (string, error) {
	body, err := json.Marshal(map[string]string{"path": path, "space": normalizeSpace(space)})
	if err != nil {
		return "", err
	}
	return c.presignWith(ctx, token, endpoint, path, body)
}

// presignWith 用调用方备好的请求体调预签名接口并取出 URL。
func (c *Client) presignWith(ctx context.Context, token, endpoint, path string, body []byte) (string, error) {
	var data presignData
	if err := c.call(ctx, http.MethodPost, endpoint, token, body, &data); err != nil {
		return "", err
	}
	if data.URL == "" {
		return "", fmt.Errorf("控制面未返回预签名地址")
	}
	return data.URL, nil
}

// call 调控制面并解 RuoYi 统一响应；out 可为 nil。
func (c *Client) call(ctx context.Context, method, path, token string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("clientid", pcClientID)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接云盘服务失败：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var wrapped ruoyiResp
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return fmt.Errorf("云盘服务响应异常(HTTP %d)", resp.StatusCode)
	}
	// RuoYi 把业务错误也放在 HTTP 200 的 body 里，故必须看 code 而非 HTTP 状态。
	if wrapped.Code != 200 {
		msg := wrapped.Msg
		if msg == "" {
			msg = fmt.Sprintf("云盘服务返回 code=%d", wrapped.Code)
		}
		return fmt.Errorf("%s", msg)
	}
	if out != nil && len(wrapped.Data) > 0 {
		if err := json.Unmarshal(wrapped.Data, out); err != nil {
			return fmt.Errorf("解析云盘数据失败：%w", err)
		}
	}
	return nil
}

// RecycleEntry 是回收站里的一条记录（控制面 es_file 的 recycled 行）。
// OriginPath 是删除前的路径快照，还原落点；VersionId 是覆盖上传递增的版本号。
type RecycleEntry struct {
	// 与 Object.FileId 同理：超 JS 安全整数的 Long 会被序列化成字符串，
	// 用 FlexInt64 两种形态都收（否则整个回收站列表解析失败）。
	FileId     FlexInt64  `json:"fileId"`
	Path       string     `json:"path"`
	OriginPath string     `json:"originPath"`
	Size       FlexInt64  `json:"size"`
	VersionId  FlexInt64  `json:"versionId"`
	DeletedAt  *time.Time `json:"deletedAt"`
}

// RecycleEntryView 是回收站条目在**前端（JS）边界**上的形态。
//
// 为什么不能直接把 RecycleEntry 交给前端：`FileId` 是雪花 ID（19 位，
// 例如 2110992378747158530），**超出 JS 安全整数 2^53-1**。
// 以 JSON number 交给 JS 会被**静默截断**（实测：那个 ID 到前端变成
// 2110992378747158500，差 30），再传回来就查不到任何行。
//
// 实测本机库里 13 行真实 ID，经一次 JS number 往返后**13 行全部改变**
// （见 known-issues.md KI-21）。所以这里用**十进制字符串**——
// 这不是风格偏好，是「19 位整数在 JS 里只有字符串能无损表示」的硬约束。
// RuoYi 自己也正是因此把这些字段序列化成字符串。
type RecycleEntryView struct {
	FileId     string     `json:"fileId"`
	Path       string     `json:"path"`
	OriginPath string     `json:"originPath"`
	Size       int64      `json:"size"`
	VersionId  int64      `json:"versionId"`
	DeletedAt  *time.Time `json:"deletedAt"`
}

// View 转成前端形态（雪花 ID 转十进制字符串，保住精度）。
func (e RecycleEntry) View() RecycleEntryView {
	return RecycleEntryView{
		FileId:     strconv.FormatInt(e.FileId.Int64(), 10),
		Path:       e.Path,
		OriginPath: e.OriginPath,
		Size:       e.Size.Int64(),
		VersionId:  e.VersionId.Int64(),
		DeletedAt:  e.DeletedAt,
	}
}

// ParseFileID 把前端传来的十进制字符串还原成雪花 ID。
//
// 反向也必须走字符串：JS 里已经没有那个整数的精确值了，
// 收到 number 就等于收到一个**已经错了的值**。
func ParseFileID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("文件 ID 无效（%q）：应为十进制字符串", raw)
	}
	if id <= 0 {
		return 0, fmt.Errorf("文件 ID 必须为正数，实得 %d", id)
	}
	return id, nil
}

// RecycleList 列举回收站（state='recycled'，按删除时间倒序）。
func (c *Client) RecycleList(ctx context.Context, token, space string) ([]RecycleEntry, error) {
	var entries []RecycleEntry
	endpoint := "/easyshare/drive/recycle?space=" + url.QueryEscape(normalizeSpace(space))
	if err := c.call(ctx, http.MethodGet, endpoint, token, nil, &entries); err != nil {
		return nil, fmt.Errorf("获取回收站列表失败：%w", err)
	}
	return entries, nil
}

// RecycleRestore 还原回收站文件到原位置。原路径被同名 active 文件占用时
// 控制面返回可读冲突错误（此处按 message 原样透出，不自动覆盖）。
func (c *Client) RecycleRestore(ctx context.Context, token, space string, fileID int64) error {
	body, err := json.Marshal(map[string]any{"fileId": fileID, "space": normalizeSpace(space)})
	if err != nil {
		return err
	}
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/recycle/restore", token, body, nil); err != nil {
		return fmt.Errorf("还原回收站文件失败：%w", err)
	}
	return nil
}

// RecyclePurge 彻底删除回收站文件：控制面摘行 + 删对象存储，不可还原。
func (c *Client) RecyclePurge(ctx context.Context, token, space string, fileID int64) error {
	body, err := json.Marshal(map[string]any{"fileId": fileID, "space": normalizeSpace(space)})
	if err != nil {
		return err
	}
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/recycle/purge", token, body, nil); err != nil {
		return fmt.Errorf("彻底删除回收站文件失败：%w", err)
	}
	return nil
}

// SetFileVisibility 设置共享空间文档的可见部门（空列表=恢复全体可见）。
// 校验在控制面（仅上传者本人可调）；过滤在列举出口统一生效。
func (c *Client) SetFileVisibility(ctx context.Context, token, space, path string, deptIDs []int64) error {
	body, err := json.Marshal(map[string]any{"path": path, "space": normalizeSpace(space), "visibleDepts": deptIDs})
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/easyshare/drive/file-visibility", token, body, nil)
}

// SetFileVisibilityByRef 同上，但部门 ID 以**十进制字符串**给出。
//
// 为什么要有这个重载：部门 ID 也是雪花 ID（实测 sys_dept 里 18 位，
// 如 1761000000000000100），同样超出 JS 安全整数 2^53-1。
// JS 侧一个 number 会把 1761000000000000101 / …102 / …103 **全部**变成
// 1761000000000000000（实测 4/4 撞成同一个值）——那样「只给研发部看」
// 会静默变成「给某个不存在的部门看」或干脆给错部门。
// 因此凡是**来自前端**的部门 ID 都必须走字符串，见 known-issues KI-22。
func (c *Client) SetFileVisibilityByRef(ctx context.Context, token, space, path string, deptIDs []string) error {
	if deptIDs == nil {
		deptIDs = []string{}
	}
	ids := make([]int64, 0, len(deptIDs))
	for _, raw := range deptIDs {
		id, err := ParseFileID(raw)
		if err != nil {
			return fmt.Errorf("可见部门 ID 无效：%w", err)
		}
		ids = append(ids, id)
	}
	return c.SetFileVisibility(ctx, token, space, path, ids)
}
