package spacedav

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"easyshare/internal/drive"
)

func TestSharedListingRechecksPermissionInsteadOfCachedDelta(t *testing.T) {
	initial := []drive.Object{{Path: "private-secret.txt", Size: 10}}
	client := &fakeChangeClient{fakeClient: newFake(initial...), changes: []drive.Changes{
		{NewestSpaceVersion: 1, Files: initial}, {NewestSpaceVersion: 1, Changes: []drive.Change{}},
	}}
	fs := newFS(client, drive.SpaceShared, false)
	if got, err := fs.objects(context.Background()); err != nil || len(got) != 1 {
		t.Fatal("首次授权列举失败")
	}
	// 文件未变化，只有权限撤销；增量日志为空，而全量授权清单已为空。
	client.objects = []drive.Object{}
	fs.cachedAt = time.Now().Add(-time.Hour)
	if got, err := fs.objects(context.Background()); err != nil || len(got) != 0 {
		t.Fatal("撤权后过期缓存仍返回旧文件")
	}
}

func TestSharedWarmCacheDoesNotAuthorizeWhenControlPlaneFails(t *testing.T) {
	client := newFake(drive.Object{Path: "private-secret.txt", Size: 10})
	fs := newFS(client, drive.SpaceShared, false)
	if _, err := fs.objects(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.listErr = os.ErrPermission
	if got, err := fs.objects(context.Background()); !errors.Is(err, os.ErrPermission) || len(got) != 0 {
		t.Fatal("远端拒绝时热缓存仍授权")
	}
}

func TestPersonalListingDoesNotShareCacheBetweenTokens(t *testing.T) {
	token := "first-fixture"
	client := newFake(drive.Object{Path: "first-private.txt", Size: 10})
	fs := New(Options{Client: client, Space: drive.SpacePersonal, Token: func() string { return token }})
	if _, err := fs.objects(context.Background()); err != nil {
		t.Fatal(err)
	}
	token = "second-fixture"
	client.objects = []drive.Object{}
	if got, err := fs.objects(context.Background()); err != nil || len(got) != 0 {
		t.Fatal("换账号后仍读取前一账号热缓存")
	}
}
