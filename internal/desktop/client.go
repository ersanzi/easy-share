package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
