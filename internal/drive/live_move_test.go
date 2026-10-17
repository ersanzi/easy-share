package drive_test

// 活栈回归：服务端 move（KI-12 的修复）走真实链路。
//
// 为什么补这条：`Move` 是 KI-12 的产物——它把「下载+上传+删除」的模拟改名
// 换成了服务端 copy+改行+删源。那条修复的两个卖点都**只在单测里验过**：
//   ① fileId 不变（分享/索引引用不断）
//   ② 空间占用不变（不再留回收站副本）
// 而这两条恰恰都依赖**真实控制面 + 真实对象存储**的行为：
// 单测里 mapper 是 mock，CopyObject 也是 mock——「同名副本」与「新 fileId」
// 这类副作用只有真跑才会露出来（这正是 KI-12 当初的成因）。
//
// 用真实字节数核 ②：改名前后读同名对象的 size，并检查回收站没有多出副本。

import (
	"context"
	"strings"
	"testing"

	"easyshare/internal/drive"
)

func TestLiveMoveKeepsFileIdAndLeavesNoRecycleCopy(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()

	const from = "live-cas/move-src.txt"
	const to = "live-cas/move-dst.txt"
	const content = "move-must-preserve-identity"

	// 前置清理 + 注册**失败也会执行**的收尾（写在末尾的清理遇到 t.Fatalf 不会跑）
	for _, p := range []string{from, to} {
		_ = client.Delete(ctx, token, drive.SpacePersonal, p)
		_ = purgeRecycledFor(t, client, ctx, token, p)
		cleanupPath(t, client, ctx, token, p)
	}

	// ① 造文件，记下 fileId 与大小
	if err := client.Upload(ctx, token, drive.SpacePersonal, from, strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	before := findObject(t, ctx, client, token, from)
	if before == nil {
		t.Fatalf("上传后应能在列表里找到 %s", from)
	}
	originalID := before.FileId.Int64()
	t.Logf("① 已上传 %s，fileId=%d，size=%d", from, originalID, before.Size)

	// ② 服务端 move
	if err := client.Move(ctx, token, drive.SpacePersonal, from, to); err != nil {
		t.Fatalf("move 失败: %v", err)
	}

	// ③ 源路径应消失
	if findObject(t, ctx, client, token, from) != nil {
		t.Errorf("move 后源路径 %s 仍在列表里", from)
	}

	// ④ 目标路径出现，且 **fileId 不变**（KI-12 的核心判据）
	after := findObject(t, ctx, client, token, to)
	if after == nil {
		t.Fatalf("move 后目标路径 %s 不在列表里", to)
	}
	if after.FileId.Int64() != originalID {
		t.Errorf("move 换掉了 fileId：%d -> %d——分享链接/任务引用/知识索引会全部失效（KI-12）",
			originalID, after.FileId.Int64())
	}
	if after.Size != int64(len(content)) {
		t.Errorf("move 后大小变了：%d -> %d", before.Size, after.Size)
	}

	// ⑤ 内容完好（copy 没有损坏字节）
	if got := readObject(t, ctx, client, token, to); got != content {
		t.Errorf("move 后内容应完好，实得 %q", got)
	}
	t.Logf("✅ 已 move 到 %s，fileId 仍为 %d（身份稳定）", to, after.FileId.Int64())

	// ⑥ 关键：**回收站里不该多出副本**（旧「上传+删源」会留一份，空间 ×2）
	entries, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("读回收站失败: %v", err)
	}
	for _, e := range entries {
		if e.Path == from || e.OriginPath == from || e.Path == to || e.OriginPath == to {
			t.Errorf("move 在回收站里留下了副本：path=%s originPath=%s（旧模拟改名会空间×2）",
				e.Path, e.OriginPath)
		}
	}
	t.Log("✅ 回收站里没有副本（空间未翻倍）")

}
