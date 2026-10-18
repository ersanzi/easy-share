package drive_test

// 新建文件夹的活栈验证：Mkdir → 带目录列举能看到 → 默认列举不传染 →
// 重复创建幂等 → 非法路径被拒 → 空目录可删且不进回收站。
//
// 默认跳过，需要活栈时显式开启（与 live_integration_test.go 同环境）：
//
//	EASYSHARE_LIVE_DRIVE=1 EASYSHARE_LIVE_BASE=http://127.0.0.1:8090 \
//	EASYSHARE_LIVE_USER_A=admin EASYSHARE_LIVE_PASS_A=admin123 \
//	go test ./internal/drive/ -run LiveMkdir -v

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"easyshare/internal/drive"
)

// TestLiveDriveMkdirAndDirVisibility 覆盖目录功能的端到端语义。
func TestLiveDriveMkdirAndDirVisibility(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()

	relDir := fmt.Sprintf("live-dir-%d", time.Now().UnixNano())
	// 目录标记对象没有 es_file 行，删除即直接删对象；失败时也要清（t.Cleanup 的理由
	// 见 live_cas_test.go 的 cleanupPath 注释）
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := client.Delete(cleanCtx, token, drive.SpacePersonal, relDir+"/"); err != nil {
			t.Logf("清理目录 %s 失败（跳过）：%v", relDir, err)
		}
	})

	// ① 新建：返回路径带尾斜杠（与列举出口的目录对象同形）
	created, err := client.Mkdir(ctx, token, drive.SpacePersonal, relDir)
	if err != nil {
		t.Fatalf("Mkdir 失败：%v", err)
	}
	if created != relDir+"/" {
		t.Errorf("返回路径应带尾斜杠：%q want %q", created, relDir+"/")
	}

	// ② 重复创建幂等：与本地文件系统行为对齐（用户为点第二次先手动删一次不可接受）
	if _, err := client.Mkdir(ctx, token, drive.SpacePersonal, relDir); err != nil {
		t.Errorf("重复 Mkdir 应幂等成功，实得错误：%v", err)
	}

	// ③ 带目录列举能看到（空文件夹必须可见，否则建了等于没建）。
	// 注意不能用 findObject：它走默认列举（不含目录），查目录必然 nil。
	withDirs, err := client.ObjectsWithDirs(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("ObjectsWithDirs 失败：%v", err)
	}
	var dirPaths []string
	foundDir := false
	for _, o := range withDirs {
		dirPaths = append(dirPaths, o.Path)
		if o.Path == relDir+"/" {
			foundDir = true
		}
	}
	if !foundDir {
		t.Fatalf("带目录列举里没有 %s（现有：%v）", relDir+"/", dirPaths)
	}

	// ④ 默认列举看不到：挂载盘 / WebDAV / 快搜的目录语义不受影响
	plain, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("Objects 失败：%v", err)
	}
	for _, o := range plain {
		if o.Path == relDir+"/" {
			t.Errorf("默认列举泄漏了目录对象 %s——其他出口会被 0 字节文件污染", o.Path)
		}
	}

	// ⑤ 非法路径被拒（逃逸与重复斜杠）
	for _, bad := range []string{"../escape", "x/./y", "a/../b"} {
		if _, err := client.Mkdir(ctx, token, drive.SpacePersonal, bad); err == nil {
			t.Errorf("非法路径 %q 应被拒", bad)
		}
	}

	// ⑥ 深层目录一次建成（photos/2026/子目录）
	deep := relDir + "/mid/leaf"
	deepCreated, err := client.Mkdir(ctx, token, drive.SpacePersonal, deep)
	if err != nil {
		t.Fatalf("深层 Mkdir 失败：%v", err)
	}
	if deepCreated != deep+"/" {
		t.Errorf("深层返回路径错误：%q", deepCreated)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// 先删叶子再删中间层：目录标记各自独立，父层删掉不影响子层键
		_ = client.Delete(cleanCtx, token, drive.SpacePersonal, deep+"/")
		_ = client.Delete(cleanCtx, token, drive.SpacePersonal, relDir+"/mid/")
	})

	// ⑦ 删除空目录：直接删对象，不进回收站（目录无 es_file 行）。
	// 删除后要用带目录列举查——默认列举本来就看不到目录，拿它断言等于永远通过。
	if err := client.Delete(ctx, token, drive.SpacePersonal, relDir+"/"); err != nil {
		t.Fatalf("删除目录失败：%v", err)
	}
	afterDelete, err := client.ObjectsWithDirs(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("删除后 ObjectsWithDirs 失败：%v", err)
	}
	for _, o := range afterDelete {
		if o.Path == relDir+"/" {
			t.Error("删除后目录仍出现在带目录列举里")
		}
	}
	entries, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("读回收站失败：%v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Path, "/") || strings.HasSuffix(e.OriginPath, "/") {
			t.Errorf("回收站出现目录行：%+v", e)
		}
	}
}
