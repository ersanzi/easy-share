package drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// RecycleOperationResult 是服务端确认的回收批次结果；所有身份均保持字符串，
// 可以无损交给 JS。Directory、Path、MemberCount 均使用服务端快照，不由请求拼造。
type RecycleOperationResult struct {
	BatchId     string `json:"batchId"`
	OperationId string `json:"operationId"`
	FileId      string `json:"fileId"`
	Path        string `json:"path"`
	Directory   bool   `json:"directory"`
	Status      string `json:"status"`
	MemberCount int    `json:"memberCount"`
}

// ErrSafeRecycleUnsupported 表示服务端没有显式声明安全回收协议；不能退回按路径删除。
var ErrSafeRecycleUnsupported = errors.New("云盘服务端不支持安全回收操作，请升级服务端")

func (c *Client) requireSafeRecycle(ctx context.Context, token, space string) error {
	supported, err := c.SupportsSafeRecycle(ctx, token, space)
	if err != nil {
		return fmt.Errorf("无法确认安全回收能力：%w", err)
	}
	if !supported {
		return ErrSafeRecycleUnsupported
	}
	return nil
}

func validateRecycleIdentity(fileID int64, operationID string) error {
	if fileID <= 0 {
		return fmt.Errorf("安全回收需要有效的稳定文件 ID")
	}
	if strings.TrimSpace(operationID) == "" || operationID != strings.TrimSpace(operationID) {
		return fmt.Errorf("安全回收需要有效的原操作 ID")
	}
	return nil
}

func (result RecycleOperationResult) validate(fileID int64, operationID string, requireBatch bool) error {
	if result.FileId != strconv.FormatInt(fileID, 10) {
		return fmt.Errorf("回收操作结果文件 ID 不一致：got %q, want %d", result.FileId, fileID)
	}
	if operationID != "" && result.OperationId != operationID {
		return fmt.Errorf("回收操作结果操作 ID 不一致：got %q, want %q", result.OperationId, operationID)
	}
	if strings.TrimSpace(result.Path) == "" || result.MemberCount < 1 {
		return fmt.Errorf("回收操作结果不完整：缺少路径或有效成员数量")
	}
	if requireBatch && (strings.TrimSpace(result.BatchId) == "" || strings.TrimSpace(result.OperationId) == "") {
		return fmt.Errorf("回收操作结果不完整：缺少批次或操作 ID")
	}
	return nil
}

// DeleteOperation 按稳定身份、已读版本和调用方持久化的 operationID 安全回收。
// expectedVersion 必须明确给出（0 是有效版本）；递归目录必须显式 recursive=true。
// 请求只发送一次；结果不明时用 QueryRecycleOperation 核对原操作，禁止退回 Path 重删。
// restored 表示该旧删除请求已被还原，原样返回，不能当作这次又完成了删除。
func (c *Client) DeleteOperation(ctx context.Context, token, space string, fileID int64, path string,
	expectedVersion *int64, operationID string, recursive bool) (*RecycleOperationResult, error) {
	if err := validateRecycleIdentity(fileID, operationID); err != nil {
		return nil, err
	}
	if expectedVersion == nil || *expectedVersion < 0 {
		return nil, fmt.Errorf("安全回收需要明确的非负期望版本")
	}
	if err := c.requireSafeRecycle(ctx, token, space); err != nil {
		return nil, err
	}
	payload := map[string]any{
		"fileId": fileID, "space": normalizeSpace(space), "expectedVersion": *expectedVersion,
		"operationId": operationID, "recursive": recursive,
	}
	if path != "" {
		payload["path"] = path
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var result RecycleOperationResult
	if err := c.call(ctx, http.MethodDelete, "/easyshare/drive/object", token, body, &result); err != nil {
		return nil, fmt.Errorf("安全回收操作失败（请核对原操作 %q）：%w", operationID, err)
	}
	if err := result.validate(fileID, operationID, true); err != nil {
		return nil, err
	}
	if result.Status != "recycled" && result.Status != "restored" {
		return nil, fmt.Errorf("删除操作尚未确认回收，状态为 %q；请核对原操作 %q", result.Status, operationID)
	}
	return &result, nil
}

// QueryRecycleOperation 只查询原 operationID，并核对原稳定 fileID。
// preparing/restoring/purging 等状态原样返回，调用方仅可把 recycled 当作确认回收。
func (c *Client) QueryRecycleOperation(ctx context.Context, token, space string, fileID int64,
	operationID string) (*RecycleOperationResult, error) {
	if err := validateRecycleIdentity(fileID, operationID); err != nil {
		return nil, err
	}
	if err := c.requireSafeRecycle(ctx, token, space); err != nil {
		return nil, err
	}
	query := url.Values{"space": {normalizeSpace(space)}, "operationId": {operationID}}
	var result RecycleOperationResult
	if err := c.call(ctx, http.MethodGet, "/easyshare/drive/recycle/operation?"+query.Encode(), token, nil, &result); err != nil {
		return nil, fmt.Errorf("查询原回收操作失败：%w", err)
	}
	if err := result.validate(fileID, operationID, true); err != nil {
		return nil, err
	}
	switch result.Status {
	case "preparing", "recycled", "restoring", "restored", "purging", "purged":
		return &result, nil
	default:
		return nil, fmt.Errorf("未知回收操作状态 %q", result.Status)
	}
}

// RestoreOperation 按回收站根 fileID 还原整个批次，返回服务端的准确结果。
// 旧回收站条目可能没有批次/操作 ID，仍接受新服务端明确返回的 restored 结果。
func (c *Client) RestoreOperation(ctx context.Context, token, space string, fileID int64) (*RecycleOperationResult, error) {
	if fileID <= 0 {
		return nil, fmt.Errorf("还原回收操作需要有效的文件 ID")
	}
	if err := c.requireSafeRecycle(ctx, token, space); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"fileId": fileID, "space": normalizeSpace(space)})
	if err != nil {
		return nil, err
	}
	var result RecycleOperationResult
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/recycle/restore", token, body, &result); err != nil {
		return nil, fmt.Errorf("还原回收操作失败：%w", err)
	}
	if err := result.validate(fileID, "", false); err != nil {
		return nil, err
	}
	if result.Status != "restored" {
		return nil, fmt.Errorf("还原操作尚未确认，状态为 %q", result.Status)
	}
	return &result, nil
}
