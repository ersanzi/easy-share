//go:build windows

package main

import (
	"easyshare/internal/account"
	"easyshare/internal/namespace"
)

// 同账号重标只更新Shell展示，不重建CFAPI根、会话或pending。
func (a *App) refreshNativeSpaceLabel() {
	// 与同步/删除回调保持同一锁序：先挂载状态，再账号；不能持账号锁等待同步。
	a.cfapi.mu.Lock()
	defer a.cfapi.mu.Unlock()
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	session := a.accountSession
	if session == nil {
		return
	}
	if a.cfapi.personal == nil || a.cfapi.session != session || a.cfapi.lifetime == nil || a.cfapi.lifetime.ctx.Err() != nil {
		return
	}
	name := session.User.DisplayName()
	if a.cfapi.displayName == name {
		return
	}
	entries := namespace.SpaceEntries(namespace.IconFromBuild(), []namespace.SpaceMount{{Kind: "personal", TargetPath: a.cfapi.personalRoot, UserID: session.User.UserID, DisplayName: name, Subtitle: a.cfapi.subtitle}})
	if err := namespace.Register(entries); err != nil {
		a.logger.Printf("cfapi: 更新网盘显示名失败: %v", err)
		return
	}
	a.cfapi.displayName = name
}

func sameAccountSession(current, expected *account.Session) bool {
	return current != nil && current == expected
}
