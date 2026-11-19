package account

import "testing"

func TestDisplayNameNickFirstTrimAndAccountFallback(t *testing.T) {
	for _, test := range []struct {
		user User
		want string
	}{
		{User{NickName: "  云盘测试  ", UserName: "login"}, "云盘测试"},
		{User{NickName: " \t ", UserName: " login "}, "login"},
		{User{UserName: " login "}, "login"},
	} {
		if got := test.user.DisplayName(); got != test.want {
			t.Fatalf("display=%q want=%q", got, test.want)
		}
	}
}
