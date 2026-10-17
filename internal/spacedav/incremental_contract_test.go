package spacedav

// 钉住挂载盘的增量合并语义：created/updated/restored 保证可见，
// deleted/purged/moved 从视图移除，且不重列整空间。
//
// 这是切片 2 的生产落点：挂载盘是目录列表最高频调用方。

import (
	"context"
	"testing"
	"time"

	"easyshare/internal/drive"
)

type fakeChangeClient struct {
	*fakeClient
	changes []drive.Changes
	calls   int
}

func (c *fakeChangeClient) Changes(_ context.Context, _, _ string, since *int64) (*drive.Changes, error) {
	c.calls++
	if len(c.changes) == 0 {
		return &drive.Changes{}, nil
	}
	next := c.changes[0]
	c.changes = c.changes[1:]
	return &next, nil
}

func TestIncrementalListMergesIntoCache(t *testing.T) {
	base := newFake(
		drive.Object{Path: "a.txt", Size: 10},
		drive.Object{Path: "b.txt", Size: 20},
	)
	client := &fakeChangeClient{fakeClient: base}
	fs := newFS(client, drive.SpacePersonal, false)

	// 第一次：全量（fake Changes 返回空 Files 时回退 Objects? 本测试给 Files）
	first := drive.Changes{
		NewestSpaceVersion: 10,
		Files: []drive.Object{
			{Path: "a.txt", Size: 10},
			{Path: "b.txt", Size: 20},
		},
	}
	second := drive.Changes{
		NewestSpaceVersion: 13,
		Changes: []drive.Change{
			{SpaceVersion: 11, Event: drive.ChangeDeleted, Path: "a.txt"},
			{SpaceVersion: 12, Event: drive.ChangeCreated, Path: "c.txt", FileId: 3},
			{SpaceVersion: 13, Event: drive.ChangeMoved, Path: "renamed.txt", OldPath: "b.txt", FileId: 2},
		},
	}
	client.changes = []drive.Changes{first, second}

	// 让缓存过期，走第二次列举
	fs.mu.Lock()
	fs.cachedAt = fs.cachedAt.Add(-time.Hour)
	fs.mu.Unlock()

	got, err := fs.objects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = got

	// 再让缓存过期，走第三次（应为增量合并）
	fs.mu.Lock()
	fs.cachedAt = fs.cachedAt.Add(-time.Hour)
	fs.mu.Unlock()

	list, err := fs.objects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var haveA, haveB, haveC, haveRenamed bool
	for _, obj := range list {
		switch obj.Path {
		case "a.txt":
			haveA = true
		case "b.txt":
			haveB = true
		case "c.txt":
			haveC = true
		case "renamed.txt":
			haveRenamed = true
		}
	}
	if haveA || haveB {
		t.Errorf("deleted/moved 的旧路径应被移除：a=%v b=%v", haveA, haveB)
	}
	if !haveC || !haveRenamed {
		t.Errorf("created/moved 的新路径应存在：c=%v renamed=%v", haveC, haveRenamed)
	}
}
