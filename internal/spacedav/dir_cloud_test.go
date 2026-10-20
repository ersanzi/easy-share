package spacedav

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"easyshare/internal/drive"
)

// 这一组用例钉的是「目录是云端实体，不是本机便签」：
// 资源管理器里新建/删除的文件夹必须同步到控制面，否则网盘页与别的电脑
// 看到的是另一个世界（钉盘/WPS 的目录所有端一致，见
// docs/intelligence/2026-10-15-dingpan-file-storage-reverse.md §9「目录对象」）。

// dirMarkerClient 给假客户端补上控制面目录能力（*drive.Client 的 Mkdir）。
type dirMarkerClient struct {
	*fakeClient
	mkdirs   []string
	mkdirErr error
}

func (c *dirMarkerClient) Mkdir(_ context.Context, _, space, path string) (string, error) {
	c.spaces = append(c.spaces, space)
	if c.mkdirErr != nil {
		return "", c.mkdirErr
	}
	c.mkdirs = append(c.mkdirs, path)
	return path + "/", nil
}

func TestMkdirRegistersDirectoryOnControlPlane(t *testing.T) {
	client := &dirMarkerClient{fakeClient: newFake()}
	fs := newFS(client, drive.SpacePersonal, false)

	if err := fs.Mkdir(context.Background(), "/报告", 0o755); err != nil {
		t.Fatalf("Mkdir 出错：%v", err)
	}
	if !slices.Equal(client.mkdirs, []string{"报告"}) {
		t.Errorf("控制面应收到目录标记创建，实得 %v", client.mkdirs)
	}
	// 本机记录同时保留：控制面失败或旧服务端时它仍可见
	if _, err := fs.Stat(context.Background(), "/报告"); err != nil {
		t.Errorf("新建目录本机应立刻可见：%v", err)
	}
}

func TestMkdirSucceedsWithoutServerSideMkdir(t *testing.T) {
	// 没有 Mkdir 能力的客户端（旧装配、单测假实现）：目录照旧只在本地成立，不报错
	client := newFake()
	fs := newFS(client, drive.SpacePersonal, false)

	if err := fs.Mkdir(context.Background(), "/local-only", 0o755); err != nil {
		t.Fatalf("无服务端目录能力时 Mkdir 不该失败：%v", err)
	}
	if _, err := fs.Stat(context.Background(), "/local-only"); err != nil {
		t.Errorf("本地目录应可见：%v", err)
	}
}

func TestMkdirSurvivesControlPlaneFailure(t *testing.T) {
	client := &dirMarkerClient{fakeClient: newFake(), mkdirErr: errors.New("404 未找到")}
	fs := newFS(client, drive.SpacePersonal, false)

	// 控制面没有 /mkdir（老版本服务端）时，不能让资源管理器弹「拒绝访问」——
	// 退化为「仅本机可见」，代价写在 spacedav.go 的 Mkdir 注释里
	if err := fs.Mkdir(context.Background(), "/degraded", 0o755); err != nil {
		t.Fatalf("服务端失败不应拦本机创建：%v", err)
	}
	if _, err := fs.Stat(context.Background(), "/degraded"); err != nil {
		t.Errorf("降级后本机仍应可见：%v", err)
	}
}

func TestReadOnlyMkdirTouchesNothing(t *testing.T) {
	client := &dirMarkerClient{fakeClient: newFake()}
	fs := newFS(client, drive.SpacePersonal, true)

	if err := fs.Mkdir(context.Background(), "/nope", 0o755); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("只读空间应拒绝建目录，实得 %v", err)
	}
	if len(client.mkdirs) != 0 {
		t.Errorf("只读空间不得在控制面建目录标记：%v", client.mkdirs)
	}
}

func TestRemoveEmptyDirectoryClearsMarker(t *testing.T) {
	client := newFake()
	fs := newFS(client, drive.SpacePersonal, false)

	if err := fs.Mkdir(context.Background(), "/emptydir", 0o755); err != nil {
		t.Fatalf("Mkdir 出错：%v", err)
	}
	if err := fs.RemoveAll(context.Background(), "/emptydir"); err != nil {
		t.Fatalf("删除空目录出错：%v", err)
	}
	if !slices.Contains(client.deleted, "emptydir/") {
		t.Errorf("空目录也应摘掉服务端标记，实删 %v", client.deleted)
	}
}

func TestRemoveFileDoesNotTouchMarkerPath(t *testing.T) {
	client := newFake(drive.Object{Path: "a.txt", Size: 1})
	fs := newFS(client, drive.SpacePersonal, false)

	if err := fs.RemoveAll(context.Background(), "/a.txt"); err != nil {
		t.Fatalf("删文件出错：%v", err)
	}
	// 文件不是目录：不该多发一次「a.txt/」的删除
	if !slices.Equal(client.deleted, []string{"a.txt"}) {
		t.Errorf("删文件只应删该对象本身，实删 %v", client.deleted)
	}
}
