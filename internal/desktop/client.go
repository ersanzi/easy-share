package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"easyshare/internal/api"
	"easyshare/internal/cloud"
	"easyshare/internal/discovery"
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
}

func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 10 * time.Second}}
}
func (client *Client) request(ctx context.Context, method, path string, input, output any) error {
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
	response, err := client.http.Do(request)
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

func (client *Client) CloudList(ctx context.Context) ([]cloud.File, error) {
	var result []cloud.File
	err := client.request(ctx, http.MethodGet, "/api/cloud/files", nil, &result)
	return result, err
}

// CloudPreview 获取后端声明的预览能力，并将相对内容地址补全为 Core 地址。
func (client *Client) CloudPreview(ctx context.Context, key string) (cloud.Preview, error) {
	var result cloud.Preview
	err := client.request(ctx, http.MethodGet, "/api/cloud/preview?key="+url.QueryEscape(key), nil, &result)
	if err != nil {
		return cloud.Preview{}, err
	}
	if strings.HasPrefix(result.ContentURL, "/") {
		result.ContentURL = client.baseURL + result.ContentURL
	}
	return result, nil
}

func (client *Client) CloudUpload(ctx context.Context, filePath string) (cloud.UploadResult, error) {
	var result cloud.UploadResult
	err := client.request(ctx, http.MethodPost, "/api/cloud/upload", map[string]string{"filePath": filePath}, &result)
	return result, err
}

// ProgressFunc reports upload progress: bytes sent so far and total file size.
type ProgressFunc func(sent, total int64)

type progressReader struct {
	reader     io.Reader
	sent       int64
	total      int64
	onProgress ProgressFunc
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.sent += int64(n)
	if pr.onProgress != nil {
		pr.onProgress(pr.sent, pr.total)
	}
	return n, err
}

// CloudUploadStream uploads a local file via multipart form data, reporting
// progress through the callback. This enables real-time progress in the UI.
func (client *Client) CloudUploadStream(ctx context.Context, filePath string, onProgress ProgressFunc) (cloud.UploadResult, error) {
	return client.CloudUploadStreamWithKey(ctx, filePath, "", onProgress)
}

// CloudUploadStreamWithKey 与 CloudUploadStream 相同，但可通过 objectKey 指定
// 含路径的对象键（如 "photos/2024/img.jpg"）。objectKey 为空时退化为文件名。
func (client *Client) CloudUploadStreamWithKey(ctx context.Context, filePath, objectKey string, onProgress ProgressFunc) (cloud.UploadResult, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return cloud.UploadResult{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return cloud.UploadResult{}, fmt.Errorf("stat file: %w", err)
	}

	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)

	go func() {
		part, partErr := writer.CreateFormFile("file", filepath.Base(filePath))
		if partErr != nil {
			_ = pipeWriter.CloseWithError(partErr)
			return
		}
		pr := &progressReader{reader: file, total: info.Size(), onProgress: onProgress}
		if _, copyErr := io.Copy(part, pr); copyErr != nil {
			_ = pipeWriter.CloseWithError(copyErr)
			return
		}
		_ = writer.Close()
		_ = pipeWriter.Close()
	}()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/cloud/upload", pipeReader)
	if err != nil {
		return cloud.UploadResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-File-Size", strconv.FormatInt(info.Size(), 10))
	if objectKey != "" {
		request.Header.Set("X-Object-Key", objectKey)
	}

	// Use a dedicated client with no timeout for large file uploads.
	uploadClient := &http.Client{Timeout: 0}
	response, err := uploadClient.Do(request)
	if err != nil {
		return cloud.UploadResult{}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= 300 {
		var apiError api.ErrorResponse
		_ = json.NewDecoder(response.Body).Decode(&apiError)
		return cloud.UploadResult{}, fmt.Errorf("%s", apiError.Message)
	}

	var result cloud.UploadResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return cloud.UploadResult{}, err
	}
	return result, nil
}
func (client *Client) CloudDownload(ctx context.Context, key string) (string, error) {
	var result struct {
		URL string `json:"url"`
	}
	err := client.request(ctx, http.MethodPost, "/api/cloud/download", map[string]string{"key": key}, &result)
	return result.URL, err
}
func (client *Client) CloudDelete(ctx context.Context, key string) error {
	return client.request(ctx, http.MethodDelete, "/api/cloud/files", map[string]string{"key": key}, nil)
}
func (client *Client) CloudShare(ctx context.Context, key string, expiryHours int) (string, error) {
	var result struct {
		URL string `json:"url"`
	}
	err := client.request(ctx, http.MethodPost, "/api/cloud/share", map[string]any{"key": key, "expiryHours": expiryHours}, &result)
	return result.URL, err
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
