package account

import (
	"context"
	"fmt"
)

// 本文件是空间与配额的控制面接口。
//
// 空间是「共享 + 个人」两类的统一模型：管理页在一处设定两者的容量，客户端按登录账号读回
// 自己可见的空间。配额的强制点在控制面的 presign-put——客户端拿不到 RustFS 密钥，
// 绕不开签发环节。

// 空间类型。与控制面 EsSpace.TYPE_* 对齐。
const (
	SpacePersonal = "personal"
	SpaceShared   = "shared"
)

// 共享空间权限。与控制面 EsSpaceMember.PERM_* 对齐。
const (
	PermRead  = "read"
	PermWrite = "write"
)

// QuotaUnset 表示未分配容量，客户端显示「待开空间」。
const QuotaUnset int64 = 0

// QuotaUnlimited 表示不限容量。
const QuotaUnlimited int64 = -1

// Space 是一个空间的配额与实时用量。
//
// SpaceID / OwnerID 用 string：雪花 ID 超出 JS 安全整数范围，一路按字符串透传到前端。
type Space struct {
	SpaceID   string `json:"spaceId"`
	SpaceType string `json:"spaceType"`
	OwnerID   string `json:"ownerId"`
	SpaceName string `json:"spaceName"`
	// QuotaBytes 0 未分配、-1 不限。
	QuotaBytes int64 `json:"quotaBytes"`
	// UsedBytes 由控制面实时聚合对象存储得出，不是库里的镜像值。
	UsedBytes int64  `json:"usedBytes"`
	Status    string `json:"status"`
	// Permission 当前登录者对该空间的权限：owner / write / read。
	Permission string `json:"permission"`
}

// Capacity 是容量总览：物理可用、已承诺、实际已用。
//
// 存在的理由：逐空间配额挡不住「承诺总量超过物理磁盘」。没有这个视图，管理员会在
// 不知情的情况下超配，用户则看到「配额还剩很多」但传不上去。
type Capacity struct {
	// Enabled 为 false 表示控制面未配置容量探测路径，池上限不生效。
	Enabled bool `json:"enabled"`
	// UsableBytes 物理可用字节数，未启用时为 -1。
	UsableBytes int64 `json:"usableBytes"`
	// PoolBytes 扣除预留水位后可用于云盘的字节数，未启用时为 -1。
	PoolBytes int64 `json:"poolBytes"`
	// ReservedBytes 预留水位：不允许云盘吃掉的部分。
	ReservedBytes int64 `json:"reservedBytes"`
	// CommittedBytes 已承诺总量：各空间配额之和，不含「不限」的空间。
	CommittedBytes int64 `json:"committedBytes"`
	// UsedBytes 实际总用量（实时聚合）。
	UsedBytes int64 `json:"usedBytes"`
	// UnlimitedCount 配额为「不限」的空间数——它们无法计入已承诺总量，
	// 故该数字大于 0 时 CommittedBytes 是不完整的。
	UnlimitedCount int `json:"unlimitedCount"`
}

// Overcommitted 报告已承诺总量是否超过池容量。
//
// 超配本身是允许的（多数账号用不满，禁止会浪费容量），但必须让管理员看见。
func (c Capacity) Overcommitted() bool {
	return c.Enabled && c.PoolBytes >= 0 && c.CommittedBytes > c.PoolBytes
}

// GetCapacity 取容量总览。仅管理员可调。
func (c *Client) GetCapacity(ctx context.Context, token string) (*Capacity, error) {
	var capacity Capacity
	if err := c.getJSON(ctx, "/easyshare/space/admin/capacity", token, &capacity); err != nil {
		return nil, err
	}
	return &capacity, nil
}

// MySpaces 取当前登录账号可见的空间：个人空间 +（被授权时的）共享空间。
//
// 未开通个人空间时控制面也会返回一行，QuotaBytes 为 0——客户端据此显示「待开空间」，
// 而不是让列表空掉。
func (c *Client) MySpaces(ctx context.Context, token string) ([]Space, error) {
	var spaces []Space
	if err := c.getJSON(ctx, "/easyshare/space/mine", token, &spaces); err != nil {
		return nil, err
	}
	return spaces, nil
}

// ListSpaces 取全部空间（共享在前，其后是各账号的个人空间）。仅管理员可调。
func (c *Client) ListSpaces(ctx context.Context, token string) ([]Space, error) {
	var spaces []Space
	if err := c.getJSON(ctx, "/easyshare/space/admin/list", token, &spaces); err != nil {
		return nil, err
	}
	return spaces, nil
}

// SharedMembers 取共享空间的成员授权：账号 ID → read/write。仅管理员可调。
func (c *Client) SharedMembers(ctx context.Context, token string) (map[string]string, error) {
	members := map[string]string{}
	if err := c.getJSON(ctx, "/easyshare/space/admin/shared-members", token, &members); err != nil {
		return nil, err
	}
	return members, nil
}

// SetPersonalQuota 设定某账号的个人空间容量。
//
// 没有空间行则开通，有则改容量——「开通」与「改配额」是同一个动作。
// quotaBytes 传 0 收回（回到待开空间），传 -1 不限。
func (c *Client) SetPersonalQuota(ctx context.Context, token, userID string, quotaBytes int64) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	body := map[string]any{"userId": userID, "quotaBytes": quotaBytes}
	return c.postValue(ctx, "/easyshare/space/admin/personal-quota", token, body, nil)
}

// SetSharedQuota 设定共享空间容量。
func (c *Client) SetSharedQuota(ctx context.Context, token string, quotaBytes int64) error {
	body := map[string]any{"quotaBytes": quotaBytes}
	return c.postValue(ctx, "/easyshare/space/admin/shared-quota", token, body, nil)
}

// GrantShared 授予或撤销某账号对共享空间的权限。permission 传空字符串即撤销。
func (c *Client) GrantShared(ctx context.Context, token, userID, permission string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	if permission != "" && permission != PermRead && permission != PermWrite {
		return fmt.Errorf("权限取值只能是 read 或 write")
	}
	body := map[string]any{"userId": userID, "permission": permission}
	return c.postValue(ctx, "/easyshare/space/admin/shared-grant", token, body, nil)
}

// validateUserID 只放行纯数字的雪花 ID。
//
// 这些值来自前端，虽然只进请求体而非 URL 路径，仍在客户端先挡一道：控制面也会再校验，
// 但把明显的脏值挡在本地能给出更快、更清楚的报错。
func validateUserID(userID string) error {
	if userID == "" {
		return fmt.Errorf("账号标识不能为空")
	}
	for _, ch := range userID {
		if ch < '0' || ch > '9' {
			return fmt.Errorf("账号标识非法")
		}
	}
	return nil
}
