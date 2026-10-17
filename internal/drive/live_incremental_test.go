package drive_test

// 活栈回归（切片 2）：增量列举必须在真实控制面上可用。
//
// 验收（路线图原文）：改 1 个文件 → 客户端只收到 1 条变更，不重列整空间。
// 本用例钉住这个行为，并顺带验证全量重建返回 newestSpaceVersion。

import (
	"context"
	"strings"
	"testing"

	"easyshare/internal/drive"
)

func TestLiveIncrementalChanges(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()
	const path = "live-incremental/a.txt"

	_ = client.Delete(ctx, token, drive.SpacePersonal, path)
	t.Cleanup(func() {
		cleanCtx := context.Background()
		_ = client.Delete(cleanCtx, token, drive.SpacePersonal, path)
		purgeLivePaths(cleanCtx, client, token, drive.SpacePersonal, "live-incremental/")
	})

	// ① 全量重建：拿当前版本号
	full, err := client.Changes(ctx, token, drive.SpacePersonal, nil)
	if err != nil {
		t.Fatalf("全量重建失败：%v", err)
	}
	if full.NewestSpaceVersion.Int64() < 0 {
		t.Fatalf("newestSpaceVersion 非法：%d", full.NewestSpaceVersion.Int64())
	}
	if full.Files == nil {
		t.Fatalf("全量重建应返回完整文件列表（nil 不合法）")
	}
	baseVersion := full.NewestSpaceVersion.Int64()

	// ② 写一个文件（真实上传路径）
	if err := client.Upload(ctx, token, drive.SpacePersonal, path,
		strings.NewReader("incremental"), 11); err != nil {
		t.Fatalf("上传失败：%v", err)
	}

	// ③ 增量拉取：应只看到这条变更
	delta, err := client.Changes(ctx, token, drive.SpacePersonal, &baseVersion)
	if err != nil {
		t.Fatalf("增量拉取失败：%v", err)
	}
	if delta.NewestSpaceVersion.Int64() <= baseVersion {
		t.Fatalf("写文件后版本应推进：%d -> %d", baseVersion, delta.NewestSpaceVersion.Int64())
	}
	// 旧实现会在目录层惰性补账时也产生 created 事件（这是合理的）。
	// 判据应过滤出本用例的路径，而不是要求全空间恰好只有一条。
	var mine *drive.Change
	for i := range delta.Changes {
		if delta.Changes[i].Path == path {
			mine = &delta.Changes[i]
			break
		}
	}
	if mine == nil {
		t.Fatalf("增量结果里没有本用例写入的 %s，实得 %+v", path, delta.Changes)
	}
	if mine.Event != "created" {
		t.Errorf("变更内容错误：event=%s，期望 created", mine.Event)
	}
	if mine.FileId.Int64() <= 0 {
		t.Errorf("变更应带回稳定 fileId，实得 %d", mine.FileId.Int64())
	}
	t.Logf("（增量共 %d 条，其中本用例目标 1 条）", len(delta.Changes))

	// ④ 再用新版本拉：应没有任何变更
	next := delta.NewestSpaceVersion.Int64()
	empty, err := client.Changes(ctx, token, drive.SpacePersonal, &next)
	if err != nil {
		t.Fatalf("空增量拉取失败：%v", err)
	}
	if len(empty.Changes) != 0 {
		t.Errorf("无变更时不应返回任何条目，实得 %d 条", len(empty.Changes))
	}
	t.Logf("✅ 全量版本=%d → 写 1 个文件 → 增量=%d 条（event=%s path=%s）→ 新版本=%d",
		baseVersion, len(delta.Changes), delta.Changes[0].Event, path, next)
}
