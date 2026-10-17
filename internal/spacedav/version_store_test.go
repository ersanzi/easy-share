package spacedav

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easyshare/internal/drive"
)

// diskVersionStoreContract 钉住跨进程持久化的核心契约：
// 写入后新建一个同路径 store（模拟进程重启）仍能读到版本。
type diskClient struct {
	*fakeClient
	responses []drive.Changes
}

func (c *diskClient) Changes(_ context.Context, _, _ string, _ *int64) (*drive.Changes, error) {
	if len(c.responses) == 0 {
		return &drive.Changes{}, nil
	}
	next := c.responses[0]
	c.responses = c.responses[1:]
	return &next, nil
}

func TestDiskVersionStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "space-versions.json")
	store := NewDiskVersionStore(path)
	if got := store.Load(drive.SpacePersonal); got != 0 {
		t.Fatalf("初始版本应为 0，实得 %d", got)
	}
	if err := store.Save(drive.SpacePersonal, 42); err != nil {
		t.Fatalf("保存失败：%v", err)
	}

	// 模拟重启：新建 store，不再引用旧对象
	restarted := NewDiskVersionStore(path)
	if got := restarted.Load(drive.SpacePersonal); got != 42 {
		t.Fatalf("重启后版本应为 42，实得 %d", got)
	}
	if got := restarted.Load(drive.SpaceShared); got != 0 {
		t.Fatalf("共享空间未保存过，应为 0，实得 %d", got)
	}

	// 两个空间的版本互不覆盖
	if err := restarted.Save(drive.SpaceShared, 7); err != nil {
		t.Fatal(err)
	}
	if restarted.Load(drive.SpacePersonal) != 42 || restarted.Load(drive.SpaceShared) != 7 {
		t.Fatalf("多空间版本被覆盖：personal=%d shared=%d",
			restarted.Load(drive.SpacePersonal), restarted.Load(drive.SpaceShared))
	}
}

func TestFSUsesDiskVersionAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "space-versions.json")

	client := &diskClient{fakeClient: newFake()}
	client.responses = append(client.responses, drive.Changes{
		NewestSpaceVersion: 10,
		Files:              []drive.Object{{Path: "a.txt", Size: 10}},
	})

	first := New(Options{
		Client: client, Space: drive.SpacePersonal,
		Token: loggedIn, Versions: NewDiskVersionStore(path),
	})
	// 强制缓存过期
	first.mu.Lock()
	first.cachedAt = first.cachedAt.Add(-time.Hour)
	first.mu.Unlock()
	if _, err := first.objects(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first.lastVersion != 10 {
		t.Fatalf("first.lastVersion = %d, want 10", first.lastVersion)
	}

	// 模拟重启：新 FS、同一路径
	restartedClient := &diskClient{fakeClient: newFake()}
	restartedClient.responses = append(restartedClient.responses, drive.Changes{
		NewestSpaceVersion: 11,
		Changes: []drive.Change{
			{SpaceVersion: 11, Event: drive.ChangeCreated, Path: "b.txt", FileId: 2},
		},
	})
	restarted := New(Options{
		Client: restartedClient, Space: drive.SpacePersonal,
		Token: loggedIn, Versions: NewDiskVersionStore(path),
	})
	restarted.mu.Lock()
	restarted.cachedAt = restarted.cachedAt.Add(-time.Hour)
	restarted.mu.Unlock()
	list, err := restarted.objects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if restarted.lastVersion != 11 {
		t.Fatalf("restarted.lastVersion = %d, want 11", restarted.lastVersion)
	}
	var haveB bool
	for _, obj := range list {
		if obj.Path == "b.txt" {
			haveB = true
		}
	}
	if !haveB {
		t.Fatalf("重启后应能从增量里看到 b.txt，实得 %+v", list)
	}
	_ = os.ReadFile
}
