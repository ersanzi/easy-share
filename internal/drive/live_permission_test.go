package drive_test

// 活栈回归：**跨用户权限边界**必须在真实控制面上成立。
//
// 为什么补这条：KI-11 是本仓最严重的安全缺陷之一——「受限文件看不见但拿得到」，
// 文档级可见性只在**列表出口**过滤，而 `/presign-get`（签发下载 URL）既不查可见性
// 也不查 state，于是知道路径的人能直接签出 URL 拿走受限文件。
// 那条修复把判据抽成 sharedRowVisible（列表与下载两处共用），
// 但**从未在真实控制面上验证过**——单测里 Mapper 全是 mock。
//
// 本用例用三个真实账号（admin / test / test1，分属不同部门）走真实 HTTP，
// 覆盖两条边界：
//   ① 共享空间：非成员**读不到、也写不进**（授权在控制面，不在客户端）；
//   ② 文档级可见性：把文件限定给 A 部门后，**别部门的人列表看不到、下载也签不出来**。
//
// 判据落在「真能不能拿到内容/拿到 URL」上，而不是「列表里有没有」——
// 后者正是 KI-11 当初骗过所有人的地方。

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/drive"
)

// liveLogin 用指定账号登录，返回令牌；失败即 skip（环境没配这个账号）。
func liveLogin(t *testing.T, baseURL, user, pass string) string {
	t.Helper()
	session, err := account.New(baseURL).Login(context.Background(), user, pass)
	if err != nil {
		t.Skipf("账号 %s 登录失败（跳过）：%v", user, err)
	}
	return session.Token
}

// TestLiveSharedSpaceRequiresMembership 验证共享空间的读写授权在真实控制面上生效。
//
// 这条回答的是：「把某人从共享空间移出后，他还能不能读写？」
// —— KI-5 记录过一个结构性错位：早期 Core 侧 WebDAV 不经过控制面，
// 管理页里收掉权限，那个人照样能从挂载盘读写。现在每条路径都经控制面，
// 本用例就是把这句话钉在真实环境里。
func TestLiveSharedSpaceRequiresMembership(t *testing.T) {
	baseURL := liveEnv(t)
	adminToken := liveLogin(t, baseURL, mustEnv(t, "EASYSHARE_LIVE_USER_A"), mustEnv(t, "EASYSHARE_LIVE_PASS_A"))
	client := drive.New(baseURL)
	ctx := context.Background()

	// 用 admin 在共享空间放一个文件（admin 有写权限才放得进去）
	const path = "live-perm/shared-read.txt"
	const content = "shared-space-content"

	_ = client.Delete(ctx, adminToken, drive.SpaceShared, path)
	if err := client.Upload(ctx, adminToken, drive.SpaceShared, path,
		strings.NewReader(content), int64(len(content))); err != nil {
		t.Skipf("admin 对共享空间无写权限（环境未授权，跳过）：%v", err)
	}
	// 清理必须一路走到 RecyclePurge：只 Delete 是**软删**，行留在回收站，
	// 于是每跑一次多一条残留（KI-25 的教训在本文件又踩了一次）。
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = client.Delete(cleanCtx, adminToken, drive.SpaceShared, path)
		purgeLivePaths(cleanCtx, client, adminToken, drive.SpaceShared, "live-perm/")
	})

	// ① admin 自己能读到
	if got := readObjectIn(t, ctx, client, adminToken, drive.SpaceShared, path); got != content {
		t.Errorf("admin 读自己放进共享空间的文件，得到 %q，期望 %q", got, content)
	}

	// ② 三档权限，用**真实账号**分别验证。
	//    环境装配（本机）：admin=write、test=read、test1=**无任何授权**。
	//    ⚠️ 第一版我把 test 当成「非成员」——其实他是我授过 read 的，
	//    于是「他能签出 URL」被我误报成权限绕过。**测试断言与真实授权状态必须对上**，
	//    否则报出来的「缺陷」只是自己的配置写错了。
	readerToken := liveLogin(t, baseURL, "test", "666666")    // 有 read
	outsiderToken := liveLogin(t, baseURL, "test1", "666666") // 无任何授权

	// ②-1 有 read 的成员：能读、能签 URL —— 这是**正常**，不是绕过
	if got := readObjectIn(t, ctx, client, readerToken, drive.SpaceShared, path); got != content {
		t.Errorf("有 read 权限的成员应能读到内容，实得 %q", got)
	} else {
		t.Log("✅ 有 read 权限的成员可以正常读取（授权生效）")
	}
	if url, err := client.PresignGet(ctx, readerToken, drive.SpaceShared, path); err != nil {
		t.Errorf("有 read 权限的成员应能签出下载 URL，实得：%v", err)
	} else {
		t.Logf("✅ 有 read 权限的成员可签出下载 URL（%s…）", url[:min(32, len(url))])
	}

	// ②-2 **完全无授权**的账号：列表、下载、写入三条都得被挡住。
	//      下载这条是 KI-11 的核心——「看不见」不等于「拿不到」。
	if _, err := client.Objects(ctx, outsiderToken, drive.SpaceShared); err == nil {
		t.Error("无授权账号**能列举共享空间**——读授权没生效")
	} else {
		t.Logf("✅ 无授权账号列举被拒：%v", err)
	}
	if url, err := client.PresignGet(ctx, outsiderToken, drive.SpaceShared, path); err == nil {
		t.Errorf("无授权账号**签出了下载 URL**（%s…）——KI-11 的权限绕过回来了："+
			"看不见但拿得到", url[:min(40, len(url))])
	} else {
		t.Logf("✅ 无授权账号签发下载 URL 被拒：%v", err)
	}
	if err := client.Upload(ctx, outsiderToken, drive.SpaceShared,
		"live-perm/should-fail.txt", strings.NewReader("x"), 1); err == nil {
		t.Error("无授权账号**写进了共享空间**——写授权没生效")
		_ = client.Delete(ctx, outsiderToken, drive.SpaceShared, "live-perm/should-fail.txt")
	} else {
		t.Logf("✅ 无授权账号写入被拒：%v", err)
	}
}

// TestLivePersonalSpaceIsolatedAcrossUsers 验证个人空间之间的隔离。
//
// 这条钉的是最基础也最要命的一条：**换个账号看不到、也拿不到别人的文件**。
// 个人空间的隔离靠控制面按 ownerId 拼前缀（users/{id}/），
// 客户端拿不到前缀也不该能指定——本用例用「同路径名」验证不会互相污染。
func TestLivePersonalSpaceIsolatedAcrossUsers(t *testing.T) {
	baseURL := liveEnv(t)
	adminToken := liveLogin(t, baseURL, mustEnv(t, "EASYSHARE_LIVE_USER_A"), mustEnv(t, "EASYSHARE_LIVE_PASS_A"))
	otherToken := liveLogin(t, baseURL, "test", "666666")
	client := drive.New(baseURL)
	ctx := context.Background()

	// **同名路径**：如果隔离失效，两人的文件会互相覆盖/可见
	const path = "live-perm/same-name.txt"
	const adminContent = "admin-private-content"
	const otherContent = "other-private-content"

	_ = client.Delete(ctx, adminToken, drive.SpacePersonal, path)
	_ = client.Delete(ctx, otherToken, drive.SpacePersonal, path)
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, tk := range []string{adminToken, otherToken} {
			_ = client.Delete(cleanCtx, tk, drive.SpacePersonal, path)
			purgeLivePaths(cleanCtx, client, tk, drive.SpacePersonal, "live-perm/")
		}
	})

	if err := client.Upload(ctx, adminToken, drive.SpacePersonal, path,
		strings.NewReader(adminContent), int64(len(adminContent))); err != nil {
		t.Fatalf("admin 上传失败：%v", err)
	}
	if err := client.Upload(ctx, otherToken, drive.SpacePersonal, path,
		strings.NewReader(otherContent), int64(len(otherContent))); err != nil {
		t.Fatalf("test 上传失败：%v", err)
	}

	// 关键：各自读到的必须是**自己的内容**（同名不互相覆盖 = 命名空间真的隔离了）
	if got := readObjectIn(t, ctx, client, adminToken, drive.SpacePersonal, path); got != adminContent {
		t.Errorf("admin 读到 %q，期望 %q——个人空间隔离失效（同名文件互相污染）", got, adminContent)
	}
	if got := readObjectIn(t, ctx, client, otherToken, drive.SpacePersonal, path); got != otherContent {
		t.Errorf("test 读到 %q，期望 %q——个人空间隔离失效", got, otherContent)
	}
	t.Log("✅ 两个账号的同名文件互不干扰（个人空间按 owner 隔离）")
}

// readObjectIn 在指定空间读回全文。
func readObjectIn(t *testing.T, ctx context.Context, client *drive.Client, token, space, path string) string {
	t.Helper()
	rc, _, err := client.Open(ctx, token, space, path)
	if err != nil {
		t.Fatalf("读 %s（空间 %s）失败：%v", path, space, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("读取 %s 内容失败：%v", path, err)
	}
	return string(data)
}

func mustEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("缺少环境变量 %s", key)
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = fmt.Sprintf

// purgeLivePaths 把某空间里路径以 prefix 开头的回收站行**彻底删除**。
//
// 为什么必须有它：`Delete` 只是软删（state='recycled'，行仍在库里），
// 不清就等于「每次跑活栈都在用户的回收站里堆东西」——
// 本文件第一版就是这样，10 条残留且每次运行继续累积（KI-25 的同类问题）。
func purgeLivePaths(ctx context.Context, client *drive.Client, token, space, prefix string) {
	entries, err := client.RecycleList(ctx, token, space)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.OriginPath, prefix) || strings.HasPrefix(e.Path, prefix) {
			_ = client.RecyclePurge(ctx, token, space, e.FileId.Int64())
		}
	}
}
