package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// VersionEntry 一个历史版本。
//
// 版本号用 int64 而不是字符串：它由 es_file.version_id 递增而来，量级远低于
// JS 安全整数上限，不像雪花 fileId 那样有截断风险。
type VersionEntry struct {
	VersionId  int64      `json:"versionId"`
	FileSize   int64      `json:"fileSize"`
	ContentMd5 string     `json:"contentMd5,omitempty"`
	CreatedAt  *time.Time `json:"createdAt,omitempty"`
	CreatedBy  int64      `json:"createdBy,omitempty"`
}

// Versions 某文件的历史版本（版本号倒序）。
//
// 没有覆盖过的文件返回空数组——前端据此显示「暂无历史版本」，
// 而不是把「空」当成错误。
func (c *Client) Versions(ctx context.Context, token, space, path string) ([]VersionEntry, error) {
	var versions []VersionEntry
	endpoint := "/easyshare/drive/versions?space=" + url.QueryEscape(normalizeSpace(space)) +
		"&path=" + url.QueryEscape(path)
	if err := c.call(ctx, http.MethodGet, endpoint, token, nil, &versions); err != nil {
		return nil, fmt.Errorf("获取版本历史失败：%w", err)
	}
	return versions, nil
}

// RestoreVersion 回滚到指定版本。
//
// 控制面会先把当前内容留档再覆盖，所以这个动作本身也是可撤销的。
func (c *Client) RestoreVersion(ctx context.Context, token, space, path string, versionID int64) error {
	body, err := json.Marshal(map[string]any{
		"path": path, "versionId": versionID, "space": normalizeSpace(space),
	})
	if err != nil {
		return err
	}
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/version/restore", token, body, nil); err != nil {
		return fmt.Errorf("恢复版本失败：%w", err)
	}
	return nil
}
