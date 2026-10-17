package drive_test

// 活栈回归：**配额闸门**必须在真实控制面上挡住超额写入。
//
// 为什么补这条：配额是「防把服务器磁盘撑爆」的最后一道闸门，而它的权威文档
// （SpaceService.assertFits）明确区分了两件**用户处置方式完全不同**的事：
//   - 逐空间配额超了 → 用户**自己删文件**就行；
//   - 存储池超了   → 用户删自己的文件**没用**，得找管理员。
// 两者混成一句会让用户白忙。这条判据从未在真实环境验证过。
//
// 另一个已知边界：`size` 为负（长度未知）时客户端按 0 上报，
// 那时**配额拦不住这一次**（注释里写明）。本用例把这个事实也钉住——
// 「知道自己拦不住什么」比「以为都拦得住」安全。

import (
	"context"
	"strings"
	"testing"

	"easyshare/internal/account"
	"easyshare/internal/drive"
)

// TestLiveQuotaBlocksOversizedUpload 验证超过个人空间配额的写入被拒，且错误可读。
func TestLiveQuotaBlocksOversizedUpload(t *testing.T) {
	baseURL := liveEnv(t)
	adminUser := mustEnv(t, "EASYSHARE_LIVE_USER_A")
	adminPass := mustEnv(t, "EASYSHARE_LIVE_PASS_A")
	token := liveLogin(t, baseURL, adminUser, adminPass)
	client := drive.New(baseURL)
	ctx := context.Background()

	// 把 admin 的配额临时收成 1 MiB —— 比任何真实测试文件都小，
	// 于是「超配额」这件事可以用极小代价稳定复现。
	ac := account.New(baseURL)
	session, err := ac.Login(ctx, adminUser, adminPass)
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}
	if err := ac.SetPersonalQuota(ctx, session.Token, "1761100000000000001", 1<<20); err != nil {
		t.Skipf("无法调整配额（跳过）：%v", err)
	}
	// 恢复原配额（1 GiB）——必须注册成 cleanup，失败也要恢复，
	// 否则一次失败的运行会把环境留在「1 MiB 配额」，后续用例全被连累。
	t.Cleanup(func() {
		restoreCtx := context.Background()
		if err := ac.SetPersonalQuota(restoreCtx, session.Token, "1761100000000000001", 1<<30); err != nil {
			t.Logf("⚠️ 恢复配额失败，环境可能停留在 1 MiB：%v", err)
		}
	})

	// 声明一个明显超过 1 MiB 的大小 → 签发阶段就该被拒
	const tooBig = 8 << 20
	path := "live-perm/quota-too-big.bin"
	err = client.Upload(ctx, token, drive.SpacePersonal, path,
		strings.NewReader("x"), tooBig)
	if err == nil {
		t.Error("超过配额的写入**被放行了**——配额闸门没生效，" +
			"用户可以把服务器磁盘写满")
		_ = client.Delete(ctx, token, drive.SpacePersonal, path)
		return
	}
	msg := err.Error()
	t.Logf("✅ 超额写入被拒：%s", msg)

	// 错误信息必须让人知道**该怎么办**，不能只丢一个「失败」
	if !strings.Contains(msg, "空间已满") && !strings.Contains(msg, "配额") {
		t.Errorf("配额错误的说明不可读（用户不知道该怎么办）：%s", msg)
	}
}
