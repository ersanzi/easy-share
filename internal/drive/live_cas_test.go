package drive_test

// 活栈用例：验证 CAS 端到端可用，并**记录它尚未接到上传路径上**这个事实。
//
// 背景（一次自我更正）：上一轮我给控制面加了 expectedVersion（乐观并发校验），
// 也加了 drive.Client.PresignPutIfVersion，并在文档里写成「静默覆盖被消灭」。
// 本轮按**真实上传路径**实测后发现那句话**说过头了**：
//
//	client.Upload(...) → PresignPut(..., nil) → 不带版本 → 服务端不校验
//
// 即 Upload / UploadFile / 挂载盘 / 客户端上传**全部不经过 CAS**，
// 两个 Upload 先后写同一路径仍然静默覆盖（本文件第二条用例实测复现）。
//
// 为什么当时没发现：我只测了**带 expectedVersion 的 HTTP 调用**（curl 级），
// 没有从 Go 客户端走一遍**生产调用链**。「接口能工作」与「生产路径在用它」是两件事。
//
// 本文件因此分两条：
//   ① CAS 本身可用（带版本时确实拒绝）—— 证明能力是真的；
//   ② 不带版本时仍会覆盖 —— 把「尚未接线」变成可复现的**事实记录**，
//      而不是靠读代码推断。
//
// 接线时机：路线图切片 5b（双向同步）—— 到那时客户端才**持有**「我看到的版本」，
// 才有东西可以断言。当前 UI 上传语义是「把这个文件放到这个路径」，
// 调用方没有版本可传，强行要求只会绑死升级。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"easyshare/internal/drive"
)

// sharedTransferClient 与 *drive.Client 的上传超时口径一致（大文件慢，单独放宽）。
func sharedTransferClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute}
}

// newPutRequest 组装直传 PUT。刻意不设 Content-Type（预签名未签该头，设了会破签名）。
func newPutRequest(ctx context.Context, url string, payload []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.ContentLength = int64(len(payload))
	return req, nil
}

// readObject 读回对象全文（小文件）。
func readObject(t *testing.T, ctx context.Context, client *drive.Client, token, path string) string {
	t.Helper()
	rc, _, err := client.Open(ctx, token, drive.SpacePersonal, path)
	if err != nil {
		t.Fatalf("读回 %s 失败: %v", path, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("读取 %s 内容失败: %v", path, err)
	}
	return string(data)
}

// TestLiveCASRejectsStaleVersion 验证 CAS 端到端可用（带版本时会拒绝）。
//
// 这条走的是**真实控制面 + 真实对象存储**，且用的是 Go 客户端的能力，
// 而不是裸 curl —— 它证明「能力是真的」，与下面那条「能力没被生产路径使用」并列。
func TestLiveCASRejectsStaleVersion(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()
	path := "live-cas/stale.txt"
	payload := []byte("first-version")

	// ⓪ 先清理同名残留：本用例可能在**上一次失败后重跑**，
	// 而上次留下的行会让「首次登记」不再是首次（实测踩过：报「当前版本 1」）。
	// 活栈用例必须自清理，否则第二次运行的行为依赖第一次的结果。
	_ = client.Delete(ctx, token, drive.SpacePersonal, path)
	if rows := purgeRecycledFor(t, client, ctx, token, path); rows > 0 {
		t.Logf("清理了 %d 条回收站残留", rows)
	}
	cleanupPath(t, client, ctx, token, path) // 失败也要清

	// ① 首次登记：expectedVersion=0（我认为文件还不存在）
	zero := int64(0)
	url, err := client.PresignPutIfVersion(ctx, token, drive.SpacePersonal, path,
		int64(len(payload)), &zero)
	if err != nil {
		t.Fatalf("首次登记应成功，实得: %v", err)
	}
	if err := putRawBytes(ctx, url, payload); err != nil {
		t.Fatalf("首次上传失败: %v", err)
	}

	// ② 再用**同一个过期版本**（0）去写 → 必须被拒
	if _, err := client.PresignPutIfVersion(ctx, token, drive.SpacePersonal, path,
		int64(len(payload)), &zero); err == nil {
		t.Fatal("用过期版本重复首次写入竟然成功——CAS 未生效")
	} else if !strings.Contains(err.Error(), "已被其他人修改") {
		t.Errorf("错误信息应说明版本冲突，实得: %v", err)
	}

	// ③ 取列表拿到当前版本，用它重试 → 应成功
	objects, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("列举失败: %v", err)
	}
	var current int64 = -1
	for _, o := range objects {
		if o.Path == path {
			current = o.VersionId.Int64()
		}
	}
	if current < 1 {
		t.Fatalf("列表未回填 versionId（实得 %d）——客户端无从知道自己看到的是哪一版", current)
	}
	url2, err := client.PresignPutIfVersion(ctx, token, drive.SpacePersonal, path,
		int64(len(payload)), &current)
	if err != nil {
		t.Fatalf("带当前版本重试应成功，实得: %v", err)
	}
	if err := putRawBytes(ctx, url2, []byte("second-version")); err != nil {
		t.Fatalf("重试上传失败: %v", err)
	}
	if got := readObject(t, ctx, client, token, path); got != "second-version" {
		t.Errorf("内容应为 second-version，实得 %q", got)
	}

}

// TestLiveUploadPathDoesNotUseCAS 记录事实：**上传路径尚未经过 CAS**。
//
// 这条断言的是「当前行为」而不是「理想行为」——它的价值在于：
//   - 把「CAS 存在但生产路径没用它」从读代码推断变成**可复现证据**；
//   - 谁将来把 Upload 接上 CAS，这条会失败，从而**被迫更新此处说明**，
//     不至于悄悄改变语义却没人知道。
func TestLiveUploadPathDoesNotUseCAS(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()
	path := "live-cas/upload-path.txt"
	cleanupPath(t, client, ctx, token, path) // 失败也要清

	if err := client.Upload(ctx, token, drive.SpacePersonal, path,
		strings.NewReader("AAAA"), 4); err != nil {
		t.Fatalf("A 上传失败: %v", err)
	}
	// 第二次上传：真实上传路径（Upload）不带 expectedVersion
	errB := client.Upload(ctx, token, drive.SpacePersonal, path,
		strings.NewReader("BBBB"), 4)
	final := readObject(t, ctx, client, token, path)

	t.Logf("真实上传路径：A 成功；B err=%v；最终内容=%q", errB, final)

	if errB != nil {
		t.Errorf("Upload 竟然带了 CAS（B 被拒：%v）——"+
			"说明上传路径已接线，请更新本用例与相关文档说明", errB)
	}
	if final != "BBBB" {
		t.Errorf("预期 B 覆盖 A（当前语义），实得 %q", final)
	}

}

// putRawBytes 把字节直传到预签名 URL（与 Upload 相同的传输超时口径）。
func putRawBytes(ctx context.Context, url string, payload []byte) error {
	req, err := newPutRequest(ctx, url, payload)
	if err != nil {
		return err
	}
	resp, err := sharedTransferClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("PUT 返回 %s", resp.Status)
	}
	return nil
}

// cleanupPath 注册一个**无论测试成败都会执行**的清理。
//
// 为什么必须用 t.Cleanup 而不是「写在函数末尾」：活栈用例失败时多以
// `t.Fatalf` 退出，而 Fatalf 会立即终止当前 goroutine——末尾的清理代码
// **根本不会执行**。本仓因此累积过 30 个孤儿对象 + 32 行 es_file 残留
// （2026-10-16 实测），而每次失败都会再叠一层。清理写在末尾等于
// 「只在一切正常时才清理」，而恰恰是出问题时最需要它。
func cleanupPath(t *testing.T, client *drive.Client, ctx context.Context, token, path string) {
	t.Helper()
	t.Cleanup(func() {
		// 用独立的 context：测试的 ctx 可能已被取消（超时用例）
		cleanCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := client.Delete(cleanCtx, token, drive.SpacePersonal, path); err != nil {
			t.Logf("清理 %s 失败（跳过）：%v", path, err)
		}
		entries, err := client.RecycleList(cleanCtx, token, drive.SpacePersonal)
		if err != nil {
			t.Logf("读回收站失败（跳过清理）：%v", err)
			return
		}
		for _, e := range entries {
			if e.Path != path && e.OriginPath != path {
				continue
			}
			if err := client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, e.FileId.Int64()); err != nil {
				t.Logf("彻底删除 %s 残留失败（跳过）：%v", path, err)
			}
		}
	})
}

// purgeRecycledFor 把某路径在回收站里的行彻底删掉（活栈用例自清理用）。
//
// 为什么需要：软删只置 state='recycled'，**行仍在库里**，而 CAS 的
// 「首次登记」判定看的是 active 行——所以残留的 recycled 行本身不影响 CAS，
// 但会让「重新上传同名」这条路径留下历史；测试要能反复跑，就得清干净。
func purgeRecycledFor(t *testing.T, client *drive.Client, ctx context.Context,
	token, path string) int {
	t.Helper()
	entries, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Logf("读回收站失败（跳过清理）：%v", err)
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Path != path && e.OriginPath != path {
			continue
		}
		if err := client.RecyclePurge(ctx, token, drive.SpacePersonal, e.FileId.Int64()); err != nil {
			t.Logf("彻底删除残留失败（跳过）：%v", err)
			continue
		}
		n++
	}
	return n
}
