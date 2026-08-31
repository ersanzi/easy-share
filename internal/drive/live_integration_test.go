package drive_test

// 对活控制面的集成测试：验证 internal/drive 与真实 RuoYi + RustFS 的契合度，
// 补上单测里 httptest 假桩覆盖不到的部分（真实 JWT、真实预签名、真实命名空间隔离）。
//
// 默认跳过，需要活栈时显式开启：
//
//	EASYSHARE_LIVE_DRIVE=1 EASYSHARE_LIVE_BASE=http://127.0.0.1:8090 \
//	EASYSHARE_LIVE_USER_A=admin EASYSHARE_LIVE_PASS_A=admin123 \
//	EASYSHARE_LIVE_USER_B=test  EASYSHARE_LIVE_PASS_B=test123 \
//	go test ./internal/drive/ -run Live -v

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/drive"
)

// liveEnv 读取活栈配置；未开启时跳过测试。
func liveEnv(t *testing.T) (baseURL string) {
	t.Helper()
	if os.Getenv("EASYSHARE_LIVE_DRIVE") == "" {
		t.Skip("未设 EASYSHARE_LIVE_DRIVE，跳过活栈集成测试")
	}
	baseURL = os.Getenv("EASYSHARE_LIVE_BASE")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8090"
	}
	return baseURL
}

// login 用账号客户端取真实 JWT。
func login(t *testing.T, baseURL, userEnv, passEnv string) string {
	t.Helper()
	user, pass := os.Getenv(userEnv), os.Getenv(passEnv)
	if user == "" || pass == "" {
		t.Skipf("缺少 %s / %s", userEnv, passEnv)
	}
	session, err := account.New(baseURL).Login(context.Background(), user, pass)
	if err != nil {
		t.Fatalf("%s 登录失败：%v", user, err)
	}
	if session.Token == "" {
		t.Fatalf("%s 登录未返回 token", user)
	}
	return session.Token
}

// TestLiveDriveRoundTripAndIsolation 走一遍真实的上传→列举→读取→隔离→删除。
func TestLiveDriveRoundTripAndIsolation(t *testing.T) {
	baseURL := liveEnv(t)
	tokenA := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()

	relPath := fmt.Sprintf("live-%d.txt", time.Now().UnixNano())
	content := "live-integration-" + relPath
	local := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
		t.Fatalf("写本地文件失败：%v", err)
	}

	// 上传时必须有进度回调，且末次回调等于文件大小。
	var lastSent, lastTotal int64
	if err := client.UploadFile(ctx, tokenA, drive.SpacePersonal, relPath, local, func(sent, total int64) {
		lastSent, lastTotal = sent, total
	}); err != nil {
		t.Fatalf("上传到活栈失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Delete(ctx, tokenA, drive.SpacePersonal, relPath) })
	if lastSent != int64(len(content)) || lastTotal != int64(len(content)) {
		t.Errorf("进度末值错误：sent=%d total=%d want=%d", lastSent, lastTotal, len(content))
	}

	// 列举里应出现该相对路径，且不得泄漏 users/ 前缀。
	objects, err := client.Objects(ctx, tokenA, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	var found *drive.Object
	for i := range objects {
		if strings.HasPrefix(objects[i].Path, "users/") {
			t.Errorf("列举结果泄漏了用户命名空间前缀：%s", objects[i].Path)
		}
		if objects[i].Path == relPath {
			found = &objects[i]
		}
	}
	if found == nil {
		t.Fatalf("列举结果里没有刚上传的 %s", relPath)
	}
	if found.Size != int64(len(content)) {
		t.Errorf("大小错误：%d want %d", found.Size, len(content))
	}
	if found.LastModified.IsZero() {
		t.Error("lastModified 未返回")
	}

	// 直取内容应与上传一致。
	body, _, err := client.Open(ctx, tokenA, drive.SpacePersonal, relPath)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	raw, readErr := io.ReadAll(body)
	body.Close()
	if readErr != nil {
		t.Fatalf("读取内容失败：%v", readErr)
	}
	if string(raw) != content {
		t.Errorf("内容不一致：%q want %q", raw, content)
	}

	// B 不得看到 A 的对象，且按同名相对路径取不到 A 的内容。
	tokenB := login(t, baseURL, "EASYSHARE_LIVE_USER_B", "EASYSHARE_LIVE_PASS_B")
	objectsB, err := client.Objects(ctx, tokenB, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("B 列举失败：%v", err)
	}
	for _, object := range objectsB {
		if object.Path == relPath {
			t.Fatalf("隔离失效：B 看到了 A 的对象 %s", relPath)
		}
	}
	if bodyB, _, errB := client.Open(ctx, tokenB, drive.SpacePersonal, relPath); errB == nil {
		rawB, _ := io.ReadAll(bodyB)
		bodyB.Close()
		if string(rawB) == content {
			t.Fatal("隔离失效：B 按同名路径读到了 A 的内容")
		}
	}

	// 删除后不应再出现在列举里。
	if err := client.Delete(ctx, tokenA, drive.SpacePersonal, relPath); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	after, err := client.Objects(ctx, tokenA, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("删除后列举失败：%v", err)
	}
	for _, object := range after {
		if object.Path == relPath {
			t.Errorf("删除后仍能列举到 %s", relPath)
		}
	}
}

// TestLiveDriveRejectsTraversalAndAnonymous 确认不变量 2 与登录校验在活栈生效。
func TestLiveDriveRejectsTraversalAndAnonymous(t *testing.T) {
	baseURL := liveEnv(t)
	client := drive.New(baseURL)
	ctx := context.Background()

	if _, err := client.Objects(ctx, "", drive.SpacePersonal); err == nil {
		t.Error("未登录列举应被拒")
	}

	tokenA := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	for _, evil := range []string{"../escape.txt", "../../users/1/escape.txt", "/escape.txt", "a/../../b.txt"} {
		if _, err := client.PresignGet(ctx, tokenA, drive.SpacePersonal, evil); err == nil {
			t.Errorf("穿越路径未被拒：%s", evil)
		}
	}
}
