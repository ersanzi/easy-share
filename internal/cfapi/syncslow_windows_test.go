//go:build windows

package cfapi

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestSyncFetchSlow 检验一条**曾写进文档却没实测过**的主张：
//
//	「同步模型把整个取数过程占在系统回调线程上，真实云端拉大文件时
//	 可能超过系统容忍上限，结果是 I/O 超时失败。」
//
// 这是可证伪的，所以不该只写在注释里。本用例用同步 Fetcher 在**回调线程上**
// 故意睡若干秒，看读取是否仍然成功。延迟用环境变量 `CFAPI_SLOW_SEC` 配置
// （默认 5），便于探测阈值。
//
// **实测结果（本机 Win10/11，2026-10-16）**：
//
//	CFAPI_SLOW_SEC=5   → 成功，耗时 5.017s
//	CFAPI_SLOW_SEC=20  → 成功，耗时 20.005s
//
// **即：在 20 秒以内，同步模型在本机没有被观察到超时失败。**
// 因此原文档里「同步会撞 I/O 超时」的**强表述不成立**，已改为更弱的表述
// （见 README 的「已知局限」）。仍未探到真实阈值——那需要跑更长的档位，
// 而更长的等待会拖慢 CI；留给需要时按需探测。
//
// 这条用例的价值不在于断言某个结论，而在于**把可证伪的主张变成可执行的探测**：
// 将来若有人怀疑同步模型，跑 `CFAPI_SLOW_SEC=60 go test -run TestSyncFetchSlow` 即可。
func TestSyncFetchSlow(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	// 延迟可配：默认 5s，用 CFAPI_SLOW_SEC 跑更大的档位
	delay := 5 * time.Second
	if v := os.Getenv("CFAPI_SLOW_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			delay = time.Duration(n) * time.Second
		}
	}
	root := t.TempDir()
	const content = "sync slow content"

	list := func(ctx contextContext) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path: "s.txt", Size: int64(len(content)),
			ModifiedUnix: 1767225600,
			Identity:     EncodeIdentity(77),
		}}, nil
	}
	// 同步 Fetcher：在**回调线程上**直接 sleep
	syncFetch := func(req FetchRequest) FetchResult {
		time.Sleep(delay)
		return FetchResult{Data: []byte(content), Offset: 0}
	}

	sr, err := NewSyncRoot(testProvider, []byte("easyshare-sync-slow"), []byte("esf:"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterSyncRoot(root, &sr.Registration, &sr.Policies, RegisterFlagNone); err != nil {
		t.Fatalf("RegisterSyncRoot: %v", err)
	}
	defer func() { _ = UnregisterSyncRoot(root) }()

	sess, err := Connect(root, syncFetch,
		ConnectFlagRequireProcessInfo|ConnectFlagRequireFullFilePath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if st := BuildTree(root, mustList(t, list)); len(st.Failures) != 0 {
		t.Fatalf("建树失败: %s", st.Error())
	}

	start := time.Now()
	got, err := os.ReadFile(filepath.Join(root, "s.txt"))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("同步慢取数（%v）失败：%v —— 文档里的超时主张得到支撑", delay, err)
	}
	if string(got) != content {
		t.Errorf("内容 = %q，期望 %q", got, content)
	}
	t.Logf("同步慢取数（%v）**成功**：耗时 %v —— 「同步有超时风险」在该时长下未被观察到",
		delay, elapsed.Round(time.Millisecond))
}
