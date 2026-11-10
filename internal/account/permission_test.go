package account

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// /mine 的应答是扁平结构（五项开关 + adminScope + roles），要确认内嵌 FeatureFlags 被正确摊平。
func TestMyPermissionParsesScopeAndRoles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/permission/mine" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tk" {
			t.Errorf("Authorization=%q", got)
		}
		_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{
			"userId":"7","knowledge":true,"meeting":false,"drive":true,"shared":false,"plugin":true,
			"adminScope":"dept","roles":["车间主管"]}}`))
	}))
	defer server.Close()

	permission, err := New(server.URL).MyPermission(context.Background(), "tk")
	if err != nil {
		t.Fatalf("MyPermission: %v", err)
	}
	if permission.UserID != "7" || !permission.Knowledge || permission.Meeting ||
		!permission.Drive || permission.Shared || !permission.Plugin {
		t.Fatalf("flags parsed wrong: %+v", permission.FeatureFlags)
	}
	if permission.AdminScope != AdminScopeDept {
		t.Errorf("AdminScope=%q, want dept", permission.AdminScope)
	}
	if len(permission.Roles) != 1 || permission.Roles[0] != "车间主管" {
		t.Errorf("Roles=%v", permission.Roles)
	}
}

func TestAdminPermissionMembersParsesRoles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/permission/admin/members" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":[
			{"userId":"8","userName":"zhang","nickName":"张三","deptId":"100","deptName":"车间",
			 "status":"0","knowledge":true,"meeting":true,"drive":false,"shared":false,"plugin":true,
			 "roleIds":["9"],"roleNames":["只读用户"],"adminScope":"none"}]}`))
	}))
	defer server.Close()

	members, err := New(server.URL).AdminPermissionMembers(context.Background(), "tk")
	if err != nil {
		t.Fatalf("AdminPermissionMembers: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members=%d, want 1", len(members))
	}
	member := members[0]
	if member.UserID != "8" || member.DeptName != "车间" || member.Drive {
		t.Fatalf("member parsed wrong: %+v", member)
	}
	if len(member.RoleIDs) != 1 || member.RoleIDs[0] != "9" ||
		len(member.RoleNames) != 1 || member.RoleNames[0] != "只读用户" {
		t.Fatalf("roles parsed wrong: %+v", member)
	}
}

func TestAdminAssignRolesSendsRoleIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method=%s, want PUT", r.Method)
		}
		if r.URL.Path != "/easyshare/permission/admin/members/8/roles" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			RoleIDs []string `json:"roleIds"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("body=%s: %v", body, err)
		}
		if len(payload.RoleIDs) != 2 || payload.RoleIDs[0] != "9" || payload.RoleIDs[1] != "10" {
			t.Errorf("roleIds=%v", payload.RoleIDs)
		}
		_, _ = w.Write([]byte(`{"code":200,"msg":"ok"}`))
	}))
	defer server.Close()

	if err := New(server.URL).AdminAssignRoles(context.Background(), "tk", "8", []string{"9", "10"}); err != nil {
		t.Fatalf("AdminAssignRoles: %v", err)
	}
}

func TestAdminDeleteRoleRejectsNonNumericID(t *testing.T) {
	// 角色 ID 会拼进 URL 路径：非数字必须本地拦掉，不给控制面拼路径的机会
	err := New("http://127.0.0.1:1").AdminDeletePermissionRole(context.Background(), "tk", "9/../system/user")
	if err == nil {
		t.Fatal("expected error for non-numeric role id")
	}
}

func TestAdminSaveRoleRequiresName(t *testing.T) {
	err := New("http://127.0.0.1:1").AdminSavePermissionRole(context.Background(), "tk",
		AdminRole{RoleName: "   "})
	if err == nil {
		t.Fatal("expected error for empty role name")
	}
}

func TestAdminSaveRoleSendsScopeDeptIDs(t *testing.T) {
	// specified 档的范围部门列表要原样透传（雪花 ID 字符串，不转数字）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/permission/admin/roles" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			ScopeDeptIDs []string `json:"scopeDeptIds"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("body=%s: %v", body, err)
		}
		if len(payload.ScopeDeptIDs) != 2 || payload.ScopeDeptIDs[0] != "100" || payload.ScopeDeptIDs[1] != "200" {
			t.Errorf("scopeDeptIds=%v", payload.ScopeDeptIDs)
		}
		_, _ = w.Write([]byte(`{"code":200,"msg":"ok"}`))
	}))
	defer server.Close()

	role := AdminRole{RoleName: "质检管理", AdminScope: AdminScopeSpecified, ScopeDeptIDs: []string{"100", "200"}}
	if err := New(server.URL).AdminSavePermissionRole(context.Background(), "tk", role); err != nil {
		t.Fatalf("AdminSavePermissionRole: %v", err)
	}
}

func TestAdminPermissionRolesParseScopeDeptIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":[
			{"roleId":"9","roleName":"质检管理","adminScope":"specified","scopeDeptIds":["100","200"],
			 "knowledge":true,"meeting":false,"drive":true,"shared":false,"plugin":true,"memberCount":3},
			{"roleId":"10","roleName":"只读用户","adminScope":"none",
			 "knowledge":true,"meeting":true,"drive":true,"shared":true,"plugin":true}]}`))
	}))
	defer server.Close()

	roles, err := New(server.URL).AdminPermissionRoles(context.Background(), "tk")
	if err != nil {
		t.Fatalf("AdminPermissionRoles: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("roles=%d, want 2", len(roles))
	}
	if roles[0].AdminScope != AdminScopeSpecified ||
		len(roles[0].ScopeDeptIDs) != 2 || roles[0].ScopeDeptIDs[0] != "100" {
		t.Fatalf("specified role parsed wrong: %+v", roles[0])
	}
	// 非 specified 档没有 scopeDeptIds 字段，应保持空
	if len(roles[1].ScopeDeptIDs) != 0 {
		t.Fatalf("plain role should have no dept ids: %+v", roles[1])
	}
}
