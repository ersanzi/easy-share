package drive

import (
	"context"
	"fmt"
	"net/http"
)

// Capabilities 明确区分旧服务端与支持持久化目录的新协议。
type Capabilities struct {
	PersistentDirectories bool `json:"persistentDirectories"`
	ProtocolVersion       int  `json:"protocolVersion"`
}

// Capabilities 不从空对象列表猜测能力，服务端未部署对应接口时直接报错。
func (c *Client) Capabilities(ctx context.Context, token string) (Capabilities, error) {
	var capabilities Capabilities
	if err := c.call(ctx, http.MethodGet, "/easyshare/drive/capabilities", token, nil, &capabilities); err != nil {
		return capabilities, fmt.Errorf("读取云盘协议能力失败: %w", err)
	}
	return capabilities, nil
}

// SupportsPersistentDirectories 仅认可已认证的新协议，不以对象列表推断目录能力。
func (c *Client) SupportsPersistentDirectories(ctx context.Context, token, space string) (bool, error) {
	if space != SpacePersonal && space != SpaceShared {
		return false, fmt.Errorf("未知云空间类型")
	}
	capabilities, err := c.Capabilities(ctx, token)
	if err != nil {
		return false, err
	}
	return capabilities.ProtocolVersion == 1 && capabilities.PersistentDirectories, nil
}
