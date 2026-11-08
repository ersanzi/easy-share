package drive_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/drive"
)

// TestLivePersistentEmptyDirectory 仅显式启用时访问真实控制面与对象存储。
// 配置沿用 cfmount/writeback_live_test.go，不内置地址、账号、密码或 token：
// EASYSHARE_LIVE_DRIVE=1 EASYSHARE_LIVE_BASE=... EASYSHARE_LIVE_USER_A=... EASYSHARE_LIVE_PASS_A=...
// go test ./internal/drive -run '^TestLivePersistentEmptyDirectory$' -count=1 -v
func TestLivePersistentEmptyDirectory(t *testing.T) {
	if os.Getenv("EASYSHARE_LIVE_DRIVE") == "" {
		t.Skip("设置 EASYSHARE_LIVE_DRIVE 才会执行真实目录写入")
	}
	base := strings.TrimRight(os.Getenv("EASYSHARE_LIVE_BASE"), "/")
	user, pass := os.Getenv("EASYSHARE_LIVE_USER_A"), os.Getenv("EASYSHARE_LIVE_PASS_A")
	if base == "" || user == "" || pass == "" {
		t.Skip("需要 EASYSHARE_LIVE_BASE、EASYSHARE_LIVE_USER_A、EASYSHARE_LIVE_PASS_A")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// 保留状态码、业务消息及错误类型；所有登录代次的 token 都参与脱敏。
	secrets := []string{user, pass}
	diagnostic := func(err error) string { return directoryLiveSafeError(err, secrets...) }
	loginFresh := func() string {
		t.Helper()
		session, err := account.New(base).Login(ctx, user, pass)
		if err != nil {
			t.Fatalf("真实账号登录失败：%s", diagnostic(err))
		}
		if session.Token == "" {
			t.Fatal("真实账号登录未返回 token")
		}
		secrets = append(secrets, session.Token)
		return session.Token
	}
	token := loginFresh()
	client := drive.New(base)
	capabilities, err := client.Capabilities(ctx, token)
	if err != nil {
		t.Fatalf("读取真实 capabilities 失败：%s", diagnostic(err))
	}
	if !capabilities.PersistentDirectories || capabilities.ProtocolVersion != 1 {
		t.Fatalf("要求 persistentDirectories=true、protocolVersion=1，实得 %v / %d",
			capabilities.PersistentDirectories, capabilities.ProtocolVersion)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("生成隔离路径失败")
	}
	root := "live-directory-" + hex.EncodeToString(nonce[:])
	emptyPath, filePath := root+"/empty", root+"/ordinary.txt"
	inScope := func(path string) bool { return path == root || strings.HasPrefix(path, root+"/") }
	objects, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("隔离路径预检：对象列表失败：%s", diagnostic(err))
	}
	for _, object := range objects {
		if inScope(object.Path) {
			t.Fatal("随机路径已被占用，拒绝写入或清理既有对象")
		}
	}
	recycled, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("隔离路径预检：回收站列表失败：%s", diagnostic(err))
	}
	for _, entry := range recycled {
		if inScope(entry.Path) || inScope(entry.OriginPath) {
			t.Fatal("随机路径已有回收站项，拒绝写入或清理")
		}
	}

	// 已核对服务端 DriveController.delete / DriveFileService.purgeFromRecycle：
	// Delete 只软删指定行，非空目录拒删；purge 按 fileId 删除单行，目录不删存储对象。
	// 已软删文件仍占存储键，因此先精确删除并 purge 子文件，再处理空目录与根目录。
	// 在首个写请求前注册，覆盖请求成功但响应丢失的情况；绝不递归删路径或清空回收站。
	t.Cleanup(func() {
		cleanCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		for _, path := range []string{filePath, emptyPath, root} {
			rows, listErr := client.Objects(cleanCtx, token, drive.SpacePersonal)
			if listErr != nil {
				t.Errorf("清理 %s：对象列表失败，保守跳过删除：%s", path, diagnostic(listErr))
				continue
			}
			for _, row := range rows {
				if row.Path != path {
					continue
				}
				// 使用列表返回的精确身份，不把路径当作递归删除前缀。
				if row.FileId.Int64() <= 0 {
					t.Errorf("清理 %s：缺少有效 fileId，拒绝删除", path)
					continue
				}
				if err := client.DeleteByFileID(cleanCtx, token, drive.SpacePersonal, row.FileId.Int64(), path); err != nil {
					t.Errorf("清理 %s：精确软删失败：%s", path, diagnostic(err))
				} else {
					t.Logf("清理 %s：已精确软删 FileId=%d", path, row.FileId.Int64())
				}
			}
			entries, listErr := client.RecycleList(cleanCtx, token, drive.SpacePersonal)
			if listErr != nil {
				t.Errorf("清理 %s：回收站列表失败：%s", path, diagnostic(listErr))
				continue
			}
			for _, entry := range entries {
				if entry.Path != path && entry.OriginPath != path {
					continue
				}
				// 两个路径字段均不得指向测试路径之外，避免按身份误删别人的项。
				if (entry.Path != "" && entry.Path != path) || (entry.OriginPath != "" && entry.OriginPath != path) || entry.FileId.Int64() <= 0 {
					t.Errorf("清理 %s：回收站身份/路径不一致，拒绝 purge", path)
					continue
				}
				if err := client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, entry.FileId.Int64()); err != nil {
					t.Errorf("清理 %s：精确 purge 失败：%s", path, diagnostic(err))
				} else {
					t.Logf("清理 %s：已精确 purge FileId=%d", path, entry.FileId.Int64())
				}
			}
		}
		rows, listErr := client.Objects(cleanCtx, token, drive.SpacePersonal)
		if listErr != nil {
			t.Errorf("清理验收：对象列表失败：%s", diagnostic(listErr))
		}
		for _, row := range rows {
			if inScope(row.Path) {
				t.Errorf("清理验收：隔离路径仍有对象 %s", row.Path)
			}
		}
		entries, listErr := client.RecycleList(cleanCtx, token, drive.SpacePersonal)
		if listErr != nil {
			t.Errorf("清理验收：回收站列表失败：%s", diagnostic(listErr))
		}
		for _, entry := range entries {
			if inScope(entry.Path) || inScope(entry.OriginPath) {
				t.Error("清理验收：隔离路径仍有回收站项")
			}
		}
	})

	create := func(path string) *drive.Object {
		t.Helper()
		object, err := client.CreateDirectory(ctx, token, drive.SpacePersonal, path)
		if err != nil {
			// 返回校验/解码失败不代表服务端未写入；只读探测精确路径，清理由已注册的 Cleanup 完成。
			probeCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			rows, probeErr := client.Objects(probeCtx, token, drive.SpacePersonal)
			if probeErr != nil {
				t.Logf("CreateDirectory 失败后探测 %s：%s", path, diagnostic(probeErr))
			} else {
				found := false
				for _, row := range rows {
					if row.Path == path {
						found = true
						t.Logf("CreateDirectory 返回错误但 objects 已有精确路径 %s：Directory=%t FileId=%d；cleanup 将按身份清理", path, row.Directory, row.FileId.Int64())
					}
				}
				if !found {
					t.Logf("CreateDirectory 失败后 objects 尚无精确路径 %s", path)
				}
			}
			t.Fatalf("CreateDirectory %s 失败：%s", path, diagnostic(err))
		}
		if object == nil || !object.Directory || object.Path != path || object.FileId.Int64() <= 0 {
			t.Fatalf("CreateDirectory %s 未返回正确 Directory/Path/FileId", path)
		}
		return object
	}
	create(root)
	empty := create(emptyPath)
	again := create(emptyPath)
	if again.FileId != empty.FileId {
		t.Fatalf("同名目录幂等身份变化：%d -> %d", empty.FileId.Int64(), again.FileId.Int64())
	}

	// 新登录 + 全新客户端只能从服务端 objects 读取，不能靠本地缓存证明持久化。
	token = loginFresh()
	client = drive.New(base)
	verifyObject := func(path string, directory bool, id int64) drive.Object {
		t.Helper()
		rows, err := client.Objects(ctx, token, drive.SpacePersonal)
		if err != nil {
			t.Fatalf("持久化验收：objects 列表失败：%s", diagnostic(err))
		}
		var found *drive.Object
		for i := range rows {
			if path == emptyPath && strings.HasPrefix(rows[i].Path, emptyPath+"/") {
				t.Fatal("待验收的空目录下出现子项")
			}
			if rows[i].Path == path {
				if found != nil {
					t.Fatalf("objects 返回重复路径 %s", path)
				}
				found = &rows[i]
			}
		}
		if found == nil || found.Directory != directory || found.FileId.Int64() <= 0 {
			t.Fatalf("objects 缺少正确类型/身份的对象 %s", path)
		}
		if id > 0 && found.FileId.Int64() != id {
			t.Fatalf("objects 身份变化 %s", path)
		}
		return *found
	}
	verifyObject(emptyPath, true, empty.FileId.Int64())
	if after := create(emptyPath); after.FileId != empty.FileId {
		t.Fatal("重新登录后幂等目录身份变化")
	}

	const content = "persistent-directory-live-file\n"
	if err := client.Upload(ctx, token, drive.SpacePersonal, emptyPath, strings.NewReader(content), int64(len(content))); err == nil {
		t.Fatal("文件上传不应允许覆盖同名目录")
	}
	verifyObject(emptyPath, true, empty.FileId.Int64())
	if err := client.Upload(ctx, token, drive.SpacePersonal, filePath, strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("准备普通文件失败：%s", diagnostic(err))
	}
	file := verifyObject(filePath, false, 0)
	if _, err := client.CreateDirectory(ctx, token, drive.SpacePersonal, filePath); err == nil {
		t.Fatal("同名文件存在时不应允许创建目录")
	}
	verifyObject(filePath, false, file.FileId.Int64())
	body, _, err := client.Open(ctx, token, drive.SpacePersonal, filePath)
	if err != nil {
		t.Fatalf("冲突验收：读取普通文件失败：%s", diagnostic(err))
	}
	got, readErr := io.ReadAll(io.LimitReader(body, int64(len(content))+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || string(got) != content {
		t.Fatal("创建同名目录的失败请求破坏了原文件内容")
	}
	verifyObject(emptyPath, true, empty.FileId.Int64())
	t.Log("真实空目录验收通过：协议 v1、持久身份、重登录可见、双向文件/目录冲突保护")
}

// directoryLiveSafeError 不输出 URL（含全部预签名查询串）、凭据字段或任何已知登录 token。
// 截断发生在脱敏之后，避免只截掉凭据前缀而留下其余部分。
func directoryLiveSafeError(err error, secrets ...string) string {
	if err == nil {
		return "<nil>"
	}
	message := err.Error()
	message = regexp.MustCompile(`(?i)https?://[^\s<>"']+`).ReplaceAllString(message, "[REDACTED_URL]")
	message = regexp.MustCompile(`(?i)(bearer\s+)[^\s,"'}]+`).ReplaceAllString(message, "${1}[REDACTED]")
	message = regexp.MustCompile(`(?i)((?:token|password|passwd|authorization|signature|credential|secret|accesskey|security-token)["']?\s*[:=]\s*["']?)[^\s,"';&}]+`).ReplaceAllString(message, "${1}[REDACTED]")
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	// 额外隐藏未收集的 JWT 或长不透明密钥，例如登录失败响应中的令牌。
	message = regexp.MustCompile(`[A-Za-z0-9_+/=.-]{48,}`).ReplaceAllString(message, "[REDACTED_OPAQUE]")
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > 1200 {
		message = string(runes[:1200]) + " [truncated]"
	}
	return fmt.Sprintf("%T: %s", err, message)
}

// 脱敏规则使用纯本地错误验证，无需活栈开关或真实凭据。
func TestDirectoryLiveSafeError(t *testing.T) {
	const token = "test-session-token"
	const password = "test-password"
	err := fmt.Errorf("HTTP 500: 创建目录失败 token=%s password=%s Bearer unseen-token GET https://storage.invalid/object?X-Amz-Signature=private-signature", token, password)
	got := directoryLiveSafeError(err, token, password)
	for _, secret := range []string{token, password, "unseen-token", "https://", "private-signature", "storage.invalid"} {
		if strings.Contains(got, secret) {
			t.Fatal("诊断输出泄露敏感字段")
		}
	}
	for _, want := range []string{"HTTP 500", "创建目录失败", "*errors.errorString"} {
		if !strings.Contains(got, want) {
			t.Fatalf("诊断输出应保留 %q", want)
		}
	}
	if got := directoryLiveSafeError(fmt.Errorf("创建远端目录失败：服务端未返回持久化目录身份")); !strings.Contains(got, "未返回持久化目录身份") {
		t.Fatal("响应校验错误应保留具体原因")
	}
}
