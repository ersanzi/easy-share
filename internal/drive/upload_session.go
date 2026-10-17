// Upload Session（Multipart 断点续传，对标 Cloudreve）。
//
// 分流：文件 ≥ multipartThreshold 走会话分片上传，否则单请求——小文件不值得
// 会话往返。会话状态持久化在 SessionStore（本地 JSON，指纹 = 空间+路径+大小+mtime），
// 进程重启后同指纹文件自动续传：已完成分片跳过、只补 presign 剩余分片。
// 分片 ETag 清单存客户端本地（服务端只记会话状态），Complete 幂等由服务端保证。
//
// 技术参数（分片大小/重试次数/阈值）全部服务端定或本包常量，不暴露给用户。
package drive

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// multipartThreshold：超过此大小的文件走 Upload Session 分片上传。
	multipartThreshold = 32 << 20

	// partUploadRetries：单分片上传失败重试次数（指数退避）。
	partUploadRetries = 3

	// singlePutRetries：单请求上传（< 32 MiB）失败重试次数。
	// 与分片同理：连接挂死必须能被判死并重试，否则用户只看到进度条不动。
	singlePutRetries = 3

	// uploadStall：上传时「**多久没有字节流动**」就判定连接挂死（分片与单请求共用）。
	//
	// 为什么是「停滞」而不是「整片超时」：两者在慢网络上结果相反。
	// 8 MiB 一个分片，健康时约 150ms；但 1 Mbps 的链路上要 **60 多秒**。
	// 若按总时长砍，慢链路会被反复误杀——而重试是从头再传这一片，
	// 于是**越慢越传不上去**，永远完不成。
	//
	// 挂死的连接与慢连接的差别**不在耗时，而在有没有进展**：
	// 有字节流动就一直等，彻底不动才判死。所以按「停滞」设限。
	//
	// 分片与单请求**两条路径都受它保护**：实测两条都会挂死（见 KI-23 与 KI-24）。
	//
	// 为什么不能只靠 transfer client 的 30 分钟：那是给整次传输的
	// （可能几十 GB），对单个分片是上万倍余量。连接一旦挂住，
	// `Do()` 要等 30 分钟才返回错误——而 `putPartWithRetry` 的重试
	// **只在 `Do()` 返回错误之后才可能发生**，于是三次重试一次都用不上，
	// 用户看到的是「上传进度条永远不动」。
	//
	// 实测（2026-10-16，本机控制面 + RustFS，33 MiB / 5 片）：
	// 健康时整包 ~0.9s；异常时**某一片永久停住**——6 次运行里 2 次卡死
	// （分别卡在第 4 片和第 1 片），给 3 分钟也不恢复。
	// 这就是「重试逻辑存在，但永远触发不了」的形状。
	//
	// 30 秒取值依据：健康单片 ~150ms，是 **200 倍**余量；
	// 同时它只要求「有字节在动」，所以慢链路不受影响。
	uploadStall = 30 * time.Second
)

// stallReader 在「一段时间内没有任何字节被读出」时触发回调（通常用于取消请求）。
//
// 为什么包在 body 上：分片上传的成败不在写了多少字节，而在**还在不在动**。
// 读侧每成功一次就重置计时器；传输层因为对端不接收而停止调用 Read 时，
// 计时器自然到期——这正是「挂死」的定义。
type stallReader struct {
	reader io.Reader
	timer  *time.Timer
}

func newStallReader(reader io.Reader, stall time.Duration, onStall func()) *stallReader {
	return &stallReader{reader: reader, timer: time.AfterFunc(stall, onStall)}
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.reader.Read(p)
	if n > 0 {
		s.timer.Reset(uploadStall)
	}
	return n, err
}

// stop 释放计时器（避免 goroutine 悬挂到窗口结束）。
func (s *stallReader) stop() { s.timer.Stop() }

// PartETag 已完成分片回执（Complete 的最小必填项）。
type PartETag struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
}

// sessionState 一个进行中/待完成上传会话的本地快照。
type sessionState struct {
	SessionID int64      `json:"sessionId"`
	UploadID  string     `json:"uploadId"`
	PartSize  int64      `json:"partSize"`
	Path      string     `json:"path"`
	Space     string     `json:"space"`
	Size      int64      `json:"size"`
	ModTime   int64      `json:"modTime"`
	Done      []PartETag `json:"done"`
}

// doneNumbers 已完成分片号集合。
func (s *sessionState) doneNumbers() map[int]bool {
	done := make(map[int]bool, len(s.Done))
	for _, part := range s.Done {
		done[part.PartNumber] = true
	}
	return done
}

// SessionStore 会话本地持久化（目录）。零值不可用，经 NewSessionStore 创建。
type SessionStore struct {
	dir string
}

// NewSessionStore 创建会话存储（目录不存在会建立）。
func NewSessionStore(dir string) *SessionStore {
	_ = os.MkdirAll(dir, 0o700)
	return &SessionStore{dir: dir}
}

// fingerprint 会话文件名：空间+路径+大小+mtime 的哈希。任一变化即视为不同会话。
func fingerprint(space, path string, size, modTime int64) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d", space, path, size, modTime)))
	return hex.EncodeToString(sum[:])
}

func (s *SessionStore) fileOf(space, path string, size, modTime int64) string {
	return filepath.Join(s.dir, fingerprint(space, path, size, modTime)+".json")
}

// Load 按指纹找可续传的会话；不存在或损坏返回 nil。
func (s *SessionStore) Load(space, path string, size, modTime int64) *sessionState {
	raw, err := os.ReadFile(s.fileOf(space, path, size, modTime))
	if err != nil {
		return nil
	}
	var state sessionState
	if json.Unmarshal(raw, &state) != nil || state.SessionID == 0 || state.PartSize <= 0 {
		return nil
	}
	return &state
}

// Save 落盘会话快照（原子写惯例：临时文件 + rename）。
func (s *SessionStore) Save(state *sessionState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	target := s.fileOf(state.Space, state.Path, state.Size, state.ModTime)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// Clear 会话终态（完成/放弃）后清理本地快照。
func (s *SessionStore) Clear(state *sessionState) {
	_ = os.Remove(s.fileOf(state.Space, state.Path, state.Size, state.ModTime))
}

// 控制面 Upload Session 端点的返回体。
//
// ⚠️ 这两个 ID 用 FlexInt64 而不是 int64：它们都是**雪花 ID**，
// 必然超出 JS 安全整数（2^53-1），因此 RuoYi 的 BigNumberSerializer
// 会把它们序列化成**字符串**。用 int64 接会导致整个 Unmarshal 失败——
// 而且失败方式极具误导性：报的是「解析云盘数据失败」，
// 看起来像云盘坏了，实际只是分片上传的返回体没跟上。
//
// 这个坑在 KI-18 修 `Object`/`RecycleEntry` 时**漏掉了本文件**，
// 后果是「≥32 MiB 文件走分片上传」这条路径**从来没在真实控制面上跑通过**——
// 单测里 sessionId 是自己编的小数字，永远撞不到。2026-10-16 由
// live_multipart_cas_test.go 在真机上一次撞出（见 known-issues KI-20）。
type sessionCreateData struct {
	SessionID FlexInt64 `json:"sessionId"`
	UploadID  string    `json:"uploadId"`
	PartSize  int64     `json:"partSize"`
}
type sessionPartData struct {
	URL string `json:"url"`
}
type sessionCompleteData struct {
	FileID FlexInt64 `json:"fileId"`
}

// createSession 换会话（配额校验/遗留清理都在服务端）。
//
// expectedVersion 与 contentMd5 都与会话一起**寄存在服务端**：Multipart 的真实写入
// 发生在 complete（大文件可能几分钟），只有在 complete 校验才能覆盖
// 「写入那一刻版本是否已过期 / 内容是否已存在（秒传）」。
func (c *Client) createSession(ctx context.Context, token, space, path string,
	size int64, expectedVersion *int64, contentMd5 string) (*sessionState, error) {
	payload := map[string]any{"path": path, "space": normalizeSpace(space), "size": size}
	if expectedVersion != nil {
		payload["expectedVersion"] = *expectedVersion
	}
	if contentMd5 != "" {
		payload["contentMd5"] = contentMd5
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var data sessionCreateData
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/upload-session/create", token, body, &data); err != nil {
		return nil, err
	}
	if data.SessionID.Int64() == 0 || data.PartSize <= 0 {
		return nil, fmt.Errorf("创建上传会话失败：服务端返回不完整")
	}
	return &sessionState{
		SessionID: data.SessionID.Int64(),
		UploadID:  data.UploadID,
		PartSize:  data.PartSize,
		Path:      path,
		Space:     normalizeSpace(space),
		Size:      size,
	}, nil
}

// presignSessionPart 换单分片预签名 URL。
func (c *Client) presignSessionPart(ctx context.Context, token string, sessionID int64, partNumber int) (string, error) {
	body, err := json.Marshal(map[string]any{"sessionId": sessionID, "partNumber": partNumber})
	if err != nil {
		return "", err
	}
	var data sessionPartData
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/upload-session/part", token, body, &data); err != nil {
		return "", err
	}
	if data.URL == "" {
		return "", fmt.Errorf("签发分片 %d 失败：服务端返回空 URL", partNumber)
	}
	return data.URL, nil
}

// completeSession 提交分片清单；服务端幂等（重复 Complete 返回同一 fileId）。
func (c *Client) completeSession(ctx context.Context, token string, state *sessionState) (int64, error) {
	body, err := json.Marshal(map[string]any{
		"sessionId": state.SessionID,
		"parts":     state.Done,
	})
	if err != nil {
		return 0, err
	}
	var data sessionCompleteData
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/upload-session/complete", token, body, &data); err != nil {
		return 0, err
	}
	return data.FileID.Int64(), nil
}

// abortSession 放弃会话（服务端负责 S3 Abort 与状态推进；失败不阻塞主流程）。
func (c *Client) abortSession(ctx context.Context, token string, sessionID int64) {
	body, _ := json.Marshal(map[string]any{"sessionId": sessionID})
	_ = c.call(ctx, http.MethodPost, "/easyshare/drive/upload-session/abort", token, body, nil)
}

// putPartWithRetry 单分片直传对象存储，重试 partUploadRetries 次。
// bodyFn 每次尝试重建 reader（SectionReader 读过一次就到尾，重试必须重开）。
func (c *Client) putPartWithRetry(ctx context.Context, url string, bodyFn func() io.Reader, size int64) (string, error) {
	var etag string
	var lastErr error
	for attempt := 0; attempt < partUploadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		// 停滞守卫：若一段时间内没有任何字节流动，就取消这次尝试。
		// 取消会让 Do() 返回错误 → 进下一次重试，而不是无限期挂住。
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		guard := newStallReader(bodyFn(), uploadStall, cancelAttempt)
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPut, url, guard)
		if err != nil {
			guard.stop()
			cancelAttempt()
			return "", err
		}
		req.ContentLength = size
		// 分片 URL 与单请求一致：不签 Content-Type，客户端也不得设该头
		resp, err := c.transfer.Do(req)
		guard.stop()
		if err != nil {
			cancelAttempt()
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cancelAttempt()
		if resp.StatusCode/100 != 2 {
			lastErr = fmt.Errorf("对象存储返回 %s", resp.Status)
			continue
		}
		etag = resp.Header.Get("ETag")
		if etag == "" {
			lastErr = fmt.Errorf("对象存储未返回分片 ETag")
			continue
		}
		return etag, nil
	}
	return "", lastErr
}

// uploadFileMultipart 分片上传主流程：续传 → 逐分片（跳过已完成）→ 幂等 Complete。
// contentMd5 寄存在服务端会话上，complete 时做秒传判定（命中则服务端复制、跳过聚合）。
func (c *Client) uploadFileMultipart(ctx context.Context, token, space, objectPath, localPath string,
	size int64, modTime time.Time, expectedVersion *int64, contentMd5 string, onProgress ProgressFunc) error {
	modUnix := modTime.Unix()
	state := c.Sessions.Load(space, objectPath, size, modUnix)
	if state == nil {
		var err error
		state, err = c.createSession(ctx, token, space, objectPath, size, expectedVersion, contentMd5)
		if err != nil {
			return err
		}
		state.ModTime = modUnix
	}

	totalParts := (size + state.PartSize - 1) / state.PartSize
	done := state.doneNumbers()

	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("打开文件失败：%w", err)
	}
	defer file.Close()

	var completedBytes int64
	for _, part := range state.Done {
		completedBytes += partBytes(state.PartSize, int64(part.PartNumber), size)
	}
	report := func(partSent int64, partNumber int) {
		if onProgress != nil {
			onProgress(completedBytes+partSent, size)
		}
	}
	report(0, 0)

	for partNumber := int64(1); partNumber <= totalParts; partNumber++ {
		if done[int(partNumber)] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err // 会话快照已落盘，下次同指纹自动续传
		}
		url, err := c.presignSessionPart(ctx, token, state.SessionID, int(partNumber))
		if err != nil {
			return err
		}
		offset := (partNumber - 1) * state.PartSize
		length := partBytes(state.PartSize, partNumber, size)
		etag, err := c.putPartWithRetry(ctx, url, func() io.Reader {
			return io.NewSectionReader(file, offset, length)
		}, length)
		if err != nil {
			return fmt.Errorf("上传分片 %d/%d 失败：%w", partNumber, totalParts, err)
		}
		state.Done = append(state.Done, PartETag{PartNumber: int(partNumber), ETag: etag})
		completedBytes += length
		report(length, int(partNumber))
		if err := c.Sessions.Save(state); err != nil {
			return fmt.Errorf("保存上传会话失败：%w", err)
		}
	}

	fileID, err := c.completeSession(ctx, token, state)
	if err != nil {
		return err
	}
	c.Sessions.Clear(state)
	_ = fileID // fileId 已由控制面登记进目录层；客户端列表刷新后自然带回
	return nil
}

// partBytes 第 partNumber 片的实际字节数（最后一片可能不足整片）。
func partBytes(partSize, partNumber, total int64) int64 {
	start := (partNumber - 1) * partSize
	if total-start >= partSize {
		return partSize
	}
	return total - start
}
