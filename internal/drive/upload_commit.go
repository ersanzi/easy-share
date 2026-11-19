package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// UploadCommitResult 是控制面 HEAD 核验后的文件身份；Long 可为 JSON 数字或字符串。
type UploadCommitResult struct {
	FileID   FlexInt64 `json:"fileId"`
	Version  FlexInt64 `json:"version"`
	Size     FlexInt64 `json:"size"`
	Verified bool      `json:"verified"`
}

// CommitUpload 在字节上传成功后要求控制面核验真实对象并失效空间用量缓存。
// 不自动探测 capabilities；只有装配方确认 UploadCommitVerification 才启用
// Client.VerifyUploadCommit。显式调用本方法总会核验，不受该开关影响。
func (c *Client) CommitUpload(ctx context.Context, token, space, path string, size int64,
	contentMd5 string) (*UploadCommitResult, error) {
	body, err := json.Marshal(struct {
		Path       string `json:"path"`
		Space      string `json:"space"`
		Size       int64  `json:"size"`
		ContentMd5 string `json:"contentMd5"`
	}{path, normalizeSpace(space), size, contentMd5})
	if err != nil {
		return nil, fmt.Errorf("编码上传完成核验请求失败：%w", err)
	}
	var proof struct {
		FileID   *FlexInt64 `json:"fileId"`
		Version  *FlexInt64 `json:"version"`
		Size     *FlexInt64 `json:"size"`
		Verified bool       `json:"verified"`
	}
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/upload-commit", token, body, &proof); err != nil {
		return nil, fmt.Errorf("上传完成核验失败：%w", err)
	}
	// HTTP 200 / code 200 不等于对象落地：缺少 size 也不能被空文件的默认 0 蒙混。
	if !proof.Verified || proof.Size == nil || proof.Size.Int64() != size ||
		proof.FileID == nil || proof.FileID.Int64() <= 0 || proof.Version == nil || proof.Version.Int64() <= 0 {
		return nil, fmt.Errorf("上传完成核验失败：服务端未确认文件身份、版本或大小")
	}
	return &UploadCommitResult{FileID: *proof.FileID, Version: *proof.Version, Size: *proof.Size, Verified: true}, nil
}
