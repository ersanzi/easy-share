package drive_test

// 活栈回归：Multipart（≥32 MiB 走的那条路）也必须受 CAS 约束。
//
// 这条来自一次**实弹发现**：Multipart 的 create/complete 原本完全不看
// expectedVersion —— 更糟的是 PutBo 里本来就有这个字段（注释还写着「传了就做校验」），
// 于是调用方送一个过期版本会拿到 HTTP 200，**以为校验过了**。
// 实测确认：已存在 versionId=1 的文件，送 expectedVersion=1000 建会话 → 200。
//
// 本用例走**完整生产链路**（真分片、真 Complete、真对象存储），不用「查库断言」糊过去：
// 造一个 ≥32 MiB 的文件 → 上传（v1）→ 用过期的版本再传一次 → 必须被拒，
// 且**对象存储里的内容仍是 v1**（这才是「没被覆盖」的证明，而不是「报了错」）。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easyshare/internal/drive"
)

// TestLiveMultipartRejectsStaleVersion 验证分片上传路径的 CAS。
//
// 为什么值得付出 33 MiB 的代价：Multipart 与单请求是**两条独立实现**，
// 单请求有 CAS 不代表这条有。这正是「接口能工作」与「生产路径在用它」的差别——
// 本仓在这个差别上已经栽过一次（见 live_cas_test.go 的自我更正说明）。
func TestLiveMultipartRejectsStaleVersion(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	// 必须接上会话存储，否则 UploadFile 不会走分片路径
	client.Sessions = drive.NewSessionStore(t.TempDir())
	ctx := context.Background()
	path := "live-cas/multipart-stale.bin"

	_ = client.Delete(ctx, token, drive.SpacePersonal, path)
	_ = purgeRecycledFor(t, client, ctx, token, path)
	cleanupPath(t, client, ctx, token, path) // 失败也要清（见 cleanupPath 注释）

	// 分片阈值是 32 MiB，取 33 MiB 确保走 Multipart
	const size = 33 << 20
	localPath := filepath.Join(t.TempDir(), "big.bin")
	if err := writeSizedFile(localPath, size, 'A'); err != nil {
		t.Fatalf("造测试文件失败: %v", err)
	}

	// ① 首次上传：expectedVersion=0（我认为不存在）——必须成功
	zero := int64(0)
	if err := client.UploadFileIfVersion(ctx, token, drive.SpacePersonal, path, localPath, &zero, nil); err != nil {
		t.Fatalf("首次分片上传应成功，实得: %v", err)
	}
	current := currentVersionOf(t, ctx, client, token, path)
	if current < 1 {
		t.Fatalf("首次上传后应回填 versionId，实得 %d", current)
	}
	t.Logf("首次分片上传完成，versionId=%d", current)

	// ⑧ 内容换成 'B' 再传（保持同样大小以复用同一指纹路径无关紧要，指纹含 mtime）
	if err := writeSizedFile(localPath, size, 'B'); err != nil {
		t.Fatal(err)
	}
	// ② 用**过期版本**再传 → 必须被拒
	stale := current + 999
	err := client.UploadFileIfVersion(ctx, token, drive.SpacePersonal, path, localPath, &stale, nil)
	if err == nil {
		t.Fatal("用过期的 expectedVersion 走分片上传竟然成功——" +
			"说明 Multipart 路径绕过了 CAS，会静默覆盖别人的修改")
	}
	if !strings.Contains(err.Error(), "已被其他人修改") &&
		!strings.Contains(err.Error(), "已被删除") &&
		!strings.Contains(err.Error(), "已过期") {
		t.Errorf("错误信息应说明版本冲突，实得: %v", err)
	}
	t.Logf("✅ 分片路径拒绝了过期版本：%v", err)

	// ③ 关键断言：内容**没被覆盖**（“报了错”不等于“没写”）
	rc, _, err := client.Open(ctx, token, drive.SpacePersonal, path)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	defer rc.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(rc, head)
	if n == 0 {
		t.Fatal("读回内容为空——对象可能已被覆盖成 0 字节")
	}
	if head[0] != 'A' {
		t.Fatalf("内容首字节是 %q，期望 'A'——原文已被对方的写入覆盖（这正是 CAS 要防的数据丢失）", head[0])
	}
	t.Logf("✅ 内容完好（首字节 %q），未被覆盖", head[0])

}

// writeSizedFile 写一个指定大小、全部填充 fill 字节的文件。
func writeSizedFile(path string, size int, fill byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	chunk := bytes.Repeat([]byte{fill}, 1<<20)
	written := 0
	for written < size {
		n := size - written
		if n > len(chunk) {
			n = len(chunk)
		}
		if _, err := f.Write(chunk[:n]); err != nil {
			return err
		}
		written += n
	}
	return f.Sync()
}

// currentVersionOf 从列表读当前版本号。
func currentVersionOf(t *testing.T, ctx context.Context, client *drive.Client, token, path string) int64 {
	t.Helper()
	objects, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("列举失败: %v", err)
	}
	for _, o := range objects {
		if o.Path == path {
			return o.VersionId.Int64()
		}
	}
	return 0
}

var _ = fmt.Sprintf
