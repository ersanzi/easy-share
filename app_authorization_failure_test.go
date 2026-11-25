package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"easyshare/internal/account"
)

func TestBackgroundRefreshPermissionFailureClosesBridgeAdminGates(t *testing.T) {
	for _, mode := range []string{"unavailable", "revoked", "wrong user"} {
		t.Run(mode, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/system/user/getInfo":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
						"user": map[string]string{"userId": "71", "userName": "alice"}, "roles": []string{"superadmin"}}})
				case "/easyshare/permission/mine":
					if mode == "unavailable" {
						http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
						return
					}
					id, enabled := "71", false
					if mode == "wrong user" {
						id, enabled = "72", true
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
						"userId": id, "knowledge": enabled, "meeting": enabled, "drive": enabled, "shared": enabled, "plugin": enabled, "adminScope": "none"}})
				default:
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": ""})
				}
			}))
			defer remote.Close()
			session := &account.Session{Token: "fixture", User: account.User{UserID: "71"}, Roles: []string{"superadmin"}}
			a := &App{logger: log.New(io.Discard, "", 0), accountSession: session, features: account.AllFeatures(), adminScope: account.AdminScopeAll}
			a.config.PlatformBaseURL = remote.URL
			err := a.refreshAccountSession(context.Background(), account.New(remote.URL), session)
			if mode != "revoked" && err == nil {
				t.Fatal("权限失败未上报")
			}
			if mode == "revoked" && err != nil {
				t.Fatal(err)
			}
			if a.CurrentUser().IsAdmin || a.features != (account.FeatureFlags{UserID: a.features.UserID}) || account.HasAdminEntry(a.adminScope) {
				t.Fatal("后台刷新后仍留有旧授权")
			}
			if _, _, err := a.adminSession(); err == nil {
				t.Fatal("超管 bridge 入口未收口")
			}
			if _, _, err := a.permissionAdminSession(); err == nil {
				t.Fatal("权限管理 bridge 入口未收口")
			}
		})
	}
}

func TestProfilePermissionFailureNeverEnablesFeatures(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
	}))
	defer remote.Close()
	a := &App{logger: log.New(io.Discard, "", 0)}
	flags, scope, _, err := a.readAccountProfile(context.Background(), account.New(remote.URL), &account.Session{Token: "fixture"})
	if err == nil {
		t.Fatal("权限接口失败未上报")
	}
	if flags != (account.FeatureFlags{}) || scope != account.AdminScopeNone {
		t.Fatal("权限服务失败仍产生功能或管理授权")
	}
}

func TestProfileIdentityFailureRevokesCachedPrivileges(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
	}))
	defer remote.Close()
	session := &account.Session{Token: "fixture", User: account.User{UserID: "71"}, Roles: []string{"superadmin"}}
	a := &App{logger: log.New(io.Discard, "", 0), accountSession: session,
		features: account.AllFeatures(), adminScope: account.AdminScopeAll}
	if a.refreshAccountSession(context.Background(), account.New(remote.URL), session) == nil {
		t.Fatal("刷新失败未上报")
	}
	if a.features != (account.FeatureFlags{}) || account.HasAdminEntry(a.adminScope) || a.CurrentUser().IsAdmin {
		t.Fatal("身份刷新失败后仍沿用旧管理员和功能授权")
	}
}

func TestLateProfileFailureCannotRevokeNewAccount(t *testing.T) {
	old := &account.Session{Token: "old-fixture", User: account.User{UserID: "71"}}
	next := &account.Session{Token: "next-fixture", User: account.User{UserID: "72"}}
	a := &App{logger: log.New(io.Discard, "", 0), accountSession: old, features: account.AllFeatures()}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.accountMu.Lock()
		a.accountSession = next
		a.accountRevision++
		a.accountMu.Unlock()
		http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
	}))
	defer remote.Close()
	_ = a.refreshAccountSession(context.Background(), account.New(remote.URL), old)
	if a.features != account.AllFeatures() || a.accountSession != next {
		t.Fatal("旧请求失败覆盖了新账号权限")
	}
}

func TestLateLogoutSnapshotCannotClearNewAccountIncludingNilSnapshot(t *testing.T) {
	current := &account.Session{Token: "current-fixture", User: account.User{UserID: "72"}}
	a := &App{accountSession: current, features: account.AllFeatures()}
	for _, stale := range []*account.Session{nil, {Token: "old-fixture", User: account.User{UserID: "71"}}} {
		if a.clearAccountSessionIf(stale) || a.accountSession != current || a.features != account.AllFeatures() {
			t.Fatal("旧注销快照清理了新账号")
		}
	}
}

func TestStaleUnauthorizedRefreshCannotInvalidateNewerSameSession(t *testing.T) {
	session := &account.Session{Token: "fixture", User: account.User{UserID: "71"}}
	a := &App{logger: log.New(io.Discard, "", 0), accountSession: session, features: account.AllFeatures()}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 后发的刷新已经成功，旧401才到达；错误必须在这一层被丢弃。
		revision, _ := a.beginAccountProfileRequest(session)
		a.commitAccountProfile(session, session, revision, account.AllFeatures(), account.AdminScopeAll)
		http.Error(w, "expired fixture", http.StatusUnauthorized)
	}))
	defer remote.Close()
	if err := a.refreshAccountSession(context.Background(), account.New(remote.URL), session); err != nil {
		t.Fatal("迟到401仍被交给 validateAccountSession 清理当前会话")
	}
	if a.features != account.AllFeatures() || a.accountSession != session {
		t.Fatal("迟到401覆盖较新刷新")
	}
}
