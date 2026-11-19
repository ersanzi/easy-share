package account

import "strings"

// DisplayName 只用于展示；用户空间身份仍用UserID，不能因昵称变化迁移根。
func (u User) DisplayName() string {
	if nick := strings.TrimSpace(u.NickName); nick != "" {
		return nick
	}
	return strings.TrimSpace(u.UserName)
}
