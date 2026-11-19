//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMarkVerifiedSyncedKeepsBodyAndPinIntent(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	var fetches atomic.Int64
	m.SetFetchObserver(func(FetchRequest) { fetches.Add(1) })
	allocated := allocatedSize(t, path)
	if err := m.MarkVerifiedCacheSynced(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	h, err := restoreOpen(path, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		t.Fatal(err)
	}
	info, placeholder, err := queryCachePlaceholder(h)
	windows.CloseHandle(h)
	if err != nil || !placeholder || info.InSyncState != 1 || info.PinState == uint32(PinStatePinned) || info.OnDisk == 0 {
		t.Fatalf("确认同步不能提前回收或钉住正文: %+v %v", info, err)
	}
	if fetches.Load() != 0 || allocatedSize(t, path) != allocated {
		t.Fatal("标记同步触发下载或提前回收正文")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("原正文变化: %v", err)
	}
}

func TestMarkVerifiedSyncedRequiresRemoteConfirmation(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	baseline.Confirm = func(context.Context) error { return errors.New("remote version changed") }
	if err := m.MarkVerifiedCacheSynced(context.Background(), baseline); err == nil {
		t.Fatal("未确认云端版本却标记同步")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
		t.Fatal("云端确认失败改变了本机正文")
	}
}
