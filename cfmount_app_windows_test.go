//go:build windows

package main

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/cfmount"
)

func TestNativeSpaceIdentityIsolation(t *testing.T) {
	base := nativeSpaceIdentity("https://server", "1", "personal")
	for _, other := range []string{nativeSpaceIdentity("https://other", "1", "personal"), nativeSpaceIdentity("https://server", "2", "personal"), nativeSpaceIdentity("https://server", "1", "shared")} {
		if base == other {
			t.Fatal("服务器/账号/空间未隔离")
		}
	}
	if base != nativeSpaceIdentity("https://server/", "1", "personal") {
		t.Fatal("尾斜杠不应改变服务器身份")
	}
}

func TestBoundSessionTokenRejectsAccountSwitch(t *testing.T) {
	a := &App{}
	a.config.PlatformBaseURL = "https://server"
	first := &account.Session{Token: "first"}
	a.accountSession = first
	token := a.boundSessionToken(first, "https://server")
	if token() != "first" {
		t.Fatal("会话不可用")
	}
	a.accountSession = &account.Session{Token: "second"}
	if token() != "" {
		t.Fatal("旧挂载读取到新会话令牌")
	}
	a.accountSession = nil
	if token() != "" {
		t.Fatal("登出未失效")
	}
}

func TestNativeOpenUnconnectedSpaceFailsClosed(t *testing.T) {
	a := &App{logger: log.New(io.Discard, "", 0)}
	if err := a.OpenCfapiFolder(); err == nil {
		t.Fatal("未挂载时不能启动资源管理器")
	}
	a.SetDropSpace("shared")
	if handled, err := a.openNativeCurrentSpace(); !handled || err == nil {
		t.Fatal("共享空间不能回退为个人云盘或普通目录")
	}
}

func TestNativeCFAPIGateFailsClosed(t *testing.T) {
	list, reconcile, err := prepareNativeCFAPI(context.Background(), cfmount.Config{}, "", "")
	if err == nil || list != nil || reconcile != nil {
		t.Fatal("未完成安全适配却发布了入口能力")
	}
}

// These tests deliberately exercise only the lifecycle barrier: no production
// namespace registration or Cloud Files mount is performed.
func TestNativeMountRejectsMissingUserID(t *testing.T) {
	for _, id := range []string{"", " \t\r\n"} {
		session := &account.Session{Token: "valid-token"}
		session.User.UserID = id
		if err := validateNativeMountIdentity(session); err == nil {
			t.Fatal("missing user ID must fail closed even with a valid token")
		}
	}
	session := &account.Session{Token: "valid-token"}
	session.User.UserID = "user-1"
	if err := validateNativeMountIdentity(session); err != nil {
		t.Fatal(err)
	}
}

func TestNativeUnmountCancelsBeforeWaitingForReconcileLock(t *testing.T) {
	lifetime := newNativeMountLifetime()
	defer lifetime.cancel()
	m := &cfapiMounts{lifetime: lifetime}
	// Model reconcile holding the publication lock until its network request
	// observes cancellation. Taking mu before cancelling would deadlock here.
	m.mu.Lock()
	request, cancel := nativeLifetimeContext(context.Background(), lifetime.ctx)
	defer cancel()
	finished := make(chan struct{})
	go func() {
		<-request.Done()
		m.mu.Unlock()
	}()
	go func() {
		if m.cancelBeforeLock(lifetime) {
			m.mu.Unlock()
			m.lifecycleMu.Unlock()
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("unmount waited for reconcile lock before cancelling its request")
	}
}

func TestNativeOldGenerationCannotCancelNewLifetime(t *testing.T) {
	old := newNativeMountLifetime()
	defer old.cancel()
	current := newNativeMountLifetime()
	defer current.cancel()
	m := &cfapiMounts{lifetime: current}
	if m.cancelBeforeLock(old) {
		m.mu.Unlock()
		m.lifecycleMu.Unlock()
		t.Fatal("stale mount operation crossed lifecycle barrier")
	}
	if current.ctx.Err() != nil {
		t.Fatal("old generation cancelled the new mount")
	}
	// Failure must release lifecycleMu too.
	if !m.cancelBeforeLock(current) {
		t.Fatal("current generation rejected")
	}
	m.mu.Unlock()
	m.lifecycleMu.Unlock()
	if current.ctx.Err() != context.Canceled {
		t.Fatal("current generation was not cancelled")
	}
}
