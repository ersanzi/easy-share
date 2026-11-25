package main

import (
	"context"
	"fmt"
	"time"

	"easyshare/internal/account"
)

// 每个资料请求都带会话对象和请求世代；同token的新登录也不能采纳旧结果。
func (a *App) beginAccountProfileRequest(session *account.Session) (uint64, bool) {
	a.accountMu.Lock()
	defer a.accountMu.Unlock()
	if session == nil || a.accountSession != session {
		return 0, false
	}
	a.accountRevision++
	return a.accountRevision, true
}

func (a *App) commitAccountProfile(session, fresh *account.Session, revision uint64, flags account.FeatureFlags, scope string) bool {
	a.accountMu.Lock()
	defer a.accountMu.Unlock()
	if a.accountSession != session || a.accountRevision != revision || fresh.User.UserID != session.User.UserID {
		return false
	}
	// 不替换session对象：CFAPI回调的授权仍绑定原会话，昵称变化不重建根。
	session.User = fresh.User
	session.Roles = fresh.Roles
	session.Permissions = fresh.Permissions
	a.features, a.adminScope = flags, scope
	return true
}

func (a *App) refreshAccountSession(ctx context.Context, client *account.Client, session *account.Session) error {
	revision, valid := a.beginAccountProfileRequest(session)
	if !valid {
		return nil
	}
	fresh, err := client.SessionInfo(ctx, session.Token)
	if err == nil && (fresh.User.UserID == "" || fresh.User.UserID != session.User.UserID) {
		err = staleProfileError()
	}
	if err != nil {
		// 离线保留身份供展示，但不能保留上一轮生效的管理员或功能授权。
		a.accountMu.Lock()
		changed := a.accountSession == session && a.accountRevision == revision
		if changed {
			a.features = account.FeatureFlags{}
			a.adminScope = account.AdminScopeNone
			session.Roles = nil
			session.Permissions = nil
		}
		a.accountMu.Unlock()
		if changed {
			a.notifyAccountChanged()
		} else {
			// 新请求已完成或已换人；迟到401也不能触发外层注销当前会话。
			return nil
		}
		return err
	}
	flags, scope, avatar, permissionErr := a.readAccountProfile(ctx, client, fresh)
	fresh.User.Avatar = avatar
	if !a.commitAccountProfile(session, fresh, revision, flags, scope) {
		return nil
	}
	a.saveAccountSession(session)
	a.notifyAccountChanged()
	return permissionErr
}

func (a *App) startAccountProfileRefresh() {
	a.accountRefreshOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if a.isQuitting() {
					return
				}
				if a.cloudAvailable() {
					a.validateAccountSession()
				}
			}
		}()
	})
}

func staleProfileError() error { return fmt.Errorf("账号状态已变化，请在当前账号重试") }
