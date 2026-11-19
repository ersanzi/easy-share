package main

import (
	"io"
	"log"
	"path/filepath"
	"testing"

	"easyshare/internal/account"
)

func TestProfileRefreshKeepsSessionObjectAndUserIdentity(t *testing.T) {
	original := &account.Session{Token: "fixture", User: account.User{UserID: "71", UserName: "alice", NickName: "旧名字"}}
	a := &App{accountSession: original}
	revision, valid := a.beginAccountProfileRequest(original)
	fresh := &account.Session{User: account.User{UserID: "71", UserName: "alice", NickName: "新名字"}}
	if !valid || !a.commitAccountProfile(original, fresh, revision, account.AllFeatures(), "all") {
		t.Fatal("同账号资料更新失败")
	}
	if a.accountSession != original || original.User.NickName != "新名字" || original.User.UserID != "71" {
		t.Fatal("改昵称替换了挂载会话或稳定身份")
	}
}

func TestProfileRefreshRejectsLateAccountAndSameUserRevision(t *testing.T) {
	original := &account.Session{Token: "fixture", User: account.User{UserID: "71", NickName: "原名字"}}
	a := &App{accountSession: original}
	first, _ := a.beginAccountProfileRequest(original)
	second, _ := a.beginAccountProfileRequest(original)
	fresh := &account.Session{User: account.User{UserID: "71", NickName: "迟到名字"}}
	if a.commitAccountProfile(original, fresh, first, account.AllFeatures(), "") {
		t.Fatal("同账号迟到资料覆盖较新请求")
	}
	a.accountSession = &account.Session{Token: "fixture", User: account.User{UserID: "72"}}
	if a.commitAccountProfile(original, fresh, second, account.AllFeatures(), "") {
		t.Fatal("同token新会话采纳旧账号资料")
	}
	a.accountSession = nil
	if a.commitAccountProfile(original, fresh, second, account.AllFeatures(), "") {
		t.Fatal("注销后采纳旧资料")
	}
}

func TestSaveStaleProfileCannotRestoreLoggedOutSession(t *testing.T) {
	dir := t.TempDir()
	a := &App{configPath: filepath.Join(dir, "config.json"), logger: log.New(io.Discard, "", 0)}
	a.saveAccountSession(&account.Session{Token: "fixture", User: account.User{UserID: "71"}})
	if _, err := account.LoadSession(a.accountSessionPath()); err == nil {
		t.Fatal("注销后的迟到资料重新保存了登录会话")
	}
}
