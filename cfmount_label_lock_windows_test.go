//go:build windows

package main

import (
	"testing"
	"time"

	"easyshare/internal/account"
)

func TestLabelWaitingForSyncDoesNotHoldAccountLock(t *testing.T) {
	a := &App{accountSession: &account.Session{User: account.User{UserID: "42", NickName: "fixture"}}}
	a.cfapi.mu.Lock()
	done := make(chan struct{})
	go func() { a.refreshNativeSpaceLabel(); close(done) }()
	// 模拟同步占着挂载锁、标签刷新等待；账号写入仍必须能完成。
	time.Sleep(20 * time.Millisecond)
	written := make(chan struct{})
	go func() { a.accountMu.Lock(); a.accountRevision++; a.accountMu.Unlock(); close(written) }()
	select {
	case <-written:
	case <-time.After(time.Second):
		a.cfapi.mu.Unlock()
		<-done
		<-written
		t.Fatal("昵称刷新持账号读锁等待同步，造成账号写入/同步读锁互相等待")
	}
	a.cfapi.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("标签刷新未完成")
	}
}
