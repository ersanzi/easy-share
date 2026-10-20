package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 账号会话落盘：桌面端重启后恢复登录态，避免每次打开都要求重新登录。
// 只保存令牌、到期时间与展示所需的用户信息，不保存密码。
// 文件位于用户级 AppData 目录，权限 0600；退出登录时删除。
const sessionFileVersion = 1

type storedSession struct {
	Version     int       `json:"version"`
	Token       string    `json:"token"`
	ExpireIn    int64     `json:"expireIn"`
	ExpiresAt   time.Time `json:"expiresAt"`
	User        User      `json:"user"`
	Roles       []string  `json:"roles"`
	Permissions []string  `json:"permissions"`
}

func SaveSession(path string, session *Session) error {
	if session == nil || session.Token == "" {
		return errors.New("空账号会话")
	}
	payload, err := json.MarshalIndent(storedSession{
		Version:     sessionFileVersion,
		Token:       session.Token,
		ExpireIn:    session.ExpireIn,
		ExpiresAt:   session.ExpiresAt,
		User:        session.User,
		Roles:       session.Roles,
		Permissions: session.Permissions,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("编码账号会话：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建账号会话目录：%w", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("写入账号会话：%w", err)
	}
	return nil
}

func LoadSession(path string) (*Session, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var stored storedSession
	if err := json.Unmarshal(payload, &stored); err != nil {
		return nil, fmt.Errorf("解析账号会话：%w", err)
	}
	if stored.Version != sessionFileVersion {
		return nil, fmt.Errorf("账号会话版本不支持：%d", stored.Version)
	}
	if stored.Token == "" {
		return nil, errors.New("账号会话缺少令牌")
	}
	return &Session{
		Token:       stored.Token,
		ExpireIn:    stored.ExpireIn,
		ExpiresAt:   stored.ExpiresAt,
		User:        stored.User,
		Roles:       stored.Roles,
		Permissions: stored.Permissions,
	}, nil
}

func RemoveSession(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除账号会话：%w", err)
	}
	return nil
}
