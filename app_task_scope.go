package main

import (
	"easyshare/internal/account"
	"easyshare/internal/desktop"
)

// taskClient 将当前会话固定在本次 IPC 请求上；后台旧任务不能借用新账号。
func (a *App) taskClient() (*desktop.Client, *account.Session, error) {
	client, err := a.coreClient()
	if err != nil {
		return nil, nil, err
	}
	a.accountMu.RLock()
	session := a.accountSession
	token := ""
	if session != nil {
		token = session.Token
	}
	a.accountMu.RUnlock()
	return client.WithAccountToken(token), session, nil
}

func (a *App) taskSessionCurrent(session *account.Session) bool {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	return a.accountSession == session
}

// 调用者持有 accountMu；换号立即打断旧账号事件订阅。
func (a *App) cancelTaskEventsLocked() {
	if a.taskEventsCancel != nil {
		a.taskEventsCancel()
		a.taskEventsCancel = nil
	}
}

// 核验与本机打开之间不允许换号；失败时不调用操作系统。
func (a *App) openTaskPath(path string, open func(string) error) error {
	client, session, err := a.taskClient()
	if err != nil {
		return err
	}
	if err := client.CheckTaskPath(a.ctx, path); err != nil {
		return err
	}
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if a.accountSession != session {
		return staleProfileError()
	}
	return open(path)
}
