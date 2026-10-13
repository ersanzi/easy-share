package desktop

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

	"github.com/coder/websocket"

	"easyshare/internal/api"
	"easyshare/internal/discovery"
	"easyshare/internal/knowledge"
	"easyshare/internal/task"
)

type Snapshot struct {
	Status api.Status       `json:"status"`
	Peers  []discovery.Peer `json:"peers"`
	Tasks  []task.Task      `json:"tasks"`
}
type Client struct {
	baseURL, token string
	http           *http.Client
	// slowHTTP 无客户端级超时，供长链路调用（知识问答多跳 LLM）使用，由 Core 侧上下文兜底。
	slowHTTP *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 10 * time.Second}, slowHTTP: &http.Client{}}
}
func (client *Client) request(ctx context.Context, method, path string, input, output any) error {
	return client.do(ctx, client.http, method, path, input, output)
}

// slowRequest 与 request 同协议，但不受 10s 客户端超时约束。
func (client *Client) slowRequest(ctx context.Context, method, path string, input, output any) error {
	return client.do(ctx, client.slowHTTP, method, path, input, output)
}

// slowRequestRaw 推原始字节体（实时 PCM 分片），不能走 do()——那会把 []byte 再 JSON 编码成 base64 字符串。
func (client *Client) slowRequestRaw(ctx context.Context, method, path string, payload []byte, output any) error {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.slowHTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		var apiError api.ErrorResponse
		_ = json.NewDecoder(response.Body).Decode(&apiError)
		if apiError.Message != "" {
			return fmt.Errorf("%s", apiError.Message)
		}
		return fmt.Errorf("core 返回状态 %d", response.StatusCode)
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(output)
}

func (client *Client) do(ctx context.Context, httpClient *http.Client, method, path string, input, output any) error {
	var body *bytes.Reader
	if input == nil {
		body = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		var apiError api.ErrorResponse
		_ = json.NewDecoder(response.Body).Decode(&apiError)
		return fmt.Errorf("%s", apiError.Message)
	}
	if output != nil {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}
func (client *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var result Snapshot
	if err := client.request(ctx, http.MethodGet, "/api/status", nil, &result.Status); err != nil {
		return result, err
	}
	if err := client.request(ctx, http.MethodGet, "/api/peers", nil, &result.Peers); err != nil {
		return result, err
	}
	if err := client.request(ctx, http.MethodGet, "/api/tasks", nil, &result.Tasks); err != nil {
		return result, err
	}
	return result, nil
}
func (client *Client) Send(ctx context.Context, peerID, path, batchID string) error {
	return client.request(ctx, http.MethodPost, "/api/transfers", map[string]string{"peerId": peerID, "filePath": path, "batchId": batchID}, nil)
}
func (client *Client) Accept(ctx context.Context, id string) error {
	return client.request(ctx, http.MethodPost, "/api/transfers/"+id+"/accept", nil, nil)
}
func (client *Client) AcceptTo(ctx context.Context, id, saveDir string) error {
	return client.request(ctx, http.MethodPost, "/api/transfers/"+id+"/accept", map[string]string{"saveDir": saveDir}, nil)
}
func (client *Client) Reject(ctx context.Context, id string) error {
	return client.request(ctx, http.MethodPost, "/api/transfers/"+id+"/reject", nil, nil)
}
func (client *Client) Action(ctx context.Context, path string) error {
	return client.request(ctx, http.MethodPost, path, nil, nil)
}
func (client *Client) ClearTasks(ctx context.Context) error {
	return client.request(ctx, http.MethodPost, "/api/tasks/clear", nil, nil)
}
func (client *Client) DeleteTask(ctx context.Context, id string) error {
	return client.request(ctx, http.MethodDelete, "/api/tasks/"+id, nil, nil)
}

// CreateTask 在 Core 统一任务存储中注册外部任务（云盘上传/下载），返回创建后的完整任务。
func (client *Client) CreateTask(ctx context.Context, input map[string]any) (task.Task, error) {
	var result task.Task
	err := client.request(ctx, http.MethodPost, "/api/tasks", input, &result)
	return result, err
}

// PatchTask 更新外部任务的进度/状态。
func (client *Client) PatchTask(ctx context.Context, id string, input map[string]any) error {
	return client.request(ctx, http.MethodPatch, "/api/tasks/"+id, input, nil)
}

// --- 知识网关代理（令牌全程留在 Core，前端只见登录态视图） ---

// KnowledgeStatus 获取知识登录态。
func (client *Client) KnowledgeStatus(ctx context.Context) (knowledge.StatusView, error) {
	var result knowledge.StatusView
	err := client.request(ctx, http.MethodGet, "/api/knowledge/status", nil, &result)
	return result, err
}

// KnowledgeLogin 登录知识服务；成功后 Core 落盘会话。
func (client *Client) KnowledgeLogin(ctx context.Context, serverURL, username, password string) (knowledge.StatusView, error) {
	var result knowledge.StatusView
	input := struct {
		ServerURL string `json:"serverUrl"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	}{ServerURL: serverURL, Username: username, Password: password}
	err := client.request(ctx, http.MethodPost, "/api/knowledge/login", input, &result)
	return result, err
}

// KnowledgeLogout 清空本地会话。
func (client *Client) KnowledgeLogout(ctx context.Context) error {
	return client.request(ctx, http.MethodPost, "/api/knowledge/logout", nil, nil)
}

// KnowledgeHealth 探测知识服务健康度（文档规模/LLM 状态）。
func (client *Client) KnowledgeHealth(ctx context.Context) (knowledge.Health, error) {
	var result knowledge.Health
	err := client.request(ctx, http.MethodPost, "/api/knowledge/health", nil, &result)
	return result, err
}

// KnowledgeAsk 知识问答；长链路调用，走无客户端超时的 slowRequest。
func (client *Client) KnowledgeAsk(ctx context.Context, question string) (knowledge.Answer, error) {
	var result knowledge.Answer
	err := client.slowRequest(ctx, http.MethodPost, "/api/knowledge/query", map[string]string{"question": question}, &result)
	return result, err
}

// KnowledgeStats 知识聚合统计（管理员汇总页；days 为观察窗口天数）。
func (client *Client) KnowledgeStats(ctx context.Context, days int) (knowledge.KnowledgeStats, error) {
	var result knowledge.KnowledgeStats
	err := client.request(ctx, http.MethodGet, fmt.Sprintf("/api/knowledge/stats?days=%d", days), nil, &result)
	return result, err
}

// KnowledgeSearch 知识快搜：仅检索不生成（全局搜索面板用）。
func (client *Client) KnowledgeSearch(ctx context.Context, question string) (knowledge.Answer, error) {
	var result knowledge.Answer
	err := client.request(ctx, http.MethodPost, "/api/knowledge/query",
		map[string]string{"question": question, "mode": "search"}, &result)
	return result, err
}

// --- 会议听记（列表/详情/管理走 JSON；上传与音频走流式专用方法） ---

// Meetings 会议听记列表（不含分段全文）。
func (client *Client) Meetings(ctx context.Context, limit int) ([]knowledge.Meeting, error) {
	var payload struct {
		Meetings []knowledge.Meeting `json:"meetings"`
	}
	err := client.request(ctx, http.MethodGet, fmt.Sprintf("/api/knowledge/meetings?limit=%d", limit), nil, &payload)
	return payload.Meetings, err
}

// Meeting 单条完整记录（分段 + 总结），受理后轮询用。
func (client *Client) Meeting(ctx context.Context, id string) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	err := client.request(ctx, http.MethodGet, "/api/knowledge/meetings/"+url.PathEscape(id), nil, &result)
	return result, err
}

// MeetingDelete 删除会议记录与音频。
func (client *Client) MeetingDelete(ctx context.Context, id string) error {
	return client.request(ctx, http.MethodDelete, "/api/knowledge/meetings/"+url.PathEscape(id), nil, nil)
}

// MeetingRenameSpeaker 发言人改名。
func (client *Client) MeetingRenameSpeaker(ctx context.Context, id, speaker, name string) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	input := map[string]string{"speaker": speaker, "name": name}
	err := client.request(ctx, http.MethodPatch, "/api/knowledge/meetings/"+url.PathEscape(id)+"/speakers", input, &result)
	return result, err
}

// MeetingSummaryRegenerate 按记录登记的模板重新生成总结（LLM 调用，慢链路）。
func (client *Client) MeetingSummaryRegenerate(ctx context.Context, id, templateID string) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	err := client.slowRequest(ctx, http.MethodPost, "/api/knowledge/meetings/"+url.PathEscape(id)+"/summary",
		map[string]string{"template_id": templateID}, &result)
	return result, err
}

// MeetingAsk 听记 AI 问答（纪要+转写全文进上下文，LLM 调用，慢链路）。
func (client *Client) MeetingAsk(ctx context.Context, id, question string, history []knowledge.MeetingChatTurn) (knowledge.MeetingAskResult, error) {
	var result knowledge.MeetingAskResult
	err := client.slowRequest(ctx, http.MethodPost, "/api/knowledge/meetings/"+url.PathEscape(id)+"/ask",
		map[string]any{"question": question, "history": history}, &result)
	return result, err
}

// McpConnection 听记 MCP 接入信息（设置页「AI 工具连接」卡展示用）。
type McpConnection struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// McpInfo 取听记 MCP 接入信息。
func (client *Client) McpInfo(ctx context.Context) (McpConnection, error) {
	var result McpConnection
	err := client.request(ctx, http.MethodGet, "/api/mcp/info", nil, &result)
	return result, err
}

// McpRegenerate 重新生成听记 MCP 配对令牌（吊销旧令牌）。
func (client *Client) McpRegenerate(ctx context.Context) (McpConnection, error) {
	var result McpConnection
	err := client.request(ctx, http.MethodPost, "/api/mcp/regenerate", nil, &result)
	return result, err
}

// MeetingMindmapRegenerate 重新生成思维导图（LLM 调用，慢链路）。
func (client *Client) MeetingMindmapRegenerate(ctx context.Context, id string) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	err := client.slowRequest(ctx, http.MethodPost, "/api/knowledge/meetings/"+url.PathEscape(id)+"/mindmap", nil, &result)
	return result, err
}

// MeetingKeywordsRegenerate 单独补跑关键词区块（LLM 调用，慢链路）。
func (client *Client) MeetingKeywordsRegenerate(ctx context.Context, id string) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	err := client.slowRequest(ctx, http.MethodPost, "/api/knowledge/meetings/"+url.PathEscape(id)+"/keywords", map[string]any{}, &result)
	return result, err
}

// MeetingTodoAdd 手工新增待办（快链路）。
func (client *Client) MeetingTodoAdd(ctx context.Context, id string, input knowledge.MeetingTodoInput) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	err := client.request(ctx, http.MethodPost, "/api/knowledge/meetings/"+url.PathEscape(id)+"/todos", input, &result)
	return result, err
}

// MeetingTodoUpdate 编辑待办或勾选完成（快链路）。
func (client *Client) MeetingTodoUpdate(ctx context.Context, id, todoID string, input knowledge.MeetingTodoInput) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	err := client.request(ctx, http.MethodPatch,
		"/api/knowledge/meetings/"+url.PathEscape(id)+"/todos/"+url.PathEscape(todoID), input, &result)
	return result, err
}

// MeetingTodoRemove 删除一条待办（快链路）。
func (client *Client) MeetingTodoRemove(ctx context.Context, id, todoID string) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	err := client.request(ctx, http.MethodDelete,
		"/api/knowledge/meetings/"+url.PathEscape(id)+"/todos/"+url.PathEscape(todoID), nil, &result)
	return result, err
}

// --- 实时上屏（边说边出字） ---

// MeetingStreamWarm 让知识服务后台预热流式模型（首片不再卡冷加载）。
func (client *Client) MeetingStreamWarm(ctx context.Context) error {
	return client.request(ctx, http.MethodPost, "/api/knowledge/meetings/stream/warm", map[string]any{}, nil)
}

// MeetingStreamOpen 开实时录制会话，返回带 id 的 recording 记录。
func (client *Client) MeetingStreamOpen(ctx context.Context, title, templateID string) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	input := map[string]string{"title": title, "templateId": templateID}
	err := client.request(ctx, http.MethodPost, "/api/knowledge/meetings/stream/session", input, &result)
	return result, err
}

// MeetingStreamChunk 推一片 PCM（首片含流式模型冷加载，走 slowRequest）。
func (client *Client) MeetingStreamChunk(ctx context.Context, id string, pcm []byte) (knowledge.MeetingLiveView, error) {
	var result knowledge.MeetingLiveView
	err := client.slowRequestRaw(ctx, http.MethodPost,
		"/api/knowledge/meetings/"+url.PathEscape(id)+"/stream", pcm, &result)
	return result, err
}

// MeetingStreamStop 结束推流会话，回完整实时字幕行。
func (client *Client) MeetingStreamStop(ctx context.Context, id string) (knowledge.MeetingLiveView, error) {
	var result knowledge.MeetingLiveView
	err := client.slowRequest(ctx, http.MethodPost,
		"/api/knowledge/meetings/"+url.PathEscape(id)+"/stream/stop", map[string]any{}, &result)
	return result, err
}

// MeetingLivePoints 会中要点（LLM 慢链路）。
func (client *Client) MeetingLivePoints(ctx context.Context, id string) ([]string, error) {
	var payload struct {
		Points []string `json:"points"`
	}
	err := client.slowRequest(ctx, http.MethodPost,
		"/api/knowledge/meetings/"+url.PathEscape(id)+"/live-points", map[string]any{}, &payload)
	return payload.Points, err
}

// --- 个性化热词与重新转写 ---

// MeetingHotwords 读当前用户热词表。
func (client *Client) MeetingHotwords(ctx context.Context) (knowledge.HotwordsResult, error) {
	var result knowledge.HotwordsResult
	err := client.request(ctx, http.MethodGet, "/api/knowledge/meetings/hotwords", nil, &result)
	return result, err
}

// MeetingHotwordsAdd 批量新增热词，返回最新词表。
func (client *Client) MeetingHotwordsAdd(ctx context.Context, words []string) (knowledge.HotwordsResult, error) {
	var result knowledge.HotwordsResult
	err := client.request(ctx, http.MethodPost, "/api/knowledge/meetings/hotwords", map[string]any{"words": words}, &result)
	return result, err
}

// MeetingHotwordRemove 删除一条热词，返回最新词表。
func (client *Client) MeetingHotwordRemove(ctx context.Context, word string) (knowledge.HotwordsResult, error) {
	var result knowledge.HotwordsResult
	err := client.request(ctx, http.MethodDelete,
		"/api/knowledge/meetings/hotwords/"+url.PathEscape(word), nil, &result)
	return result, err
}

// MeetingRetranscribe 重新转写整场（异步受理）。
func (client *Client) MeetingRetranscribe(ctx context.Context, id string) (knowledge.Meeting, error) {
	var result knowledge.Meeting
	err := client.request(ctx, http.MethodPost,
		"/api/knowledge/meetings/"+url.PathEscape(id)+"/retranscribe", map[string]any{}, &result)
	return result, err
}

// MeetingVoiceprints 列出当前用户已登记声纹（仅元数据）。
func (client *Client) MeetingVoiceprints(ctx context.Context) ([]knowledge.MeetingVoiceprint, error) {
	var payload struct {
		Voiceprints []knowledge.MeetingVoiceprint `json:"voiceprints"`
	}
	err := client.request(ctx, http.MethodGet, "/api/knowledge/meetings/voiceprints", nil, &payload)
	return payload.Voiceprints, err
}

// MeetingVoiceprintRemove 删除一条声纹。
func (client *Client) MeetingVoiceprintRemove(ctx context.Context, name string) ([]knowledge.MeetingVoiceprint, error) {
	var payload struct {
		Voiceprints []knowledge.MeetingVoiceprint `json:"voiceprints"`
	}
	err := client.request(ctx, http.MethodDelete,
		"/api/knowledge/meetings/voiceprints/"+url.PathEscape(name), nil, &payload)
	return payload.Voiceprints, err
}

// MeetingArchive 归档进知识库。
func (client *Client) MeetingArchive(ctx context.Context, id string) (knowledge.ArchiveResult, error) {
	var result knowledge.ArchiveResult
	err := client.slowRequest(ctx, http.MethodPost, "/api/knowledge/meetings/"+url.PathEscape(id)+"/archive", nil, &result)
	return result, err
}

// MeetingUpload 流式转发会议音频 multipart 上传体（无客户端超时，Core 侧上下文兜底）。
func (client *Client) MeetingUpload(ctx context.Context, contentType string, body io.Reader, out *knowledge.Meeting) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/knowledge/meetings/transcribe", body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", contentType)
	response, err := client.slowHTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		var apiError api.ErrorResponse
		_ = json.NewDecoder(response.Body).Decode(&apiError)
		return fmt.Errorf("%s", apiError.Message)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// MeetingAudioStream 把会议音频响应流式写入 writer（同源网关透传）。
func (client *Client) MeetingAudioStream(ctx context.Context, id string, writer http.ResponseWriter) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/knowledge/meetings/"+url.PathEscape(id)+"/audio", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.slowHTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		var apiError api.ErrorResponse
		_ = json.NewDecoder(response.Body).Decode(&apiError)
		return fmt.Errorf("%s", apiError.Message)
	}
	writer.Header().Set("Content-Type", "audio/wav")
	if response.ContentLength > 0 {
		writer.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	}
	writer.WriteHeader(http.StatusOK)
	_, err = io.Copy(writer, response.Body)
	return err
}

// MeetingExportStream 把备忘录导出文件（format: markdown|docx）流式写入 writer。
func (client *Client) MeetingExportStream(ctx context.Context, id, format string, writer http.ResponseWriter) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		client.baseURL+"/api/knowledge/meetings/"+url.PathEscape(id)+"/export/"+url.PathEscape(format), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.slowHTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		var apiError api.ErrorResponse
		_ = json.NewDecoder(response.Body).Decode(&apiError)
		return fmt.Errorf("%s", apiError.Message)
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

// SubscribeEvents 通过 WebSocket 订阅 Core 的实时事件流。
// 每收到一条 JSON 消息就调用 onEvent(rawJSON)。连接断开或 ctx 取消时返回。
// 调用方负责重连逻辑。
func (client *Client) SubscribeEvents(ctx context.Context, onEvent func(raw []byte)) error {
	// http://127.0.0.1:19080 → ws://127.0.0.1:19080/api/events
	wsURL := strings.Replace(client.baseURL, "http://", "ws://", 1) + "/api/events"
	header := http.Header{}
	header.Set("Authorization", "Bearer "+client.token)

	connection, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer connection.CloseNow()
	connection.SetReadLimit(1 << 20) // 1MB 足够

	for {
		_, data, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		onEvent(data)
	}
}
