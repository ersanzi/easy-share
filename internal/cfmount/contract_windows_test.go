//go:build windows

package cfmount

import (
	"testing"

	"easyshare/internal/drive"
)

// TestDriveClientSatisfiesInterfaces 守住两个**编译期契约**：
//
//  1. *drive.Client 必须满足 Lister（本包的必需接口）；
//  2. *drive.Client 必须满足 Presigner（本包的可选接口）。
//
// 第 2 条尤其重要：`Open` 是靠 `cfg.Client.(Presigner)` 这个**运行期类型断言**
// 决定走 Range 还是整份读的。若 drive.Client 哪天不再满足它（例如 PresignGet
// 改名或改签名），断言会**静默失败**——代码仍然能跑，但会退回整份读取，
// 即「Range 优化悄悄失效」。编译期断言能把这种退化变成构建失败。
func TestDriveClientSatisfiesInterfaces(t *testing.T) {
	// 编译期断言（不用 reflect，失败时错误信息更直接）
	var c = drive.New("http://127.0.0.1:1")

	var _ Lister = c
	var _ Presigner = c
	var _ ChangeLister = c

	// 运行期再确认一次类型断言与编译期一致
	if _, ok := any(c).(Presigner); !ok {
		t.Fatal("*drive.Client 不满足 Presigner——Range 路径会静默退化为整份读取")
	}
	if _, ok := any(c).(ChangeLister); !ok {
		t.Fatal("*drive.Client 不满足 ChangeLister——占位符挂载会静默退回每次全量列举")
	}
}
