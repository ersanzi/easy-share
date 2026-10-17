package spacedav

import (
	"context"
	"errors"
	"testing"

	"easyshare/internal/drive"
)

// moveAwareClient 记录服务端 move 的调用，用来验证「有该能力时优先走它」。
type moveAwareClient struct {
	*fakeClient
	moves   [][2]string
	moveErr error
}

func (c *moveAwareClient) Move(_ context.Context, _, _, from, to string) error {
	if c.moveErr != nil {
		return c.moveErr
	}
	c.moves = append(c.moves, [2]string{from, to})
	return nil
}

// TestRenamePrefersServerSideMove 钉住 2026-10-16 的改名改造。
//
// 旧实现是客户端「下载 + 上传 + 删除」。它有三个生产级问题：
//
//  1. **换掉了 fileId**：上传到新键 = 新登记，旧 fileId 的分享链接、任务引用、
//     知识索引全部失效——改名在用户看来是「同一个文件」，在这些引用看来是「没了一个」；
//  2. **流量与耗时**：2 GB 文件改名要先把字节拉回客户端再传上去；
//  3. **中途失败留两份**：上传成功、删除失败，就是两份都在。
//
// 服务端 move（CopyObject + 删源）三者一起解决：字节不出对象存储、fileId 不变。
// 本用例同时断言**没有发生上传**——否则「也调了 move」掩盖不了仍然搬了一遍字节。
func TestRenamePrefersServerSideMove(t *testing.T) {
	base := newFake(drive.Object{Path: "old.bin", Size: 1024})
	client := &moveAwareClient{fakeClient: base}
	fs := newFS(client, drive.SpacePersonal, false)

	if err := fs.Rename(context.Background(), "/old.bin", "/new.bin"); err != nil {
		t.Fatalf("改名失败: %v", err)
	}

	if len(client.moves) != 1 || client.moves[0] != [2]string{"old.bin", "new.bin"} {
		t.Fatalf("应恰好走一次服务端 move，实得 %v", client.moves)
	}
	// 关键负断言：服务端 move 可行时**不该再搬字节**
	if len(base.uploads) != 0 {
		t.Errorf("走服务端 move 时不得再上传（那是旧实现的搬字节路径）：%v", base.uploads)
	}
	// 也不该走客户端删除源——删源是控制面在 CopyObject 之后自己做的
	if len(base.deleted) != 0 {
		t.Errorf("走服务端 move 时不得再单独删除源对象：%v", base.deleted)
	}
}

// TestRenameFallsBackWhenServerMoveUnavailable 钉住退化路径仍然可用。
//
// 假客户端与旧控制面没有 Move 能力，必须退回「下载 + 上传 + 删除」而不是直接失败——
// 否则升级顺序被绑死（客户端与服务端必须同时上线）。
func TestRenameFallsBackWhenServerMoveUnavailable(t *testing.T) {
	client := newFake(drive.Object{Path: "old.bin", Size: 10})
	fs := newFS(client, drive.SpacePersonal, false)

	if err := fs.Rename(context.Background(), "/old.bin", "/new.bin"); err != nil {
		t.Fatalf("退化路径改名失败: %v", err)
	}

	if _, ok := client.uploads["new.bin"]; !ok {
		t.Errorf("退化路径应上传到新键，实得 uploads=%v", client.uploads)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "old.bin" {
		t.Errorf("退化路径应删源，实得 deleted=%v", client.deleted)
	}
}

// TestRenameReportsServerMoveFailureWithoutTouchingSource 钉住失败时不动源。
//
// 服务端 move 报错（如目标已存在、配额、S3 拒绝）时必须原样上报，
// 且**不得**退回「下载上传删除」——目标已存在时退回只会把文件再复制一遍，
// 用户真正需要的是看到「目标位置已有同名文件」。
func TestRenameReportsServerMoveFailureWithoutTouchingSource(t *testing.T) {
	base := newFake(drive.Object{Path: "old.bin", Size: 1024})
	client := &moveAwareClient{fakeClient: base, moveErr: errors.New("目标位置已有同名文件，未覆盖")}
	fs := newFS(client, drive.SpacePersonal, false)

	err := fs.Rename(context.Background(), "/old.bin", "/new.bin")
	if err == nil {
		t.Fatal("服务端 move 失败时改名应报错")
	}
	if len(base.uploads) != 0 || len(base.deleted) != 0 {
		t.Errorf("move 失败后不得退回搬字节，也不得删源：uploads=%v deleted=%v",
			base.uploads, base.deleted)
	}
}

// 编译期断言：生产类型必须满足 mover，否则会静默退回搬字节的旧路。
var _ mover = (*drive.Client)(nil)
