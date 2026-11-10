package account

import "testing"

func TestDeriveKnowledgeURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"标准带端口", "http://192.168.1.10:8090", "http://192.168.1.10:8000"},
		{"无端口补 8000", "http://kb.corp.local", "http://kb.corp.local:8000"},
		{"localhost", "http://localhost:8090", "http://localhost:8000"},
		{"路径与查询剔除", "http://10.0.0.5:8090/sub/?x=1", "http://10.0.0.5:8000"},
		{"缺 scheme 拒推", "192.168.1.10:8090", ""},
		{"空串", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveKnowledgeURL(tc.in); got != tc.want {
				t.Fatalf("DeriveKnowledgeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsLoopbackURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"127.0.0.1", "http://127.0.0.1:8000", true},
		{"127 段任意地址", "http://127.10.1.2:8000", true},
		{"localhost", "http://localhost:8000", true},
		{"localhost 大写", "HTTP://LocalHost:8000", true},
		{"IPv6 回环", "http://[::1]:8000", true},
		{"内网 IP", "http://192.168.1.10:8000", false},
		{"域名", "https://kb.corp.local", false},
		{"空串", "", false},
		{"非法地址", "not-a-url", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLoopbackURL(tc.in); got != tc.want {
				t.Fatalf("IsLoopbackURL(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSameServerURL(t *testing.T) {
	cases := []struct {
		name        string
		left, right string
		want        bool
	}{
		{"完全相同", "http://10.0.0.5:8000", "http://10.0.0.5:8000", true},
		{"末尾斜杠", "http://10.0.0.5:8000/", "http://10.0.0.5:8000", true},
		{"主机大小写", "http://KB.corp.local:8000", "http://kb.corp.local:8000", true},
		{"http 默认端口", "http://kb.corp.local", "http://kb.corp.local:80", true},
		{"端口不同", "http://10.0.0.5:8000", "http://10.0.0.5:8100", false},
		{"协议不同", "http://10.0.0.5:8000", "https://10.0.0.5:8000", false},
		{"一方非法", "http://10.0.0.5:8000", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameServerURL(tc.left, tc.right); got != tc.want {
				t.Fatalf("SameServerURL(%q, %q) = %v, want %v", tc.left, tc.right, got, tc.want)
			}
		})
	}
}
