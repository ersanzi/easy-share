package account_test

// 活栈探针：逐个调用账号/空间接口，找出**哪些响应的 JSON 类型与 Go 结构体不符**。
//
// 背景：2026-10-16 真机联调发现 drive.Object.FileId 解析失败——
// RuoYi 的 JacksonConfig 给所有 Long 注册 BigNumberSerializer，
// **超出 JS 安全整数（2^53-1）时序列化成字符串**。雪花 ID 与配额字节数必然超范围。
// 而 Go 的 encoding/json 遇到类型不符会**让整个 Unmarshal 失败**（不是跳过该字段），
// 于是一个字段的形态问题会打掉整份响应。
//
// 本探针不预设结论：逐个调，把失败的接口与错误原样列出来，
// 从而得到**完整清单**而不是靠逐个 grep 结构体猜。

import (
	"context"
	"os"
	"testing"

	"easyshare/internal/account"
)

func TestLiveProbeAccountAndSpaceJSON(t *testing.T) {
	if os.Getenv("EASYSHARE_LIVE_DRIVE") == "" {
		t.Skip("未设 EASYSHARE_LIVE_DRIVE，跳过活栈探针")
	}
	base := os.Getenv("EASYSHARE_LIVE_BASE")
	if base == "" {
		base = "http://127.0.0.1:8090"
	}
	user := os.Getenv("EASYSHARE_LIVE_USER_A")
	pass := os.Getenv("EASYSHARE_LIVE_PASS_A")
	client := account.New(base)
	ctx := context.Background()

	session, err := client.Login(ctx, user, pass)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	token := session.Token

	type probe struct {
		name string
		call func() error
	}
	probes := []probe{
		{"MySpaces", func() error {
			_, err := client.MySpaces(ctx, token)
			return err
		}},
		{"ListSpaces", func() error {
			_, err := client.ListSpaces(ctx, token)
			return err
		}},
		{"GetCapacity", func() error {
			_, err := client.GetCapacity(ctx, token)
			return err
		}},
		{"ListUsers", func() error {
			_, err := client.ListUsers(ctx, token, 1, 5)
			return err
		}},
		{"SharedMembers", func() error {
			_, err := client.SharedMembers(ctx, token)
			return err
		}},
		{"ListDepts", func() error {
			_, err := client.ListDepts(ctx, token)
			return err
		}},
	}

	var broken []string
	for _, p := range probes {
		if err := p.call(); err != nil {
			t.Logf("❌ %-16s %v", p.name, err)
			broken = append(broken, p.name)
		} else {
			t.Logf("✅ %-16s OK", p.name)
		}
	}
	if len(broken) > 0 {
		t.Errorf("以下接口的响应与 Go 结构体类型不符（会被整个 Unmarshal 掉）：%v\n"+
			"大概率是 RuoYi 把超范围 Long 序列化成了字符串，而 Go 侧声明的是 int64。"+
			"改用 FlexInt64（见 internal/drive/client.go）", broken)
	}
}
