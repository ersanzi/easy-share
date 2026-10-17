package drive_test

// 活栈回归：回收站三件套（软删 → 列举 → 还原 → 软删 → 彻底删除）走完整生产链路。
//
// 为什么补这条：KI-21 修完「fileId 跨 JS 边界被截断」之后，`RecycleRestore` /
// `RecyclePurge` 这两个**恰好是用户点按钮时真正执行的**动作，却只有
// 「ID 往返无损」被验证过，「拿这个 ID 真去还原/彻底删除能不能成功」没验。
// 这正是本仓反复出现的形状：**换了一个零件，却只测零件、没测装配后的机器**。
//
// 这条用例同时覆盖：
//   - 软删后 active 行消失、回收站出现该行（KI-10 的读侧过滤）
//   - 还原后 fileId **不变**（KI-12 的身份稳定性）
//   - 彻底删除后行与对象都消失（KI-7 的引用计数判据不被误伤）

import (
	"context"
	"strings"
	"testing"

	"easyshare/internal/drive"
)

func TestLiveRecycleRestoreAndPurgeRoundTrip(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()

	const path = "live-cas/recycle-roundtrip.txt"
	const content = "recycle-roundtrip-content"

	// 前置清理：本用例可反复运行，不依赖上一次的结果
	_ = client.Delete(ctx, token, drive.SpacePersonal, path)
	_ = purgeRecycledFor(t, client, ctx, token, path)
	cleanupPath(t, client, ctx, token, path) // 失败也要清

	// ① 造一个文件
	if err := client.Upload(ctx, token, drive.SpacePersonal, path, strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	before := findObject(t, ctx, client, token, path)
	if before == nil {
		t.Fatalf("上传后列表里应有 %s", path)
	}
	originalFileID := before.FileId.Int64()
	if originalFileID <= 0 {
		t.Fatalf("上传后应有 fileId，实得 %d", originalFileID)
	}
	t.Logf("① 已上传，fileId=%d", originalFileID)

	// ② 软删 → 列表里消失、回收站里出现
	if err := client.Delete(ctx, token, drive.SpacePersonal, path); err != nil {
		t.Fatalf("软删失败: %v", err)
	}
	if findObject(t, ctx, client, token, path) != nil {
		t.Fatal("软删后列表里仍然可见——读侧没按 state 过滤（KI-10）")
	}
	entry := findRecycle(t, ctx, client, token, path)
	if entry == nil {
		t.Fatalf("软删后回收站里应有 %s", path)
	}
	if entry.FileId.Int64() != originalFileID {
		t.Errorf("回收站条目的 fileId 与上传时不一致：%d vs %d", entry.FileId.Int64(), originalFileID)
	}
	t.Logf("② 已软删，回收站持 fileId=%d", entry.FileId.Int64())

	// ③ 还原（走与前端相同的字符串入口）→ 列表里回来，且 fileId 不变
	if err := client.RecycleRestore(ctx, token, drive.SpacePersonal, entry.FileId.Int64()); err != nil {
		t.Fatalf("还原失败: %v", err)
	}
	after := findObject(t, ctx, client, token, path)
	if after == nil {
		t.Fatal("还原后列表里应重新出现该文件")
	}
	if after.FileId.Int64() != originalFileID {
		t.Errorf("还原后 fileId 变了：%d -> %d（分享/索引引用会全部失效，KI-12）",
			originalFileID, after.FileId.Int64())
	}
	if got := readObject(t, ctx, client, token, path); got != content {
		t.Errorf("还原后内容应完好，实得 %q", got)
	}
	t.Logf("③ 已还原，fileId 仍为 %d（身份稳定）", after.FileId.Int64())

	// ④ 再软删 → 彻底删除 → 回收站里不再有它
	if err := client.Delete(ctx, token, drive.SpacePersonal, path); err != nil {
		t.Fatalf("二次软删失败: %v", err)
	}
	entry2 := findRecycle(t, ctx, client, token, path)
	if entry2 == nil {
		t.Fatal("二次软删后回收站里应有该行")
	}
	if err := client.RecyclePurge(ctx, token, drive.SpacePersonal, entry2.FileId.Int64()); err != nil {
		t.Fatalf("彻底删除失败: %v", err)
	}
	if findRecycle(t, ctx, client, token, path) != nil {
		t.Error("彻底删除后回收站里仍有该行")
	}
	if findObject(t, ctx, client, token, path) != nil {
		t.Error("彻底删除后列表里仍有该文件")
	}
	t.Log("④ 已彻底删除，列表与回收站都不再有它")

}

// findObject 在列举里找指定路径，找不到返回 nil。
func findObject(t *testing.T, ctx context.Context, client *drive.Client, token, path string) *drive.Object {
	t.Helper()
	objects, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("列举失败: %v", err)
	}
	for i := range objects {
		if objects[i].Path == path {
			return &objects[i]
		}
	}
	return nil
}

// findRecycle 在回收站里找指定路径（含删除前的 originPath），找不到返回 nil。
func findRecycle(t *testing.T, ctx context.Context, client *drive.Client, token, path string) *drive.RecycleEntry {
	t.Helper()
	entries, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("读回收站失败: %v", err)
	}
	for i := range entries {
		if entries[i].Path == path || entries[i].OriginPath == path {
			return &entries[i]
		}
	}
	return nil
}
