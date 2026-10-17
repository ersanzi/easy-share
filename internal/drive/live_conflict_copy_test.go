package drive_test

// 活栈回归（切片 3 / 生产路径 CAS）：同名再次上传不得静默覆盖。
//
// 修复前：UploadFile 不带版本 → 两个客户端先后写同一路径 = 后者覆盖前者。
// 修复后：客户端先看版本、上传带回版本；服务端发现别人先改过就拒绝，
// 再自动落到“冲突副本”。本用例钉住**双方内容都能取回**这一最终用户可感知的行为。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easyshare/internal/drive"
)

func TestLiveSamePathUploadKeepsBothVersions(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()
	base := "live-cas/conflict-" + filepath.Base(t.TempDir()) + ".txt"
	first := base
	second := conflictName(base)

	_ = client.Delete(ctx, token, drive.SpacePersonal, first)
	_ = client.Delete(ctx, token, drive.SpacePersonal, second)
	t.Cleanup(func() {
		cleanCtx := context.Background()
		for _, p := range []string{first, second} {
			_ = client.Delete(cleanCtx, token, drive.SpacePersonal, p)
		}
		purgeLivePaths(cleanCtx, client, token, drive.SpacePersonal, "live-cas/conflict-")
	})

	a := t.TempDir() + "/a.txt"
	b := t.TempDir() + "/b.txt"
	if err := os.WriteFile(a, []byte("first-version"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("second-version"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A：真实上传路径
	if err := client.UploadFile(ctx, token, drive.SpacePersonal, first, a, nil); err != nil {
		t.Fatalf("A 上传失败：%v", err)
	}

	// B：重新创建一个“只看到旧版本”的客户端（模拟另一个设备/窗口）
	objects, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal(err)
	}
	var seen int64 = -1
	for _, o := range objects {
		if o.Path == first {
			seen = o.VersionId.Int64()
		}
	}
	if seen < 0 {
		t.Fatal("A 上传后列表应有该文件")
	}

	bClient := drive.New(baseURL)
	// B 看到的是**过期版本**（模拟 B 在 A 修改前拿到的列表）。
	// 若用“当前版本”则会被服务端接受——那是正常覆盖，不是冲突。
	stale := seen - 1
	if stale < 0 {
		stale = 0
	}
	err = bClient.UploadFileIfVersion(ctx, token, drive.SpacePersonal, first, b, &stale, nil)
	if err == nil {
		t.Fatal("B 用相同旧版本上传竟然成功——CAS 未生效，会静默覆盖")
	}
	if !isDriveVersionConflict(err) {
		t.Fatalf("应报版本冲突，实得：%v", err)
	}

	// 自动冲突副本（生产路径的兜底规则）：以 0 新建，不覆盖任何人
	if err := bClient.UploadFileIfVersion(ctx, token, drive.SpacePersonal, second, b, int64Ptr(0), nil); err != nil {
		t.Fatalf("冲突副本上传失败：%v", err)
	}

	gotFirst := readObject(t, ctx, client, token, first)
	gotSecond := readObject(t, ctx, client, token, second)
	if gotFirst != "first-version" || gotSecond != "second-version" {
		t.Errorf("双方内容必须都可取回：%q / %q", gotFirst, gotSecond)
	}
	t.Logf("✅ 同名冲突保留双方：%s=%q，%s=%q", first, gotFirst, second, gotSecond)
}

func conflictName(key string) string {
	ext := filepath.Ext(key)
	return strings.TrimSuffix(key, ext) + " (冲突副本)" + ext
}

func int64Ptr(v int64) *int64 { return &v }

func isDriveVersionConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "已被其他人修改") ||
		strings.Contains(msg, "已被删除") ||
		strings.Contains(msg, "版本") && strings.Contains(msg, "已过期")
}
