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

// Health 远端 /health 中面板关心的子集。
type Health struct {
	Records   int    `json:"records"`
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
