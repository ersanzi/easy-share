//go:build windows

package cfapi

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestQuerySyncStatusDoesNotRecallCloudBody(t *testing.T) {
	m, _, _, baseline := cacheTestSetup(t)
	var fetches atomic.Int64
	m.SetFetchObserver(func(FetchRequest) { fetches.Add(1) })
	ordinary, err := m.QueryPlaceholderStatus("file.bin")
	if err != nil || ordinary.Placeholder {
		t.Fatalf("普通未确认文件被当为云占位符: %+v %v", ordinary, err)
	}
	if err := m.MarkVerifiedCacheSynced(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	local, err := m.QueryPlaceholderStatus("file.bin")
	if err != nil || !local.Placeholder || !local.FullContent || local.InSyncState != 1 {
		t.Fatalf("完整已确认正文状态不符: %+v %v", local, err)
	}
	if _, err := m.ReleaseVerifiedCache(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	online, err := m.QueryPlaceholderStatus("file.bin")
	if err != nil || !online.Placeholder || online.FullContent || online.OnDisk != 0 || online.Modified != 0 {
		t.Fatalf("仅云端状态不符: %+v %v", online, err)
	}
	if fetches.Load() != 0 {
		t.Fatal("状态查询召回了文件正文")
	}
}

func TestQuerySyncStatusRejectsScopeEscape(t *testing.T) {
	m, _, _, _ := cacheTestSetup(t)
	for _, rel := range []string{"", ".", "../file", "folder/../../file", `folder\file`, "file ", "file.", "C:/file"} {
		if _, err := m.QueryPlaceholderStatus(rel); err == nil {
			t.Fatalf("状态查询允许不安全路径: %q", rel)
		}
	}
}

func TestQuerySyncStatusEmptyTrustedPlaceholder(t *testing.T) {
	m, _, _, baseline := cacheTestSetup(t, []byte{})
	if err := m.MarkVerifiedCacheSynced(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	state, err := m.QueryPlaceholderStatus("file.bin")
	if err != nil || !state.Placeholder || !state.FullContent || state.Size != 0 || state.InSyncState != 1 {
		t.Fatalf("空文件必须结合可信占位身份: %+v %v", state, err)
	}
}

func TestDeletionStatusKeepsOriginalIdentityAfterRemoteIndexRemoval(t *testing.T) {
	m, _, _, baseline := cacheTestSetup(t)
	if err := m.MarkVerifiedCacheSynced(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.index = map[string]string{}
	m.versions = map[string]int64{}
	m.mu.Unlock()
	if _, err := m.QueryPlaceholderStatus("file.bin"); err == nil {
		t.Fatal("普通状态查询接受失去索引的文件")
	}
	state, err := m.QueryDeletionStatus("file.bin")
	if err != nil || !state.Placeholder || state.RemoteID != baseline.RemoteFileID || state.LocalIdentity != baseline.LocalIdentity || !state.FullContent {
		t.Fatalf("删除收尾未返回原身份元数据: %+v %v", state, err)
	}
}
