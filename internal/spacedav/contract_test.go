package spacedav

import (
	"testing"

	"easyshare/internal/drive"
)

// TestDriveClientSatisfiesInterfaces 守住两个**编译期契约**。
//
// 第 2 条尤其要紧，它与 KI-9 直接相关：
// `readFile.Seek(0, SeekStart)` 是 `http.ServeContent` 的必需调用，而它的实现
// 依赖 `f.client.(openRangeer)` 这个**运行期类型断言**——重开一条从 0 起的流。
//
// 若 `*drive.Client` 哪天不再满足 `openRangeer`（例如 `PresignGet` 改名或改签名），
// 断言会**静默失败**：编译照过、测试照过（测试用假客户端），
// 但 `Seek` 会开始返回 ErrInvalid → ServeContent 回 500 →
// **「此电脑」里双击任何文件又打不开了**，即 KI-9 复发，且没有任何构建期信号。
//
// 编译期断言把这种退化变成**构建失败**。
func TestDriveClientSatisfiesInterfaces(t *testing.T) {
	c := drive.New("http://127.0.0.1:1")

	// 必需：FS 的四个方法（否则连挂载都不成立）
	var _ Client = c
	// 关键：有它才能实现 Seek(0, SeekStart) —— KI-9 的修复依赖此能力
	var _ openRangeer = c

	if _, ok := any(c).(openRangeer); !ok {
		t.Fatal("*drive.Client 不满足 openRangeer —— 文件下载会静默退化为 500（KI-9 复发）")
	}
}
