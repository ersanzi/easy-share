package account

import (
	"context"
	"net/url"
	"strings"
)

// 本文件是租户服务配置（服务发现）的客户端：登录后从控制面拉取服务拓扑，
// 员工免手填知识服务地址。设计见 ServiceConfigController（Java 侧）注释。

// ServiceConfig 是控制面登记的租户服务地址。未登记项为空串。
type ServiceConfig struct {
	KnowledgeURL string `json:"knowledgeUrl"`
}

// ServiceConfig 拉取租户服务配置（登录即可读）。
func (c *Client) ServiceConfig(ctx context.Context, token string) (*ServiceConfig, error) {
	var vo struct {
		KnowledgeURL string `json:"knowledgeUrl"`
	}
	if err := c.getJSON(ctx, "/easyshare/service/config", token, &vo); err != nil {
		return nil, err
	}
	return &ServiceConfig{KnowledgeURL: strings.TrimSpace(vo.KnowledgeURL)}, nil
}

// SetServiceConfig 登记知识服务地址（超管；空串=清除，控制面放行）。
func (c *Client) SetServiceConfig(ctx context.Context, token, knowledgeURL string) error {
	body := map[string]string{"knowledgeUrl": strings.TrimSpace(knowledgeURL)}
	return c.putValue(ctx, "/easyshare/service/config", token, body, nil)
}

// DeriveKnowledgeURL 从控制面地址推导知识服务的默认地址（登记缺失时的回退）。
// 公司部署两者同机不同端口（控制面 8090 / 知识服务 8000），推导即同主机换端口：
// http://192.168.1.10:8090 → http://192.168.1.10:8000；控制面未带端口则补 :8000。
// 推不出来（地址非法）返回空串，由调用方决定展示。
func DeriveKnowledgeURL(platformBaseURL string) string {
	base := strings.TrimSpace(platformBaseURL)
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return ""
	}
	if parsed.Port() != "" {
		parsed.Host = parsed.Hostname() + ":8000"
	} else {
		parsed.Host = parsed.Host + ":8000"
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// --- AI 能力配置（管理端配置化；超管读写，密钥掩码回显，见 AiConfigController） ---

// AiConfig 控制面登记的 AI 能力配置。AsrEnabled/AsrSpeakerEnabled 为 null 表示
// 未设置（知识服务沿用 .env）；Pushed=false 时 PushError 说明原因（保存已生效）。
type AiConfig struct {
	LlmBaseUrl        string `json:"llmBaseUrl"`
	LlmModel          string `json:"llmModel"`
	ApiKeySet         bool   `json:"apiKeySet"`
	ApiKeyMasked      string `json:"apiKeyMasked"`
	AsrEnabled        *bool  `json:"asrEnabled"`
	AsrSpeakerEnabled *bool  `json:"asrSpeakerEnabled"`
	Pushed            bool   `json:"pushed"`
	PushError         string `json:"pushError"`
}

// AiConfig 拉取 AI 能力配置（超管）。
func (c *Client) AiConfig(ctx context.Context, token string) (*AiConfig, error) {
	var vo AiConfig
	if err := c.getJSON(ctx, "/easyshare/service/ai-config", token, &vo); err != nil {
		return nil, err
	}
	return &vo, nil
}

// SetAiConfig 保存 AI 能力配置并触发知识服务热生效（超管；控制面负责推送）。
// llmApiKey 传空串 = 保持已有密钥不变。
func (c *Client) SetAiConfig(ctx context.Context, token string, body map[string]any) (*AiConfig, error) {
	var vo AiConfig
	if err := c.putValue(ctx, "/easyshare/service/ai-config", token, body, &vo); err != nil {
		return nil, err
	}
	return &vo, nil
}
