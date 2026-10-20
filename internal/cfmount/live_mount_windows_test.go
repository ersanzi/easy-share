//go:build windows

package cfmount

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
)

// 活栈 + 真机：真实控制面（/changes 与预签名下载）+ 真实 Windows CfApi。
//
// 单测证明的是「请求形态对」，集成测试证明的是「假客户端下能跑」，
// 两者都不等于**服务端真实返回的变更事件**能在本机落成可用占位符：
// 事件里的 mtime 是 `2026-10-17 01:43:23` 这种本地时间串，解析失败就是零值，
// 而零值时间在 FFI 边界上会把整条创建请求打回（0x80070057，本轮就是这么撞出来的）。
//
// 门控与 internal/spacedav 的活栈用例一致：未设 EASYSHARE_LIVE_DRIVE 时跳过。
type countingClient struct {
	*drive.Client
	objectsCalls int
	changesCalls int
	cursors      []*int64
}

func (c *countingClient) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	c.objectsCalls++
	return c.Client.Objects(ctx, token, space)
}

func (c *countingClient) Changes(ctx context.Context, token, space string, since *int64) (*drive.Changes, error) {
	c.changesCalls++
	c.cursors = append(c.cursors, since)
	return c.Client.Changes(ctx, token, space, since)
}

func TestLiveMountUsesIncrementalListing(t *testing.T) {
	if os.Getenv("EASYSHARE_LIVE_DRIVE") == "" {
		t.Skip("未设 EASYSHARE_LIVE_DRIVE，跳过活栈测试")
	}
	if !cfapi.Available() {
		t.Skip("本机无 CfApi")
	}
	baseURL := os.Getenv("EASYSHARE_LIVE_BASE")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8090"
	}
	user, pass := os.Getenv("EASYSHARE_LIVE_USER_A"), os.Getenv("EASYSHARE_LIVE_PASS_A")
	if user == "" || pass == "" {
		t.Skip("缺少 EASYSHARE_LIVE_USER_A / EASYSHARE_LIVE_PASS_A")
	}

	session, err := account.New(baseURL).Login(context.Background(), user, pass)
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}
	token := session.Token
	raw := drive.New(baseURL)
	client := &countingClient{Client: raw}

	const dir = "live-cfmount"
	first := dir + "/first.txt"
	second := dir + "/second.txt"
	body := strings.Repeat("a", 4096)

	// 前置：清掉上次运行的残留
	for _, p := range []string{first, second} {
		_ = raw.Delete(context.Background(), token, drive.SpacePersonal, p)
	}
	purgeRecycled(t, raw, token, dir)

	if err := raw.Upload(context.Background(), token, drive.SpacePersonal, first,
		strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("上传第一个文件失败：%v", err)
	}

	root := t.TempDir()
	cfg := Config{
		RootPath:     root,
		Space:        drive.SpacePersonal,
		Client:       client,
		Token:        func() string { return token },
		ProviderName: "EasyShareTest",
		ProviderID:   testGUID,
		State:        NewListState(),
	}
	m, err := cfapi.MountSpace(cfapi.MountOptions{
		RootPath:         root,
		Provider:         cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "0.1.0", ID: cfg.ProviderID},
		SyncRootIdentity: []byte("easyshare-live-incremental"),
		List:             List(cfg),
		Open:             Open(cfg),
	})
	if err != nil {
		t.Fatalf("MountSpace 失败：%v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close 失败：%v", err)
		}
		purgeRecycled(t, raw, token, dir)
		_ = raw.Delete(context.Background(), token, drive.SpacePersonal, second)
	}()

	// ① 第一个文件应成为本地占位符，逻辑大小是云端的真实大小
	path := filepath.Join(root, filepath.FromSlash(dir), "first.txt")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("第一个占位符没建出来：%v", err)
	}
	if fi.Size() != int64(len(body)) {
		t.Errorf("占位符逻辑大小 = %d，期望 %d", fi.Size(), len(body))
	}
	if got := readLivePlaceholder(t, path); got != body {
		t.Errorf("按需取数内容不符：读到 %d 字节，期望 %d", len(got), len(body))
	}

	// ② 云端新增第二个文件后同步：必须靠增量把新占位符建出来
	if err := raw.Upload(context.Background(), token, drive.SpacePersonal, second,
		strings.NewReader("second body"), 11); err != nil {
		t.Fatalf("上传第二个文件失败：%v", err)
	}
	if err := m.Sync(List(cfg)); err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	if client.objectsCalls != 0 {
		t.Errorf("支持增量后一次都不该用旧的全量列举接口，objectsCalls=%d", client.objectsCalls)
	}
	if client.changesCalls < 2 {
		t.Fatalf("至少应有两轮 /changes（首轮全量 + 次轮增量），实得 %d", client.changesCalls)
	}
	if client.cursors[0] != nil {
		t.Fatalf("首轮不该带游标")
	}
	if client.cursors[1] == nil {
		t.Fatalf("次轮应带游标")
	}
	if *client.cursors[1] <= 0 {
		t.Errorf("次轮游标应是服务端返回的空间版本，实得 %d", *client.cursors[1])
	}

	// ③ 新文件真的落成了可读的占位符——事件里的元数据必须被正确消化
	path2 := filepath.Join(root, filepath.FromSlash(dir), "second.txt")
	fi2, err := os.Stat(path2)
	if err != nil {
		t.Fatalf("增量事件没建出第二个占位符：%v", err)
	}
	if fi2.Size() != 11 {
		t.Errorf("第二个占位符逻辑大小 = %d，期望 11", fi2.Size())
	}
	if got := readLivePlaceholder(t, path2); got != "second body" {
		t.Errorf("第二个占位符内容不符：%q", got)
	}
}

// readLivePlaceholder 读一个占位符并返回文本内容（触发一次真实的按需取数）。
func readLivePlaceholder(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读占位符 %s 失败：%v", filepath.Base(path), err)
	}
	return string(data)
}

// purgeRecycled 把某前缀下进了回收站的行彻底删掉，不给活栈留垃圾。
func purgeRecycled(t *testing.T, client *drive.Client, token, prefix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entries, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Logf("列回收站失败（忽略）：%v", err)
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Path, prefix) || strings.HasPrefix(e.OriginPath, prefix) {
			if err := client.RecyclePurge(ctx, token, drive.SpacePersonal, e.FileId.Int64()); err != nil {
				t.Logf("彻底删除 %s 失败（忽略）：%v", e.Path, err)
			}
		}
	}
}
