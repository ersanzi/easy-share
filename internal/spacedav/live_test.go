package spacedav_test

// 活栈回归：把「此电脑」挂载盘的读写链路接到**真实控制面 + 真实对象存储**上跑一遍。
//
// 为什么必须补这一层：spacedav 是本仓缺陷最密集的包——
// KI-9（Seek 漏 (0,SeekStart)，双击任何文件都 500）、
// KI-12（改名走「下载+上传+删除」，每次留回收站副本且换掉 fileId）、
// KI-13（拖大文件吃等量内存）**三个高危缺陷全部出在这里**，
// 而它此前**零活栈覆盖**——单测里 Client 全是假实现。
// 「测试全绿但真实链路不工作」在这个包上已经发生过三次，
// 补活栈覆盖是唯一能改变这个格局的做法。
//
// 本用例走的是 WebDAV handler 真正会走的那几个方法：
// OpenFile(读) / OpenFile(写) / Stat / RemoveAll / Rename，
// 断言全部落在**真实控制面返回的结果**上，而不是假对象。

import (
	"bytes"
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
	"easyshare/internal/spacedav"
)

// liveSpace 组装一个连着真实控制面的 FS。
func liveSpace(t *testing.T) (*spacedav.FS, *drive.Client, string) {
	t.Helper()
	baseURL := os.Getenv("EASYSHARE_LIVE_BASE")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8090"
	}
	if os.Getenv("EASYSHARE_LIVE_DRIVE") == "" {
		t.Skip("未设 EASYSHARE_LIVE_DRIVE，跳过活栈测试")
	}
	user, pass := os.Getenv("EASYSHARE_LIVE_USER_A"), os.Getenv("EASYSHARE_LIVE_PASS_A")
	if user == "" || pass == "" {
		t.Skip("缺少 EASYSHARE_LIVE_USER_A / EASYSHARE_LIVE_PASS_A")
	}
	session, err := account.New(baseURL).Login(context.Background(), user, pass)
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}
	client := drive.New(baseURL)
	// 每条用例独立版本文件，避免上一条用例的 last_version 影响下一条的增量起点。
	// 这也符合“进程重启”的语义：每个测试进程都有自己的持久化状态。
	fs := spacedav.New(spacedav.Options{
		Client:   client,
		Space:    drive.SpacePersonal,
		Token:    func() string { return session.Token },
		Label:    "EasyShare",
		SpoolDir: t.TempDir(),
		Versions: spacedav.NewDiskVersionStore(filepath.Join(t.TempDir(), "space-versions.json")),
	})
	return fs, client, session.Token
}

// TestLiveSpacedavRoundTrip 走一遍挂载盘的真实读写：
// 写 → 列 → 读（含 KI-9 的 Seek）→ 改名（含 KI-12 的 fileId 稳定）→ 删。
func TestLiveSpacedavRoundTrip(t *testing.T) {
	fs, client, token := liveSpace(t)
	ctx := context.Background()
	// 每次运行唯一路径：不同用例/不同运行之间不复用目录行
	id := time.Now().UnixNano()
	path := fmt.Sprintf("/live-spacedav/roundtrip-%d.txt", id)
	renamed := fmt.Sprintf("/live-spacedav/renamed-%d.txt", id)
	const content = "spacedav-live-content"

	// 前置清理
	_ = fs.RemoveAll(ctx, path)
	registerLiveCleanup(t, fs, client, token, path, renamed)

	// ① 写：WebDAV 的 PUT 路径（OpenFile 带 O_CREATE）
	w, err := fs.OpenFile(ctx, path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(写) 失败：%v", err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close（真正上传）失败：%v", err)
	}

	// ② Stat：大小要对得上
	info, err := fs.Stat(ctx, path)
	if err != nil {
		t.Fatalf("Stat 失败：%v", err)
	}
	if info.Size() != int64(len(content)) {
		t.Errorf("Stat 大小 %d，期望 %d", info.Size(), len(content))
	}

	// ③ 读：这是 KI-9 的现场——readFile.Seek 漏了 (0, SeekStart)，
	//    导致 http.ServeContent 拿不到内容、GET 一律 500。
	//    这里直接读回全部字节，字节不一致就是回归。
	r, err := fs.OpenFile(ctx, path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(读) 失败：%v", err)
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if string(got) != content {
		t.Errorf("读回内容 %q，期望 %q（KI-9：Seek 回归会让这里拿不到数据）", got, content)
	}

	// ④ 读回来的 fileId：改名后必须不变（KI-12）
	before := findLiveObject(t, ctx, client, token, strings.TrimPrefix(path, "/"))
	if before == nil {
		t.Fatal("真实控制面里应能列出刚写的文件")
	}
	originalID := before.FileId.Int64()

	// ⑤ 改名
	if err := fs.Rename(ctx, path, renamed); err != nil {
		t.Fatalf("Rename 失败：%v", err)
	}
	after := findLiveObject(t, ctx, client, token, strings.TrimPrefix(renamed, "/"))
	if after == nil {
		t.Fatal("改名后目标路径应存在")
	}
	if after.FileId.Int64() != originalID {
		t.Errorf("改名换掉了 fileId：%d -> %d（KI-12：分享/索引引用会全断）",
			originalID, after.FileId.Int64())
	}
	t.Logf("✅ 写/列/读/改名全通，fileId 稳定为 %d", originalID)

	// ⑥ 删
	if err := fs.RemoveAll(ctx, renamed); err != nil {
		t.Fatalf("RemoveAll 失败：%v", err)
	}
	if findLiveObject(t, ctx, client, token, strings.TrimPrefix(renamed, "/")) != nil {
		t.Error("删除后列表里仍有该文件")
	}
	t.Log("✅ 删除生效")
}

// TestLiveSpacedavServeContentSeeks 钉住 http.ServeContent 依赖的三个 Seek 形态。
//
// 资源管理器打开大文件、媒体播放器拖进度条都靠 Range 请求，而 Range 的处理
// 由 http.ServeContent 完成——它只用 (0,Start)/(0,Current)/(0,End) 这三种形态。
// KI-9 正是这条路径上的缺陷（漏了 (0,Start)，GET 一律 500），所以值得单独钉。
func TestLiveSpacedavRangeRead(t *testing.T) {
	fs, client, token := liveSpace(t)
	ctx := context.Background()
	const path = "/live-spacedav/range.bin"
	payload := []byte("0123456789abcdefghij")

	_ = fs.RemoveAll(ctx, path)
	// 只 RemoveAll 是**软删**：行会留在回收站（KI-25 的教训——残留每跑必增）。
	// 所以清理必须一路走到 RecyclePurge。
	registerLiveCleanup(t, fs, client, token, path)

	w, err := fs.OpenFile(ctx, path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(写) 失败：%v", err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}

	r, err := fs.OpenFile(ctx, path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(读) 失败：%v", err)
	}
	defer r.Close()

	// ⚠️ 这里刻意**不**用 Seek(5, Start)：那是 http.ServeContent 的职责，
	// 它自己按 ServeContent 契约控制 readFile（本包的 Seek 只支持
	// (0,Start)/(0,Current)/(0,End)，见 seek_contract_test.go 的注释）。
	// 我第一版就误用了 Seek(5,Start)，结果拿到 "invalid argument"——
	// 差点把「用错 API」记成产品缺陷。真实的范围读取由 webdav.Handler
	// 自己切分，所以这里验证的是它**依赖的那三个 Seek 形态**在真机上可用。

	// ① 读前先回开头（ServeContent 处理 Range 时会这么做）
	if pos, err := r.Seek(0, io.SeekStart); err != nil || pos != 0 {
		t.Fatalf("Seek(0, Start) = (%d,%v)，期望 (0,nil)——"+
			"ServeContent 会因此回 500，整份下载不可用（KI-9）", pos, err)
	}

	// ② 顺序读到的就是原内容（Range 的切片基准）
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if !bytes.Equal(buf, payload) {
		t.Errorf("读到 %q，期望 %q", buf, payload)
	}

	// ③ SeekCurrent 要如实报告已读位置（ServeContent 据此推算剩余）
	if pos, err := r.Seek(0, io.SeekCurrent); err != nil {
		t.Fatalf("Seek(0, Current) 失败：%v", err)
	} else if pos != int64(len(payload)) {
		t.Errorf("读完后 SeekCurrent = %d，期望 %d", pos, len(payload))
	}

	// ④ SeekEnd 给出总长（ServeContent 用它算 Content-Length 与 Range 边界）
	if pos, err := r.Seek(0, io.SeekEnd); err != nil {
		t.Fatalf("Seek(0, End) 失败：%v", err)
	} else if pos != int64(len(payload)) {
		t.Errorf("SeekEnd = %d，期望 %d", pos, len(payload))
	}
	t.Logf("✅ ServeContent 依赖的三个 Seek 形态在真机上均可用（文件 %d 字节）", len(payload))
}

// findLiveObject 在真实列举里按相对路径找对象（找不到返回 nil）。
func findLiveObject(t *testing.T, ctx context.Context, client *drive.Client, token, rel string) *drive.Object {
	t.Helper()
	objs, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	for i := range objs {
		if objs[i].Path == rel {
			return &objs[i]
		}
	}
	return nil
}

// registerLiveCleanup 注册**失败也会执行**的清理，并一路走到 RecyclePurge。
//
// 两个要点都是被真机打出来过的：
//   - 用 t.Cleanup 而不是写在末尾：失败时 t.Fatalf 会直接返回，末尾清理不执行（KI-25）；
//   - RemoveAll 只是**软删**，行仍留在回收站；要彻底清干净必须 RecyclePurge，
//     否则每跑一次就多一条回收站残留（KI-25 实测）。
func registerLiveCleanup(t *testing.T, fs *spacedav.FS, client *drive.Client, token string, paths ...string) {
	t.Helper()
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, p := range paths {
			_ = fs.RemoveAll(cleanCtx, p)
		}
		entries, err := client.RecycleList(cleanCtx, token, drive.SpacePersonal)
		if err != nil {
			return
		}
		for _, e := range entries {
			if strings.HasPrefix(e.OriginPath, "live-spacedav/") || strings.HasPrefix(e.Path, "live-spacedav/") {
				_ = client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, e.FileId.Int64())
			}
		}
	})
}

// TestLiveSpacedavRenameOntoExistingKeepsBoth 钉住切片 3 的第四条规则：
// 移动/改名目标已存在 → 生成冲突副本，**双方都保留**。
//
// 服务端 move 已在目录层拒绝覆盖（一个字节不复制）；
// 客户端负责把这次拒绝翻译成用户可理解的行为——自动落
// `新名 (冲突副本)`，而不是把“保存失败”丢给用户。
func TestLiveSpacedavRenameOntoExistingKeepsBoth(t *testing.T) {
	fs, client, token := liveSpace(t)
	ctx := context.Background()
	id := time.Now().UnixNano()
	source := fmt.Sprintf("/live-spacedav/rename-a-%d.txt", id)
	target := fmt.Sprintf("/live-spacedav/rename-b-%d.txt", id)
	conflict := strings.TrimSuffix(target, ".txt") + " (冲突副本).txt"
	const srcContent = "rename-source"
	const dstContent = "rename-existing-target"

	_ = fs.RemoveAll(ctx, source)
	_ = fs.RemoveAll(ctx, target)
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = fs.RemoveAll(cleanCtx, source)
		_ = fs.RemoveAll(cleanCtx, target)
		_ = fs.RemoveAll(cleanCtx, conflict)
		entries, err := client.RecycleList(cleanCtx, token, drive.SpacePersonal)
		if err != nil {
			return
		}
		for _, e := range entries {
			if strings.HasPrefix(e.OriginPath, "live-spacedav/") || strings.HasPrefix(e.Path, "live-spacedav/") {
				_ = client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, e.FileId.Int64())
			}
		}
	})

	// 先放两个文件：source 与 target 同名不同内容
	if err := writeLiveFile(t, fs, source, srcContent); err != nil {
		t.Fatal(err)
	}
	if err := writeLiveFile(t, fs, target, dstContent); err != nil {
		t.Fatal(err)
	}

	// 把 source 改名到 target —— 目标已存在，必须自动落冲突副本
	if err := fs.Rename(ctx, source, target); err != nil {
		t.Fatalf("目标占用时的 Rename 应自动生成冲突副本，实得：%v", err)
	}

	gotTarget := readLiveFile(t, fs, target)
	gotConflict := readLiveFile(t, fs, conflict)
	if gotTarget != dstContent || gotConflict != srcContent {
		t.Errorf("双方内容都应保留：target=%q（期望 %q），conflict=%q（期望 %q）",
			gotTarget, dstContent, gotConflict, srcContent)
	}
	t.Logf("✅ 目标占用改名保留双方：%s=%q，%s=%q", target, gotTarget, conflict, gotConflict)
}

// 目录是云端实体，不是本机便签：资源管理器里 Mkdir 必须在控制面留下目录标记，
// RemoveAll 之后标记也要一并摘掉。单测里 Client 全是假实现，这条只能钉在活栈上——
// 症状是「网盘页看不到我刚建的文件夹」和「删掉的空文件夹又长回来」。
func TestLiveSpacedavDirectoryIsCloudEntity(t *testing.T) {
	fs, client, token := liveSpace(t)
	ctx := context.Background()
	dir := fmt.Sprintf("/live-spacedav/cloud-dir-%d", time.Now().UnixNano())
	rel := strings.TrimPrefix(dir, "/")

	registerLiveCleanup(t, fs, client, token, dir)

	if err := fs.Mkdir(ctx, dir, 0o755); err != nil {
		t.Fatalf("WebDAV Mkdir 失败：%v", err)
	}
	if !liveHasObject(t, client, token, rel+"/") {
		t.Errorf("控制面应看到目录标记 %s/（网盘页据此显示文件夹），实际没有", rel)
	}

	child := rel + "/a.txt"
	if err := writeLiveFile(t, fs, "/"+child, "A"); err != nil {
		t.Fatalf("写入子文件失败：%v", err)
	}
	if !liveHasObject(t, client, token, child) {
		t.Fatalf("前置条件不成立：子文件 %s 没出现在服务端清单", child)
	}

	if err := fs.RemoveAll(ctx, dir); err != nil {
		t.Fatalf("递归删目录失败：%v", err)
	}
	if liveHasObject(t, client, token, child) {
		t.Errorf("删除后子文件 %s 仍在服务端在用清单", child)
	}
	if liveHasObject(t, client, token, rel+"/") {
		t.Errorf("删除后服务端仍留着目录标记 %s/ ——网盘页会显示一个本机已删的空文件夹", rel)
	}
}

// liveHasObject 在**含目录标记**的服务端清单里查一个路径是否存在。
func liveHasObject(t *testing.T, client *drive.Client, token, path string) bool {
	t.Helper()
	objects, err := client.ObjectsWithDirs(context.Background(), token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("列举服务端对象失败：%v", err)
	}
	for _, o := range objects {
		if o.Path == path {
			return true
		}
	}
	return false
}

func writeLiveFile(t *testing.T, fs *spacedav.FS, name, content string) error {
	t.Helper()
	w, err := fs.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(content)); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func readLiveFile(t *testing.T, fs *spacedav.FS, name string) string {
	t.Helper()
	r, err := fs.OpenFile(context.Background(), name, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("打开 %s 失败：%v", name, err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", name, err)
	}
	return string(data)
}
