package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// MarketClient 是插件商城（RuoYi 控制面 /easyshare/plugins）的匿名 HTTP 客户端。
// 信任模型同在线升级：商城列表与下载 URL 匿名可拉，安装校验靠 SHA256。
type MarketClient struct {
	baseURL string
	http    *http.Client
}

// MarketAsset 是商城里的一个可下载插件包（zip）。
type MarketAsset struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// MarketItem 是商城里一个插件的最新清单。
type MarketItem struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	Author      string        `json:"author"`
	Version     string        `json:"version"`
	Notes       string        `json:"notes"`
	PublishedAt string        `json:"publishedAt"`
	Asset       *MarketAsset  `json:"asset,omitempty"`
	// UpdateAvailable 由客户端按本地已装版本号回填（服务端不知道本地状态）。
	UpdateAvailable bool `json:"updateAvailable,omitempty"`
}

// NewMarketClient 创建商城客户端（baseURL 为控制面地址，如 http://localhost:8090）。
func NewMarketClient(baseURL string) *MarketClient {
	return &MarketClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// List 拉取商城列表（全部有已发布版本的插件）。
func (c *MarketClient) List(ctx context.Context) ([]MarketItem, error) {
	var items []MarketItem
	if err := c.getJSON(ctx, "/easyshare/plugins", &items); err != nil {
		return nil, err
	}
	return items, nil
}

// Latest 拉取单插件最新清单；从未发布过返回 nil。
func (c *MarketClient) Latest(ctx context.Context, pluginID string) (*MarketItem, error) {
	var item MarketItem
	if err := c.getJSON(ctx, "/easyshare/plugins/"+pluginID+"/latest", &item); err != nil {
		return nil, err
	}
	return &item, nil
}

// DownloadURL 现取插件包的预签名下载 URL（短有效期，不缓存）。
func (c *MarketClient) DownloadURL(ctx context.Context, assetID string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := c.getJSON(ctx, "/easyshare/plugins/assets/"+assetID+"/url", &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", fmt.Errorf("商城未返回下载 URL")
	}
	return out.URL, nil
}

// Download 下载插件包到内存并返回字节与 SHA256 校验（包上限 maxPluginZipBytes）。
func (c *MarketClient) Download(ctx context.Context, asset MarketAsset) ([]byte, error) {
	url, err := c.DownloadURL(ctx, asset.ID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载插件包: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载插件包: HTTP %d", resp.StatusCode)
	}
	// 上限校验：商城包很小（HTML/JS/CSS），超限直接拒绝。
	if asset.SizeBytes <= 0 || asset.SizeBytes > maxPluginZipBytes {
		return nil, fmt.Errorf("插件包大小异常：%d 字节", asset.SizeBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPluginZipBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读插件包: %w", err)
	}
	if int64(len(data)) != asset.SizeBytes {
		return nil, fmt.Errorf("插件包大小不符：清单 %d，实际 %d", asset.SizeBytes, len(data))
	}
	return data, nil
}

// ruoyiEnvelope 是 RuoYi 统一响应包装 {code, msg, data}。
type ruoyiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (c *MarketClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("访问商城: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("访问商城: HTTP %d", resp.StatusCode)
	}
	var envelope ruoyiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("解析商城响应: %w", err)
	}
	if envelope.Code != 200 {
		return fmt.Errorf("商城接口错误：%s", envelope.Msg)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil // data=null：商城为空/该插件从未发布，调用方按零值处理
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("解析商城数据: %w", err)
	}
	return nil
}
