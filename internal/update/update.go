// Package update 实现客户端在线升级：从账号控制面拉版本清单、按清单下载并校验
// 安装包、平台差异化的应用动作。升级源复用 config.json 的 platformBaseUrl，
// 不新增配置；版本比较在客户端本地完成（手写 semver 比较，不引依赖）。
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Asset 是一个可下载的发布资产（安装包/DMG/zip）。
type Asset struct {
	ID       string `json:"id"`
	Platform string `json:"platform"`
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	Size     int64  `json:"sizeBytes"`
	SHA256   string `json:"sha256"`
	Status   string `json:"status"`
}

// Manifest 是控制面 latest 接口返回的版本清单。
type Manifest struct {
	Version     string  `json:"version"`
	Notes       string  `json:"notes"`
	PublishedAt string  `json:"publishedAt"`
	Assets      []Asset `json:"assets"`
}

// SelectAsset 按平台挑资产：Windows 用 installer，macOS 优先 dmg、回退 zip。
// 找不到返回 nil。
func (m *Manifest) SelectAsset(platform string) *Asset {
	var fallback *Asset
	for i := range m.Assets {
		asset := m.Assets[i]
		if asset.Platform != platform {
			continue
		}
		if platform == PlatformWindows && asset.Kind == "installer" {
			return &asset
		}
		if platform == PlatformMacOS {
			if asset.Kind == "dmg" {
				return &asset
			}
			if asset.Kind == "zip" {
				fallback = &asset
			}
		}
	}
	return fallback
}

// 平台标识（与控制面 es_app_release_asset.platform 对齐）。
const (
	PlatformWindows = "windows"
	PlatformMacOS   = "macos"
)

// Client 是控制面升级接口的 HTTP 客户端。匿名接口，无 token。
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient 创建升级客户端；baseURL 为控制面地址（config.json 的 platformBaseUrl）。
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Latest 拉取某平台的最新清单。控制面从未发布过时返回 (nil, nil)，
// 由调用方决定如何提示「暂无发布」。
func (c *Client) Latest(ctx context.Context, platform string) (*Manifest, error) {
	var manifest Manifest
	if err := c.getJSON(ctx, fmt.Sprintf("/easyshare/app/latest?platform=%s", platform), &manifest); err != nil {
		if err == errNoData {
			return nil, nil
		}
		return nil, err
	}
	return &manifest, nil
}

// DownloadURL 现取某资产的预签名下载 URL。预签名有效期短（控制面默认 10 分钟），
// 必须在每次下载发起前调用，不做缓存。
func (c *Client) DownloadURL(ctx context.Context, assetID string) (string, error) {
	var payload struct {
		URL string `json:"url"`
	}
	if err := c.getJSON(ctx, "/easyshare/app/assets/"+assetID+"/url", &payload); err != nil {
		if err == errNoData {
			return "", fmt.Errorf("服务端未返回下载地址")
		}
		return "", err
	}
	if payload.URL == "" {
		return "", fmt.Errorf("服务端返回的下载地址为空")
	}
	return payload.URL, nil
}

// errNoData 表示控制面返回 data 为 null（如该平台从未发布）。
var errNoData = fmt.Errorf("响应 data 为空")

// ruoyiEnvelope 是 RuoYi 统一响应包装。
type ruoyiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// getJSON 请求控制面并解包 RuoYi 包装，data 为 null 时返回 errNoData。
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("构造请求: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("请求控制面: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("控制面返回 HTTP %d", resp.StatusCode)
	}
	var envelope ruoyiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("解析响应: %w", err)
	}
	if envelope.Code != 200 {
		return fmt.Errorf("控制面错误: %s", envelope.Msg)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errNoData
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("解析数据: %w", err)
	}
	return nil
}

// CompareVersions 比较 semver 风格版本号，返回 -1/0/1（a<b、a==b、a>b）。
//
// 规则：可选 v 前缀；主版本任意段数（缺段按 0 补齐，0.1 与 0.1.0 相等）；
// 预发布后缀小于同主版本的无后缀版本（0.1.0-preview.1 < 0.1.0）；预发布标识符
// 按段比较，数字段小于字母段（semver 惯例），全部相等时字段少者小。
// 解析失败的段按字符串比较兜底，保证结果是全序。
func CompareVersions(a, b string) int {
	aMain, aPre := splitVersion(a)
	bMain, bPre := splitVersion(b)
	if result := compareNumericSegments(aMain, bMain); result != 0 {
		return result
	}
	// 主版本相等：无后缀 > 有后缀
	switch {
	case aPre == "" && bPre == "":
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	}
	return comparePreRelease(aPre, bPre)
}

// splitVersion 去掉 v 前缀并分离主版本与预发布后缀。
func splitVersion(v string) (main, pre string) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	// 预发布后缀从第一个 '-' 开始（版本号主段不含 '-'）
	if idx := strings.Index(v, "-"); idx >= 0 {
		return v[:idx], v[idx+1:]
	}
	return v, ""
}

// compareNumericSegments 按点分段比较数字主版本，缺段按 0 补。
func compareNumericSegments(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	length := max(len(aParts), len(bParts))
	for i := 0; i < length; i++ {
		av, bv := "0", "0"
		if i < len(aParts) {
			av = aParts[i]
		}
		if i < len(bParts) {
			bv = bParts[i]
		}
		an, aErr := strconv.Atoi(av)
		bn, bErr := strconv.Atoi(bv)
		if aErr != nil || bErr != nil {
			// 非数字段（脏数据）：字典序兜底，保证全序
			if result := strings.Compare(av, bv); result != 0 {
				return result
			}
			continue
		}
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
	}
	return 0
}

// comparePreRelease 比较预发布后缀：按点分段，数字段小于字母段，前缀相同时字段少者小。
func comparePreRelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < min(len(aParts), len(bParts)); i++ {
		an, aErr := strconv.Atoi(aParts[i])
		bn, bErr := strconv.Atoi(bParts[i])
		switch {
		case aErr != nil && bErr != nil:
			if result := strings.Compare(aParts[i], bParts[i]); result != 0 {
				return result
			}
		case aErr != nil:
			return 1 // 字母标识 > 数字标识（semver：数字总是小于字母数字）
		case bErr != nil:
			return -1
		default:
			switch {
			case an < bn:
				return -1
			case an > bn:
				return 1
			}
		}
	}
	// 公共前缀全部相等：字段少者小（1.0.0-alpha < 1.0.0-alpha.1）
	switch {
	case len(aParts) < len(bParts):
		return -1
	case len(aParts) > len(bParts):
		return 1
	}
	return 0
}
