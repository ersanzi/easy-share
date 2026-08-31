package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionIsAdmin(t *testing.T) {
	cases := []struct {
		name    string
		session *Session
		want    bool
	}{
		{"nil 会话", nil, false},
		{"空会话（取用户信息失败的降级路径）", &Session{}, false},
		{"superadmin 角色", &Session{Roles: []string{"superadmin"}}, true},
		{"通配权限但角色名自定义", &Session{Roles: []string{"ops"}, Permissions: []string{"*:*:*"}}, true},
		{"普通用户", &Session{Roles: []string{"test1"}, Permissions: []string{"system:user:list"}}, false},
		// 防前缀误判：不能把 superadmin2 之类的名字当成管理员。
		{"角色名仅前缀相同", &Session{Roles: []string{"superadmin2"}}, false},
		// 防通配误判：细粒度通配不等于全局通配。
		{"模块级通配", &Session{Permissions: []string{"system:*:*"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.session.IsAdmin(); got != tc.want {
				t.Fatalf("IsAdmin()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoginParsesRolesAndPermissions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/login":
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"access_token":"tk","expire_in":720}}`))
		case "/system/user/getInfo":
			if got := r.Header.Get("Authorization"); got != "Bearer tk" {
				t.Errorf("Authorization=%q, want Bearer tk", got)
			}
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{
				"user":{"userId":"1761100000000000001","userName":"admin","nickName":"管理员"},
				"roles":["superadmin"],
				"permissions":["*:*:*"]}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	session, err := New(server.URL).Login(context.Background(), "admin", "admin123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.User.UserID != "1761100000000000001" {
		t.Errorf("UserID=%q", session.User.UserID)
	}
	if !session.IsAdmin() {
		t.Errorf("IsAdmin()=false, want true (roles=%v perms=%v)", session.Roles, session.Permissions)
	}
}

// getInfo 失败时必须降级为「已登录但非管理员」，绝不能因缺角色信息而放开入口。
func TestLoginDegradesToNonAdminWhenUserInfoFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/auth/login" {
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"access_token":"tk","expire_in":720}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":500,"msg":"内部错误"}`))
	}))
	defer server.Close()

	session, err := New(server.URL).Login(context.Background(), "admin", "admin123")
	if err != nil {
		t.Fatalf("Login should degrade, got error: %v", err)
	}
	if session.Token != "tk" {
		t.Errorf("Token=%q, want tk", session.Token)
	}
	if session.IsAdmin() {
		t.Error("IsAdmin()=true after getInfo failure; 缺角色信息时必须视为非管理员")
	}
}

// RuoYi 把业务错误放在 HTTP 200 的 body 里，登录失败必须按 code 判定。
func TestLoginFailsOnBusinessErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "msg": "密码输入错误1次"})
	}))
	defer server.Close()

	if _, err := New(server.URL).Login(context.Background(), "admin", "wrong"); err == nil {
		t.Fatal("Login should fail on code=401 despite HTTP 200")
	}
}
