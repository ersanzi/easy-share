// Package knowledge 提供知识服务（FastAPI）的 HTTP 客户端、会话存储与网关运行态。
// Core 是桌面端/后续扩展访问知识库的唯一通道；令牌只在 Core 进程与 knowledge.json 中流转，不透给前端。
package knowledge

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

// Session 一次登录会话。ServerURL 为空表示未配置，Token 为空表示未登录。
type Session struct {
	ServerURL string `json:"serverUrl"`
	Token     string `json:"token"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expiresAt"`
}

// StatusView 面向前端的登录态视图，永不包含令牌。
type StatusView struct {
	Configured bool   `json:"configured"`
	LoggedIn   bool   `json:"loggedIn"`
	ServerURL  string `json:"serverUrl"`
	Username   string `json:"username"`
	Role       string `json:"role"`
}

// User 远端身份（登录响应中的用户部分）。
type User struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// QueryRequest 问答请求；top_k 沿用服务端默认值。
type QueryRequest struct {
	Question string `json:"question"`
}

// SourceRef 生成答案的文档级引用。
type SourceRef struct {
	DocID      *string  `json:"doc_id"`
	Score      *float64 `json:"score"`
	IngestedAt *string  `json:"ingested_at"`
}

// Context 检索片段，引用溯源所需的字段子集。
type Context struct {
	DocID      *string  `json:"doc_id"`
	FileID     *string  `json:"file_id"`
	VersionID  *string  `json:"version_id"`
	Filename   *string  `json:"filename"`
	Score      *float64 `json:"score"`
	IngestedAt *string  `json:"ingested_at"`
	Text       string   `json:"text"`
	BlockIDs   []string `json:"block_ids"`
}

// Answer /query 响应：答案 + 引用 + 检索片段。
type Answer struct {
	Answer   string      `json:"answer"`
	Sources  []SourceRef `json:"sources"`
	Contexts []Context   `json:"contexts"`
}

// DocumentSourceLocation 块在原件中的位置，用于阅读页定位与引用高亮。
type DocumentSourceLocation struct {
	Page      *int    `json:"page,omitempty"`
	Sheet     *string `json:"sheet,omitempty"`
	Slide     *int    `json:"slide,omitempty"`
	Paragraph *int    `json:"paragraph,omitempty"`
	Table     *int    `json:"table,omitempty"`
	Row       *int    `json:"row,omitempty"`
}

// DocumentBlock 结构化正文块；Rows 仅表格块有值。
type DocumentBlock struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Text     string                 `json:"text,omitempty"`
	Level    *int                   `json:"level,omitempty"`
	Rows     [][]string             `json:"rows,omitempty"`
	Source   DocumentSourceLocation `json:"source"`
	Metadata map[string]any         `json:"metadata,omitempty"`
}

// Document 知识服务保存的 document.json（阅读页正文）。
type Document struct {
	SchemaVersion   int             `json:"schema_version"`
	FileID          string          `json:"file_id"`
	VersionID       string          `json:"version_id"`
	ObjectKey       string          `json:"object_key"`
	Filename        string          `json:"filename"`
	MediaType       string          `json:"media_type"`
	Metadata        map[string]any  `json:"metadata"`
	Warnings        []string        `json:"warnings"`
	Blocks          []DocumentBlock `json:"blocks"`
	SourceSHA256    string          `json:"source_sha256"`
	PipelineVersion string          `json:"pipeline_version"`
}

// DocumentManifest 阅读页需要的文档级元数据子集。
type DocumentManifest struct {
	FileID    string  `json:"file_id"`
	VersionID string  `json:"version_id"`
	Filename  string  `json:"filename"`
	ObjectKey string  `json:"object_key"`
	MediaType string  `json:"media_type"`
	Owner     *string `json:"owner"`
	// 知识服务 manifest 存的是 CSV（与 es_file 同表示），不是 JSON 数组
	VisibleDepts string   `json:"visible_depts"`
	SourceBytes  int64    `json:"source_bytes"`
	Blocks       int      `json:"blocks"`
	Characters   int      `json:"characters"`
	Chunks       int      `json:"chunks"`
	Warnings     []string `json:"warnings"`
	ProcessedAt  string   `json:"processed_at"`
}

// DocumentView 阅读页一次取齐：元数据 + 结构化正文。
type DocumentView struct {
	Manifest DocumentManifest `json:"manifest"`
	Document Document         `json:"document"`
}

// DocumentSummary 知识库清单一行：最新任务状态 + 最近一次成功版本。
type DocumentSummary struct {
	JobID         string  `json:"job_id"`
	FileID        string  `json:"file_id"`
	VersionID     string  `json:"version_id"`
	Filename      string  `json:"filename"`
	Status        string  `json:"status"`
	Stage         string  `json:"stage"`
	Progress      int     `json:"progress"`
	ErrorCode     *string `json:"error_code"`
	ErrorMessage  *string `json:"error_message"`
	Owner         *string `json:"owner"`
	UpdatedAt     string  `json:"updated_at"`
	Ready         bool    `json:"ready"`
	ReadVersionID *string `json:"read_version_id"`
	Chunks        int     `json:"chunks"`
	Characters    int     `json:"characters"`
	ProcessedAt   *string `json:"processed_at"`
	SourceBytes   int64   `json:"source_bytes"`
}

// DocumentList 清单响应。
type DocumentList struct {
	Documents []DocumentSummary `json:"documents"`
}

// GenerationQuality 生成质量聚合（命名类型：Wails 绑定生成器不支持匿名嵌套结构）。
type GenerationQuality struct {
	Total               int      `json:"total"`
	AvgFaithfulness     *float64 `json:"avg_faithfulness"`
	AvgUnsupportedRatio *float64 `json:"avg_unsupported_ratio"`
}

// CitedDoc 高频引用文档（fileId + 被引次数）。
type CitedDoc struct {
	FileID string `json:"file_id"`
	Count  int    `json:"count"`
}

// KnowledgeStats 管理员汇总页的聚合统计快照。
type KnowledgeStats struct {
	Days           int               `json:"days"`
	TotalQueries   int               `json:"total_queries"`
	RecentQueries  int               `json:"recent_queries"`
	BlindSpotCount int               `json:"blind_spot_count"`
	Generation     GenerationQuality `json:"generation"`
	MostCitedDocs  []CitedDoc        `json:"most_cited_docs"`
	Documents      int               `json:"documents"`
	LLM            string            `json:"llm"`
}

// Stats 拉聚合统计（days 为观察窗口天数，1-90）。
func (client *Client) Stats(ctx context.Context, token string, days int) (KnowledgeStats, error) {
	var stats KnowledgeStats
	err := client.call(ctx, http.MethodGet, fmt.Sprintf("/stats?days=%d", days), token, nil, &stats)
	return stats, err
}

// Health 远端 /health 中面板关心的子集。
type Health struct {
	Records   int    `json:"records"`
	Documents int    `json:"documents"`
	LLM       string `json:"llm"`
	WatchDirs int    `json:"watch_dirs"`
}

// RemoteError 携带远端 HTTP 状态码，供网关区分"凭据无效"与"服务故障"。
type RemoteError struct {
	Status int
	Detail string
}

func (e *RemoteError) Error() string { return e.Detail }

// Client 知识服务 HTTP 客户端。问答可能经多跳 LLM 链路，不设客户端级超时，由调用方上下文控制。
type Client struct {
	baseURL string
	http    *http.Client
}

// NormalizeServerURL 规范化服务器地址：去空白、缺省补 http://、去尾部斜杠。
func NormalizeServerURL(serverURL string) string {
	value := strings.TrimSpace(serverURL)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	return strings.TrimRight(value, "/")
}

// ValidServerURL 校验地址是否为可用的 http/https URL。
func ValidServerURL(serverURL string) bool {
	parsed, err := url.Parse(NormalizeServerURL(serverURL))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// NewClient 按规范化后的服务器地址创建客户端。
func NewClient(serverURL string) *Client {
	return &Client{baseURL: NormalizeServerURL(serverURL), http: &http.Client{}}
}

func (client *Client) call(ctx context.Context, method, path, token string, input, output any) error {
	var body []byte
	if input != nil {
		var err error
		if body, err = json.Marshal(input); err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("connect knowledge server: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return &RemoteError{Status: response.StatusCode, Detail: remoteDetail(response.Body)}
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// remoteDetail 提取 FastAPI 错误响应的 detail 字段，失败时回退到状态文本。
func remoteDetail(body io.Reader) string {
	var payload struct {
		Detail any `json:"detail"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err == nil {
		switch detail := payload.Detail.(type) {
		case string:
			if detail != "" {
				return detail
			}
		default:
			// 422 校验错误时 detail 是数组，序列化为可读文本
			if encoded, err := json.Marshal(payload.Detail); err == nil && string(encoded) != "null" {
				return string(encoded)
			}
		}
	}
	return "knowledge server error"
}

// Login 用账号密码换取会话令牌。
func (client *Client) Login(ctx context.Context, username, password string) (Session, error) {
	var response struct {
		Token     string `json:"token"`
		Username  string `json:"username"`
		Role      string `json:"role"`
		ExpiresAt string `json:"expires_at"`
	}
	err := client.call(ctx, http.MethodPost, "/auth/login", "", struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: username, Password: password}, &response)
	if err != nil {
		return Session{}, err
	}
	return Session{
		ServerURL: client.baseURL,
		Token:     response.Token,
		Username:  response.Username,
		Role:      response.Role,
		ExpiresAt: response.ExpiresAt,
	}, nil
}

// Query 携带令牌发起检索问答。
func (client *Client) Query(ctx context.Context, token, question string) (Answer, error) {
	var answer Answer
	err := client.call(ctx, http.MethodPost, "/query", token, QueryRequest{Question: question}, &answer)
	return answer, err
}

// Search 仅检索不生成（全局快搜面板用）：mode=search 时远端跳过 LLM，响应即时。
// Documents 拉知识库文档清单（按最近更新排序，最多 limit 份）。
func (client *Client) Documents(ctx context.Context, token string, limit int) (DocumentList, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	var result DocumentList
	err := client.call(ctx, http.MethodGet, fmt.Sprintf("/documents?limit=%d", limit), token, nil, &result)
	return result, err
}

// DocumentView 读取已入库文档的阅读页数据（元数据 + 结构化正文）。
func (client *Client) DocumentView(ctx context.Context, token, fileID, versionID string) (DocumentView, error) {
	var result DocumentView
	path := fmt.Sprintf("/documents/%s/versions/%s/view", url.PathEscape(fileID), url.PathEscape(versionID))
	err := client.call(ctx, http.MethodGet, path, token, nil, &result)
	return result, err
}

// OpenDocumentSource 打开原件下载流；调用方负责关闭 Body。
func (client *Client) OpenDocumentSource(ctx context.Context, token, fileID, versionID string) (*http.Response, error) {
	path := fmt.Sprintf("/documents/%s/versions/%s/source", url.PathEscape(fileID), url.PathEscape(versionID))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("connect knowledge server: %w", err)
	}
	if response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, &RemoteError{Status: response.StatusCode, Detail: remoteDetail(response.Body)}
	}
	return response, nil
}

func (client *Client) Search(ctx context.Context, token, question string) (Answer, error) {
	var answer Answer
	err := client.call(ctx, http.MethodPost, "/query", token, map[string]string{
		"question": question, "mode": "search",
	}, &answer)
	return answer, err
}

// HealthWithTimeout 探测远端健康状态；超时由调用方给定（短探测，不等 LLM）。
func (client *Client) HealthWithTimeout(parent context.Context, timeout time.Duration) (Health, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var health Health
	err := client.call(ctx, http.MethodGet, "/health", "", nil, &health)
	return health, err
}

// --- 会议听记（/meetings） ---

// MeetingSegment 一条发言轮次；Speaker 为引擎输出的序号字符串（"0"/"1"/...），
// 展示名以 SpeakerNames 映射为准（用户改名后与序号解耦）。
type MeetingSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker string  `json:"speaker"`
}

// MeetingTodo 一条待办（一等实体，对标钉钉 MinutesTodoI：可增删改与勾选完成）。
// ID 由知识服务生成；Source 为 "ai"（随总结刷新）或 "manual"（用户手工，重生成保留）。
type MeetingTodo struct {
	ID        string `json:"id"`
	Task      string `json:"task"`
	OwnerHint string `json:"owner_hint"`
	DueHint   string `json:"due_hint"`
	Done      bool   `json:"done"`
	Source    string `json:"source"`
}

// MeetingSection 模板化总结的小节（title + 段落或列表）。
type MeetingSection struct {
	Title   string   `json:"title"`
	Text    string   `json:"text"`
	Bullets []string `json:"bullets"`
}

// MeetingChapter 智能切片章节（Start/End 为秒；对标钉钉结构化章节，点击可跳转播放）。
type MeetingChapter struct {
	Title string `json:"title"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Gist  string `json:"gist,omitempty"`
}

// MeetingSummary AI 总结（未生成/LLM 未配置时为 null）。
// 默认模板填 Minutes/KeyPoints；其他模板填 Sections；待办是独立实体，不在这里。
type MeetingSummary struct {
	Minutes   string           `json:"minutes,omitempty"`
	KeyPoints []string         `json:"key_points,omitempty"`
	Sections  []MeetingSection `json:"sections,omitempty"`
	Chapters  []MeetingChapter `json:"chapters,omitempty"`
}

// Meeting 会议听记记录。列表视图时 Segments 为空、SegmentCount 给出条数；
// 详情视图含全部分段。Owner/Error/DurationSeconds/Summary 可为 null。
// Blocks 是各 AI 区块的独立状态（pending/processing/ready/failed/skipped），
// 对标钉钉分任务状态：某块失败只补跑那块。
type Meeting struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	CreatedAt       string            `json:"created_at"`
	Owner           *string           `json:"owner"`
	Status          string            `json:"status"`
	Error           *string           `json:"error"`
	DurationSeconds *float64          `json:"duration_seconds"`
	SpeakerCount    int               `json:"speaker_count"`
	SpeakerNames    map[string]string `json:"speaker_names"`
	Segments        []MeetingSegment  `json:"segments"`
	EditedSegments  []int             `json:"edited_segments"`
	Summary         *MeetingSummary   `json:"summary"`
	Keywords        []string          `json:"keywords"`
	Todos           []MeetingTodo     `json:"todos"`
	Blocks          map[string]string `json:"blocks"`
	BlockErrors     map[string]string `json:"block_errors"`
	Mindmap         *string           `json:"mindmap"`
	TemplateID      *string           `json:"template_id"`
	SegmentCount    int               `json:"segment_count"`
}

// MeetingLiveLine 实时字幕单行（At 为该行起始秒数）。
type MeetingLiveLine struct {
	Text string  `json:"text"`
	At   float64 `json:"at"`
}

// MeetingLiveView 推流会话的当前上屏状态（预览，终态以离线重跑为准）。
// LastLineAt 是最后一行上屏时的录音秒数（0=还没出过字），前端据此显示「N 秒前」；
// ActiveSeconds 表示这一片音频里确实有声音（静音片段还在等，不算卡住）。
type MeetingLiveView struct {
	Lines          []MeetingLiveLine `json:"lines"`
	Current        string            `json:"current"`
	ElapsedSeconds float64           `json:"elapsed_seconds"`
	LastLineAt     float64           `json:"last_line_at"`
	ActiveSeconds  bool              `json:"active_seconds"`
}

// MeetingLiveCheck 会中快检结果（只核对刚上屏的最近几行）。
// Verdict：attention=发现可核对的问题，ok=没问题，unclear=内容不足，
// unavailable=LLM 未配置/调用失败。
type MeetingLiveCheck struct {
	Verdict    string   `json:"verdict"`
	Note       string   `json:"note"`
	Points     []string `json:"points"`
	LastLineAt float64  `json:"last_line_at"`
	CheckedAt  string   `json:"checked_at"`
}

// MeetingLiveSession 录制会话状态（前端轮询兜底：推流断了字也不会停在半路）。
type MeetingLiveSession struct {
	Recording bool              `json:"recording"`
	Lines     []MeetingLiveLine `json:"lines"`
	Current   string            `json:"current"`
	Elapsed   float64           `json:"elapsed_seconds"`
}

// postForm 发 x-www-form-urlencoded（FastAPI 的 Form 字段口径），返回解码后的 JSON。
func (client *Client) postForm(ctx context.Context, path, token string, form url.Values, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build form request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("connect knowledge server: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return &RemoteError{Status: response.StatusCode, Detail: remoteDetail(response.Body)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decode form response: %w", err)
	}
	return nil
}

// postRaw 发原始字节体（实时推流的 PCM 分片），返回解码后的 JSON。
func (client *Client) postRaw(ctx context.Context, path, token string, body []byte, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build raw request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("connect knowledge server: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return &RemoteError{Status: response.StatusCode, Detail: remoteDetail(response.Body)}
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decode raw response: %w", err)
	}
	return nil
}

// WarmMeetingStream 让知识服务后台预热流式模型（首次加载约 70s，否则首片卡住上屏）。
func (client *Client) WarmMeetingStream(ctx context.Context, token string) error {
	var ignored map[string]any
	return client.postForm(ctx, "/meetings/stream/warm", token, url.Values{}, &ignored)
}

// OpenMeetingStream 开一场实时录制会话（先落 recording 记录，返回其 id 当推流会话 id）。
func (client *Client) OpenMeetingStream(ctx context.Context, token, title, templateID string) (Meeting, error) {
	var meeting Meeting
	form := url.Values{"title": {title}}
	if templateID != "" {
		form.Set("template_id", templateID)
	}
	err := client.postForm(ctx, "/meetings/stream/session", token, form, &meeting)
	return meeting, err
}

// MeetingStreamChunk 推一片 PCM16/16kHz/单声道，回当前上屏状态。
func (client *Client) MeetingStreamChunk(ctx context.Context, token, id string, pcm []byte) (MeetingLiveView, error) {
	var view MeetingLiveView
	err := client.postRaw(ctx, "/meetings/"+url.PathEscape(id)+"/stream", token, pcm, &view)
	return view, err
}

// MeetingStreamStop 结束推流（冲尾包），回完整实时字幕行。
func (client *Client) MeetingStreamStop(ctx context.Context, token, id string) (MeetingLiveView, error) {
	var view MeetingLiveView
	err := client.postForm(ctx, "/meetings/"+url.PathEscape(id)+"/stream/stop", token, url.Values{}, &view)
	return view, err
}

// MeetingLivePoints 会中要点（LLM 基于已上屏内容归纳，随录随长）。
func (client *Client) MeetingLivePoints(ctx context.Context, token, id string) ([]string, error) {
	var payload struct {
		Points []string `json:"points"`
	}
	err := client.postForm(ctx, "/meetings/"+url.PathEscape(id)+"/live-points", token, url.Values{}, &payload)
	return payload.Points, err
}

// MeetingLiveCheck 会中快检：只核对刚上屏的最近几行（秒级 LLM 调用）。
func (client *Client) MeetingLiveCheck(ctx context.Context, token, id string) (MeetingLiveCheck, error) {
	var result MeetingLiveCheck
	err := client.postForm(ctx, "/meetings/"+url.PathEscape(id)+"/live-check", token, url.Values{}, &result)
	return result, err
}

// MeetingLiveSession 取录制会话当前状态（不推音频也能刷新上屏内容）。
func (client *Client) MeetingLiveSession(ctx context.Context, token, id string) (MeetingLiveSession, error) {
	var view MeetingLiveSession
	err := client.call(ctx, http.MethodGet, "/meetings/"+url.PathEscape(id)+"/stream/live", token, nil, &view)
	return view, err
}

// MergeMeetingSpeakers 合并两位发言人（同一人被拆成两簇时一键并回）。
func (client *Client) MergeMeetingSpeakers(ctx context.Context, token, id, fromSpeaker, intoSpeaker string) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodPost, "/meetings/"+url.PathEscape(id)+"/speakers/merge", token,
		map[string]string{"from_speaker": fromSpeaker, "into_speaker": intoSpeaker}, &meeting)
	return meeting, err
}

// UpdateMeetingSegment 改一句转写正文（句索引以当前 segments 顺序为准）。
func (client *Client) UpdateMeetingSegment(ctx context.Context, token, id string, index int, text string) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodPatch, "/meetings/"+url.PathEscape(id)+"/segments/"+strconv.Itoa(index), token,
		map[string]string{"text": text}, &meeting)
	return meeting, err
}

// HotwordsResult 热词读写返回（max 为服务端每人上限，供前端显示 N/max）。
type HotwordsResult struct {
	Words []string `json:"words"`
	Max   int      `json:"max"`
}

// MeetingHotwords 取当前用户热词表。
func (client *Client) MeetingHotwords(ctx context.Context, token string) (HotwordsResult, error) {
	var result HotwordsResult
	err := client.call(ctx, http.MethodGet, "/meetings/hotwords", token, nil, &result)
	if result.Words == nil {
		result.Words = []string{}
	}
	return result, err
}

// AddMeetingHotwords 批量新增热词（服务端校验字符合法性与上限），返回最新词表。
func (client *Client) AddMeetingHotwords(ctx context.Context, token string, words []string) (HotwordsResult, error) {
	var result HotwordsResult
	if words == nil {
		words = []string{}
	}
	err := client.call(ctx, http.MethodPost, "/meetings/hotwords", token, map[string]any{"words": words}, &result)
	if result.Words == nil {
		result.Words = []string{}
	}
	return result, err
}

// DeleteMeetingHotword 删除一条热词，返回最新词表。
func (client *Client) DeleteMeetingHotword(ctx context.Context, token, word string) (HotwordsResult, error) {
	var result HotwordsResult
	err := client.call(ctx, http.MethodDelete, "/meetings/hotwords/"+url.PathEscape(word), token, nil, &result)
	if result.Words == nil {
		result.Words = []string{}
	}
	return result, err
}

// RetranscribeMeeting 重新转写整场（加了热词后重跑全管线），异步受理返回 processing 记录。
func (client *Client) RetranscribeMeeting(ctx context.Context, token, id string) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodPost, "/meetings/"+url.PathEscape(id)+"/retranscribe", token, map[string]any{}, &meeting)
	return meeting, err
}

// MeetingVoiceprint 已登记声纹的元数据（向量本体不出服务端）。
type MeetingVoiceprint struct {
	Name          string  `json:"name"`
	SampleSeconds float64 `json:"sample_seconds"`
	UpdatedAt     string  `json:"updated_at"`
}

// MeetingVoiceprints 列出当前用户已登记的声纹。
func (client *Client) MeetingVoiceprints(ctx context.Context, token string) ([]MeetingVoiceprint, error) {
	var payload struct {
		Voiceprints []MeetingVoiceprint `json:"voiceprints"`
	}
	err := client.call(ctx, http.MethodGet, "/meetings/voiceprints", token, nil, &payload)
	if payload.Voiceprints == nil {
		payload.Voiceprints = []MeetingVoiceprint{}
	}
	return payload.Voiceprints, err
}

// DeleteMeetingVoiceprint 删除一条声纹（生物特征数据必须可删）。
func (client *Client) DeleteMeetingVoiceprint(ctx context.Context, token, name string) ([]MeetingVoiceprint, error) {
	var payload struct {
		Voiceprints []MeetingVoiceprint `json:"voiceprints"`
	}
	err := client.call(ctx, http.MethodDelete, "/meetings/voiceprints/"+url.PathEscape(name), token, nil, &payload)
	if payload.Voiceprints == nil {
		payload.Voiceprints = []MeetingVoiceprint{}
	}
	return payload.Voiceprints, err
}

// ArchiveResult 会议归档入库的结果。
type ArchiveResult struct {
	DocID    string `json:"doc_id"`
	Filename string `json:"filename"`
	Chunks   int    `json:"chunks"`
	Chars    int    `json:"chars"`
}

// MeetingChatTurn 听记问答历史单轮（role 限 user/assistant）。
type MeetingChatTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MeetingAskResult 听记问答返回。
type MeetingAskResult struct {
	Answer string `json:"answer"`
}

// Meetings 拉会议列表（不含分段全文）。
func (client *Client) Meetings(ctx context.Context, token string, limit int) ([]Meeting, error) {
	var payload struct {
		Meetings []Meeting `json:"meetings"`
	}
	err := client.call(ctx, http.MethodGet, fmt.Sprintf("/meetings?limit=%d", limit), token, nil, &payload)
	return payload.Meetings, err
}

// Meeting 拉单条完整记录（含分段与总结），录音完成后前端轮询用。
func (client *Client) Meeting(ctx context.Context, token, id string) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodGet, "/meetings/"+url.PathEscape(id), token, nil, &meeting)
	return meeting, err
}

// DeleteMeeting 删除会议记录与音频文件。
func (client *Client) DeleteMeeting(ctx context.Context, token, id string) error {
	return client.call(ctx, http.MethodDelete, "/meetings/"+url.PathEscape(id), token, nil, nil)
}

// RenameMeetingSpeaker 发言人改名。
func (client *Client) RenameMeetingSpeaker(ctx context.Context, token, id, speaker, name string) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodPatch, "/meetings/"+url.PathEscape(id)+"/speakers", token,
		map[string]string{"speaker": speaker, "name": name}, &meeting)
	return meeting, err
}

// RegenerateMeetingSummary 按记录登记的模板重新生成总结（LLM 调用，较慢）。
func (client *Client) RegenerateMeetingSummary(ctx context.Context, token, id, templateID string) (Meeting, error) {
	var meeting Meeting
	body := map[string]string{"template_id": templateID}
	err := client.call(ctx, http.MethodPost, "/meetings/"+url.PathEscape(id)+"/summary", token, body, &meeting)
	return meeting, err
}

// MeetingAsk 对本条听记提问（纪要+转写全文进上下文，LLM 调用，较慢）。
func (client *Client) MeetingAsk(ctx context.Context, token, id, question string, history []MeetingChatTurn) (MeetingAskResult, error) {
	var result MeetingAskResult
	// nil 切片会序列化成 null，对端 pydantic 拒收显式 null，这里归一为空列表
	if history == nil {
		history = []MeetingChatTurn{}
	}
	body := map[string]any{"question": question, "history": history}
	err := client.call(ctx, http.MethodPost, "/meetings/"+url.PathEscape(id)+"/ask", token, body, &result)
	return result, err
}

// RegenerateMeetingKeywords 单独补跑关键词区块（LLM 调用，较慢），返回完整记录。
func (client *Client) RegenerateMeetingKeywords(ctx context.Context, token, id string) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodPost, "/meetings/"+url.PathEscape(id)+"/keywords", token, map[string]any{}, &meeting)
	return meeting, err
}

// MeetingTodoInput 待办新增/编辑入参（指针字段：缺省即不改动）。
type MeetingTodoInput struct {
	Task      *string `json:"task,omitempty"`
	OwnerHint *string `json:"owner_hint,omitempty"`
	DueHint   *string `json:"due_hint,omitempty"`
	Done      *bool   `json:"done,omitempty"`
}

// AddMeetingTodo 手工新增待办，返回更新后的完整记录。
func (client *Client) AddMeetingTodo(ctx context.Context, token, id string, input MeetingTodoInput) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodPost, "/meetings/"+url.PathEscape(id)+"/todos", token, input, &meeting)
	return meeting, err
}

// UpdateMeetingTodo 编辑待办（内容/负责人/期限/完成态），返回更新后的完整记录。
func (client *Client) UpdateMeetingTodo(ctx context.Context, token, id, todoID string, input MeetingTodoInput) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodPatch, "/meetings/"+url.PathEscape(id)+"/todos/"+url.PathEscape(todoID), token, input, &meeting)
	return meeting, err
}

// DeleteMeetingTodo 删除一条待办，返回更新后的完整记录。
func (client *Client) DeleteMeetingTodo(ctx context.Context, token, id, todoID string) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodDelete, "/meetings/"+url.PathEscape(id)+"/todos/"+url.PathEscape(todoID), token, nil, &meeting)
	return meeting, err
}

// RegenerateMeetingMindmap 重新生成思维导图大纲（LLM 调用，较慢）。
func (client *Client) RegenerateMeetingMindmap(ctx context.Context, token, id string) (Meeting, error) {
	var meeting Meeting
	err := client.call(ctx, http.MethodPost, "/meetings/"+url.PathEscape(id)+"/mindmap", token, nil, &meeting)
	return meeting, err
}

// ArchiveMeeting 把纪要与转写归档进知识库。
func (client *Client) ArchiveMeeting(ctx context.Context, token, id string) (ArchiveResult, error) {
	var result ArchiveResult
	err := client.call(ctx, http.MethodPost, "/meetings/"+url.PathEscape(id)+"/archive", token, nil, &result)
	return result, err
}

// UploadMeeting 转发 multipart 上传体到 /meetings/transcribe（流式，不整体落内存）。
func (client *Client) UploadMeeting(ctx context.Context, token, contentType string, body io.Reader) (Meeting, error) {
	var meeting Meeting
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/meetings/transcribe", body)
	if err != nil {
		return meeting, fmt.Errorf("build meeting upload: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", contentType)
	response, err := client.http.Do(request)
	if err != nil {
		return meeting, fmt.Errorf("connect knowledge server: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return meeting, &RemoteError{Status: response.StatusCode, Detail: remoteDetail(response.Body)}
	}
	if err := json.NewDecoder(response.Body).Decode(&meeting); err != nil {
		return meeting, fmt.Errorf("decode meeting response: %w", err)
	}
	return meeting, nil
}

// StreamMeetingAudio 把会议音频响应流式写入 writer（网关透传，不落内存）。
func (client *Client) StreamMeetingAudio(ctx context.Context, token, id string, writer http.ResponseWriter) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/meetings/"+url.PathEscape(id)+"/audio", nil)
	if err != nil {
		return fmt.Errorf("build meeting audio request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("connect knowledge server: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return &RemoteError{Status: response.StatusCode, Detail: remoteDetail(response.Body)}
	}
	writer.Header().Set("Content-Type", "audio/wav")
	if response.ContentLength > 0 {
		writer.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	}
	writer.WriteHeader(http.StatusOK)
	_, err = io.Copy(writer, response.Body)
	return err
}

// StreamMeetingExport 把会议导出文件（format: markdown|docx）流式透传到 writer，
// 透传 Content-Type 与 Content-Disposition（内含下载文件名）。
func (client *Client) StreamMeetingExport(ctx context.Context, token, id, format string, writer http.ResponseWriter) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		client.baseURL+"/meetings/"+url.PathEscape(id)+"/export/"+url.PathEscape(format), nil)
	if err != nil {
		return fmt.Errorf("build meeting export request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("connect knowledge server: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return &RemoteError{Status: response.StatusCode, Detail: remoteDetail(response.Body)}
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		writer.Header().Set("Content-Type", contentType)
	}
	if disposition := response.Header.Get("Content-Disposition"); disposition != "" {
		writer.Header().Set("Content-Disposition", disposition)
	}
	if response.ContentLength > 0 {
		writer.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	}
	writer.WriteHeader(http.StatusOK)
	_, err = io.Copy(writer, response.Body)
	return err
}
