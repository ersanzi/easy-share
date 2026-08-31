package spacedav

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"easyshare/internal/drive"
)

// fakeClient 记录收到的调用，便于断言「空间」与「大小」是否被正确透传。
type fakeClient struct {
	objects []drive.Object
	// 记录每次调用看到的 space，用来钉住跨空间隔离
	spaces []string
	// uploads 记录 path→size，配额依赖 size 被正确上报
	uploads   map[string]int64
	deleted   []string
	openErr   error
	listErr   error
	uploadErr error
}

func newFake(objs ...drive.Object) *fakeClient {
	return &fakeClient{objects: objs, uploads: map[string]int64{}}
}

func (c *fakeClient) Objects(_ context.Context, _, space string) ([]drive.Object, error) {
	c.spaces = append(c.spaces, space)
	if c.listErr != nil {
		return nil, c.listErr
	}
	return c.objects, nil
}

func (c *fakeClient) Open(_ context.Context, _, space, path string) (io.ReadCloser, int64, error) {
	c.spaces = append(c.spaces, space)
	if c.openErr != nil {
		return nil, 0, c.openErr
	}
	for _, obj := range c.objects {
		if obj.Path == path {
			return io.NopCloser(strings.NewReader("hello-body")), obj.Size, nil
		}
	}
	return nil, 0, errors.New("对象不存在")
}

func (c *fakeClient) Upload(_ context.Context, _, space, path string, body io.Reader, size int64) error {
	c.spaces = append(c.spaces, space)
	if c.uploadErr != nil {
		return c.uploadErr
	}
	data, _ := io.ReadAll(body)
	if int64(len(data)) != size {
		return errors.New("申报大小与实际字节数不符")
	}
	c.uploads[path] = size
	return nil
}

func (c *fakeClient) Delete(_ context.Context, _, space, path string) error {
	c.spaces = append(c.spaces, space)
	c.deleted = append(c.deleted, path)
	return nil
}

func loggedIn() string { return "tok" }

func newFS(client Client, space string, readOnly bool) *FS {
	return New(Options{Client: client, Space: space, Token: loggedIn, ReadOnly: readOnly, Label: "测试空间"})
}

func TestWriteReportsRealSizeForQuota(t *testing.T) {
	client := newFake()
	fs := newFS(client, drive.SpacePersonal, false)

	file, err := fs.OpenFile(context.Background(), "/a.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile 出错：%v", err)
	}
	if _, err := file.Write([]byte("12345")); err != nil {
		t.Fatalf("Write 出错：%v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close 出错：%v", err)
	}
	// 配额判定完全依赖这个 size：上报 0 就等于配额失效
	if got := client.uploads["a.txt"]; got != 5 {
		t.Fatalf("上传申报大小 = %d，应为 5", got)
	}
}

func TestReadOnlySpaceRejectsAllWrites(t *testing.T) {
	client := newFake(drive.Object{Path: "a.txt", Size: 3, LastModified: time.Now()})
	fs := newFS(client, drive.SpaceShared, true)
	ctx := context.Background()

	if _, err := fs.OpenFile(ctx, "/new.txt", os.O_CREATE|os.O_WRONLY, 0o644); !errors.Is(err, os.ErrPermission) {
		t.Errorf("只读空间创建文件应被拒，得到 %v", err)
	}
	if err := fs.Mkdir(ctx, "/dir", 0o755); !errors.Is(err, os.ErrPermission) {
		t.Errorf("只读空间建目录应被拒，得到 %v", err)
	}
	if err := fs.RemoveAll(ctx, "/a.txt"); !errors.Is(err, os.ErrPermission) {
		t.Errorf("只读空间删除应被拒，得到 %v", err)
	}
	if err := fs.Rename(ctx, "/a.txt", "/b.txt"); !errors.Is(err, os.ErrPermission) {
		t.Errorf("只读空间改名应被拒，得到 %v", err)
	}
	// 只读不等于不可读
	if _, err := fs.OpenFile(ctx, "/a.txt", os.O_RDONLY, 0); err != nil {
		t.Errorf("只读空间应仍可读，得到 %v", err)
	}
	if len(client.uploads) != 0 || len(client.deleted) != 0 {
		t.Errorf("只读空间不应产生任何写调用：uploads=%v deleted=%v", client.uploads, client.deleted)
	}
}

func TestLoggedOutDeniesEverything(t *testing.T) {
	client := newFake(drive.Object{Path: "a.txt", Size: 3})
	// 退出登录后令牌为空——挂载点还在，但必须立刻失去访问能力
	fs := New(Options{Client: client, Space: drive.SpacePersonal, Token: func() string { return "" }})
	ctx := context.Background()

	if _, err := fs.OpenFile(ctx, "/a.txt", os.O_RDONLY, 0); !errors.Is(err, os.ErrPermission) {
		t.Errorf("未登录读取应被拒，得到 %v", err)
	}
	if _, err := fs.OpenFile(ctx, "/n.txt", os.O_CREATE|os.O_WRONLY, 0o644); !errors.Is(err, os.ErrPermission) {
		t.Errorf("未登录写入应被拒，得到 %v", err)
	}
	if _, err := fs.Stat(ctx, "/a.txt"); !errors.Is(err, os.ErrPermission) {
		t.Errorf("未登录 Stat 应被拒，得到 %v", err)
	}
}

func TestSpaceIsAlwaysPassedThrough(t *testing.T) {
	client := newFake(drive.Object{Path: "a.txt", Size: 3})
	fs := newFS(client, drive.SpaceShared, false)
	ctx := context.Background()

	if _, err := fs.Stat(ctx, "/a.txt"); err != nil {
		t.Fatalf("Stat 出错：%v", err)
	}
	if err := fs.RemoveAll(ctx, "/a.txt"); err != nil {
		t.Fatalf("RemoveAll 出错：%v", err)
	}
	// 每一次落到控制面的调用都必须带对空间，否则会操作到别的空间
	for _, space := range client.spaces {
		if space != drive.SpaceShared {
			t.Fatalf("出现了非本空间的调用：%v", client.spaces)
		}
	}
}

func TestPathNormalizationStripsEscapes(t *testing.T) {
	cases := map[string]string{
		"/a.txt":      "a.txt",
		"a.txt":       "a.txt",
		"/dir/b.txt":  "dir/b.txt",
		"/dir//b.txt": "dir/b.txt",
		`\dir\b.txt`:  "dir/b.txt",
		"/":           "",
		"":            "",
		".":           "",
		"/dir/":       "dir",
	}
	for input, want := range cases {
		if got := toKey(input); got != want {
			t.Errorf("toKey(%q) = %q，应为 %q", input, got, want)
		}
	}
}

func TestDirectoryListingSplitsFilesAndDirs(t *testing.T) {
	client := newFake(
		drive.Object{Path: "a.txt", Size: 1, LastModified: time.Now()},
		drive.Object{Path: "docs/b.txt", Size: 2, LastModified: time.Now()},
		drive.Object{Path: "docs/deep/c.txt", Size: 3, LastModified: time.Now()},
	)
	fs := newFS(client, drive.SpacePersonal, false)
	ctx := context.Background()

	root, err := fs.OpenFile(ctx, "/", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("打开根目录出错：%v", err)
	}
	entries, err := root.Readdir(0)
	if err != nil {
		t.Fatalf("Readdir 出错：%v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("根下应有 docs/ 与 a.txt 两项，实得 %d", len(entries))
	}
	// 目录排在前面
	if !entries[0].IsDir() || entries[0].Name() != "docs" {
		t.Errorf("第一项应是目录 docs，实得 %q isDir=%v", entries[0].Name(), entries[0].IsDir())
	}
	if entries[1].IsDir() || entries[1].Name() != "a.txt" {
		t.Errorf("第二项应是文件 a.txt，实得 %q", entries[1].Name())
	}

	// 子目录只列直接子项：deep/ 是目录，不应把 c.txt 提上来
	sub, err := fs.OpenFile(ctx, "/docs", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("打开 docs 出错：%v", err)
	}
	subEntries, _ := sub.Readdir(0)
	if len(subEntries) != 2 {
		t.Fatalf("docs 下应有 deep/ 与 b.txt，实得 %d", len(subEntries))
	}
}

func TestMkdirSurvivesUntilFirstFile(t *testing.T) {
	client := newFake()
	fs := newFS(client, drive.SpacePersonal, false)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/empty", 0o755); err != nil {
		t.Fatalf("Mkdir 出错：%v", err)
	}
	// 对象存储没有真目录：新建的空目录必须在本次会话里可见，否则刷新就消失
	info, err := fs.Stat(ctx, "/empty")
	if err != nil {
		t.Fatalf("新建目录后 Stat 出错：%v", err)
	}
	if !info.IsDir() {
		t.Error("新建的应是目录")
	}
	root, _ := fs.OpenFile(ctx, "/", os.O_RDONLY, 0)
	entries, _ := root.Readdir(0)
	if len(entries) != 1 || entries[0].Name() != "empty" {
		t.Errorf("根下应列出空目录 empty，实得 %v", entries)
	}
}

func TestRenameDirectoryIsRefusedNotHalfDone(t *testing.T) {
	client := newFake(
		drive.Object{Path: "docs/b.txt", Size: 2},
		drive.Object{Path: "docs/c.txt", Size: 3},
	)
	fs := newFS(client, drive.SpacePersonal, false)

	err := fs.Rename(context.Background(), "/docs", "/newdocs")
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("目录改名应被明确拒绝，得到 %v", err)
	}
	// 关键：拒绝要干净，不能已经搬走了一半
	if len(client.uploads) != 0 || len(client.deleted) != 0 {
		t.Errorf("被拒的改名不应产生任何写：uploads=%v deleted=%v", client.uploads, client.deleted)
	}
}

func TestRenameUploadsBeforeDeleting(t *testing.T) {
	client := newFake(drive.Object{Path: "a.txt", Size: 10})
	client.uploadErr = errors.New("配额不足")
	fs := newFS(client, drive.SpacePersonal, false)

	if err := fs.Rename(context.Background(), "/a.txt", "/b.txt"); err == nil {
		t.Fatal("上传失败时改名应报错")
	}
	// 上传失败必须不删源文件，否则数据就没了
	if len(client.deleted) != 0 {
		t.Errorf("上传失败却删了源文件：%v", client.deleted)
	}
}

func TestRemoveAllDeletesWholeSubtree(t *testing.T) {
	client := newFake(
		drive.Object{Path: "docs/b.txt", Size: 2},
		drive.Object{Path: "docs/deep/c.txt", Size: 3},
		drive.Object{Path: "keep.txt", Size: 4},
	)
	fs := newFS(client, drive.SpacePersonal, false)

	if err := fs.RemoveAll(context.Background(), "/docs"); err != nil {
		t.Fatalf("RemoveAll 出错：%v", err)
	}
	if len(client.deleted) != 2 {
		t.Fatalf("应删掉 docs 下 2 个对象，实删 %v", client.deleted)
	}
	for _, path := range client.deleted {
		if !strings.HasPrefix(path, "docs/") {
			t.Errorf("删到了 docs 之外的对象：%s", path)
		}
	}
}

func TestListErrorSurfacesNotSilentEmpty(t *testing.T) {
	client := newFake()
	client.listErr = errors.New("没有共享空间的访问权限")
	fs := newFS(client, drive.SpaceShared, false)

	// 权限错误必须冒出来，不能表现成一个空目录——那会让人以为文件丢了
	if _, err := fs.OpenFile(context.Background(), "/", os.O_RDONLY, 0); err == nil {
		t.Fatal("列举失败时应报错，而不是返回空目录")
	}
}

func TestListCacheCollapsesRepeatedBrowse(t *testing.T) {
	client := newFake(drive.Object{Path: "a.txt", Size: 1})
	fs := newFS(client, drive.SpacePersonal, false)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := fs.Stat(ctx, "/a.txt"); err != nil {
			t.Fatalf("Stat 出错：%v", err)
		}
	}
	// 资源管理器打开目录会连发多次请求，缓存应把它们压成一次
	if len(client.spaces) != 1 {
		t.Errorf("5 次 Stat 应只回控制面 1 次，实际 %d 次", len(client.spaces))
	}

	// 写操作后缓存必须失效，否则用户看不到刚传上去的文件
	file, _ := fs.OpenFile(ctx, "/b.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	_, _ = file.Write([]byte("x"))
	_ = file.Close()
	if _, err := fs.Stat(ctx, "/a.txt"); err != nil {
		t.Fatalf("Stat 出错：%v", err)
	}
	if len(client.spaces) < 3 {
		t.Errorf("写后应重新拉清单，调用序列：%v", client.spaces)
	}
}
