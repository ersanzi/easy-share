package drive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 候选边界透传关键词与字符串ID，不在客户端放宽授权。
func TestFileMemberCandidatesContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/easyshare/file-member/100/candidates" || r.URL.Query().Get("keyword") != "张 & 三" || r.URL.Query().Get("limit") != "20" {
			t.Errorf("错误候选请求: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":[{"memberType":"user","memberId":"9007199254740993","memberName":"张三","grantable":true}]}`))
	}))
	defer server.Close()
	rows, err := New(server.URL).FileMemberCandidates(context.Background(), "token", 100, "张 & 三", 20)
	if err != nil || len(rows) != 1 || rows[0].MemberID != "9007199254740993" {
		t.Fatalf("候选ID丢失: %#v %v", rows, err)
	}
}
