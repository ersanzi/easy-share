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
