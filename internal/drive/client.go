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
	FileId       int64     `json:"fileId"`
}

// Client 连接控制面的存储授权接口。baseURL 形如 http://localhost:8090。
type Client struct {
	baseURL string
	http    *http.Client
	// transfer 用于直传/直取对象存储：大文件耗时远超控制面调用，故单独放宽超时。
	transfer *http.Client
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
type presignData struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

// Objects 列举指定空间内的对象。space 传空按个人空间。
func (c *Client) Objects(ctx context.Context, token, space string) ([]Object, error) {
	var objects []Object
	endpoint := "/easyshare/drive/objects?space=" + url.QueryEscape(normalizeSpace(space))
	if err := c.call(ctx, http.MethodGet, endpoint, token, nil, &objects); err != nil {
		return nil, err
	}
	return objects, nil
}

// PresignPut 取上传用预签名 URL。
//
// size 必须是真实字节数：控制面按它判配额，只有签发这一刻能拦住写入（签出后字节直传对象
// 存储，控制面不在数据路径上）。size 为负（长度未知）时按 0 上报，配额只能拦住已经写满的
// 空间，拦不住这一次。
func (c *Client) PresignPut(ctx context.Context, token, space, path string, size int64) (string, error) {
	if size < 0 {
		size = 0
	}
	body, err := json.Marshal(map[string]any{
		"path":  path,
		"space": normalizeSpace(space),
		"size":  size,
	})
	if err != nil {
		return "", err
	}
	return c.presignWith(ctx, token, "/easyshare/drive/presign-put", path, body)
}

// PresignGet 取下载用预签名 URL。
func (c *Client) PresignGet(ctx context.Context, token, space, path string) (string, error) {
	return c.presign(ctx, token, "/easyshare/drive/presign-get", space, path)
}

// Delete 删除指定空间内的对象。
func (c *Client) Delete(ctx context.Context, token, space, path string) error {
	body, err := json.Marshal(map[string]string{"path": path, "space": normalizeSpace(space)})
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodDelete, "/easyshare/drive/object", token, body, nil)
}

// normalizeSpace 兜空空间名。空字符串按个人空间——旧调用方不传时行为不变。
func normalizeSpace(space string) string {
	if space == "" {
		return SpacePersonal
	}
	return space
}

// Upload 换取预签名 URL 后把内容直传对象存储。size 为负表示长度未知。
func (c *Client) Upload(ctx context.Context, token, space, path string, body io.Reader, size int64) error {
	target, err := c.PresignPut(ctx, token, space, path, size)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, body)
	if err != nil {
		return err
	}
	// 预签名刻意不签 Content-Type，故这里不设该头，避免签名不匹配。
	if size >= 0 {
		req.ContentLength = size
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return fmt.Errorf("上传到对象存储失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("上传 %s 失败：对象存储返回 %s", path, resp.Status)
	}
	return nil
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
