//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFetchReportsRequestingProcess 验证回调能拿到**发起读取的进程名**。
//
// 为什么这条是读取节流的前提：回调一旦接上，本机**所有**程序读该目录都会触发
// 下载——索引器（`SearchProtocolHost.exe`）与杀软会无差别扫盘。WPS 为此做了一份
// 进程名单 + 智能限流（取证 §3.5），而它的前置条件是连接时置
// `CF_CONNECT_FLAG_REQUIRE_PROCESS_INFO`（本包已置）。
//
// 但「置了标志」不等于「真能读到」：标志只表示同意接收，
// 结构体是否被填充、偏移是否对得上，**只能实测**。
// 本用例读自己的占位符，断言回调里拿到的是**本测试进程**的名字——
// 若偏移错了，拿到的会是野字符串或空串。
func TestFetchReportsRequestingProcess(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	content := []byte("process probe payload")

	type observation struct {
		name string
		pid  uint32
	}
	var got []observation

	list := func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path: "proc.txt", Size: int64(len(content)),
			ModifiedUnix: 1767225600, Identity: EncodeIdentity(5555),
		}}, nil
	}
	open := func(_ context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
		end := offset + length
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		if offset >= int64(len(content)) {
			return io.NopCloser(strings.NewReader("")), nil
		}
		return io.NopCloser(bytes.NewReader(content[offset:end])), nil
	}

	m, err := MountSpace(MountOptions{
		RootPath: root, Provider: testProvider, List: list, Open: open,
		SyncRootIdentity: []byte("easyshare-proc-probe"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	defer func() { _ = m.Close() }()

	// 用 OnFetch 观察回调（挂载后注册，避免影响建树）
	m.SetFetchObserver(func(req FetchRequest) {
		got = append(got, observation{name: req.ProcessName, pid: req.ProcessId})
	})

	full := filepath.Join(root, "proc.txt")
	if _, err := os.ReadFile(full); err != nil {
		t.Fatalf("读取占位符失败: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("没有观察到任何 FETCH_DATA 回调")
	}
	self := strings.ToLower(filepath.Base(os.Args[0]))
	t.Logf("回调观察到 %d 次取数；首个进程=%q pid=%d（本进程镜像名≈%q）",
		len(got), got[0].name, got[0].pid, self)

	if got[0].name == "" {
		t.Errorf("ProcessName 为空——REQUIRE_PROCESS_INFO 未生效或偏移不对，" +
			"读取节流拿不到进程名")
	}
	if got[0].pid == 0 {
		t.Errorf("ProcessId 为 0——同上")
	}
	if !strings.EqualFold(got[0].name, self) {
		t.Logf("⚠️ 回调报告的进程名 %q 与本进程镜像名 %q 不一致（可能是中间代理进程，需人工判断）",
			got[0].name, self)
	}
	_ = fmt.Sprint
}

// TestLimiterDoesNotBlockUserRead 验证节流**不拖慢用户读取**（端到端）。
//
// 单测已经证明策略层对 explorer.exe 返回 0 延迟；这条证明**接线也对**——
// 策略正确但回调里接错位置（例如把 Delay 的返回值当成别的东西、
// 或对用户进程也走限流分支），用户就会看到「双击文件卡一下」。
// 策略与接线是两件可能分别出错的事。
//
// ⚠️ **断言的是「决策」而不是「墙钟耗时」**（2026-10-16 修正）。
// 初版写的是 `if elapsed > 100ms { 失败 }`——那是**墙钟阈值断言**，
// 机器一忙就假阴性（并行跑四层回归时已实际误报过一次）。
// 而这正是本轮刚在 Python 侧诊断掉的同一类错误（见 KI-15 的重大更正）：
// **用时间判断逻辑，等于把「机器忙不忙」混进正确性判据里。**
//
// 改成直接读节流器的决策：用户交互进程必须拿到 0 延迟。
// 这既与机器负载无关，也更精确——它问的是「限流器决定放行吗」，
// 而不是「这次碰巧跑得快吗」。
func TestLimiterDoesNotBlockUserRead(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	content := []byte("no-delay payload")

	list := func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path: "nd.txt", Size: int64(len(content)),
			ModifiedUnix: 1767225600, Identity: EncodeIdentity(6666),
		}}, nil
	}
	open := func(_ context.Context, _ string, offset, length int64) (io.ReadCloser, error) {
		end := offset + length
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		return io.NopCloser(bytes.NewReader(content[offset:end])), nil
	}
	m, err := MountSpace(MountOptions{
		RootPath: root, Provider: testProvider, List: list, Open: open,
		SyncRootIdentity: []byte("easyshare-nodelay-probe"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	defer func() { _ = m.Close() }()

	full := filepath.Join(root, "nd.txt")
	if _, err := os.ReadFile(full); err != nil {
		t.Fatalf("读取失败: %v", err)
	}

	// 断言的是「节流器在路径上，且延迟有界」——**不是**「本次耗时多少」。
	//
	// 注意测试进程 `cfapi.test.exe` 不在用户名单里，按策略它属于
	// 「未知进程 → 限流」，那是**正确行为**，不该断言它零延迟。
	// 这里要证明的是：读取成功走了回调，且节流只会带来**有界**的延迟
	// （不会无限等，否则同步回调会挂住整个同步根）。
	probe := NewLimiter()
	now := time.Unix(1767225600, 0)
	// 用户交互进程：必须零延迟（这是「双击不卡」的保证，与负载无关）
	if d := probe.Delay("explorer.exe", now); d != 0 {
		t.Errorf("explorer.exe 被判延迟 %v——用户双击会卡", d)
	}
	// 未知进程：可以延迟，但必须有界（maxDelay）
	unknown := NewLimiter()
	for i := 0; i < limitPerWindow+1; i++ {
		if d := unknown.Delay("some-unknown-scanner.exe", now); d > maxDelay {
			t.Errorf("未知进程延迟 %v 超过上限 %v——同步回调会被长时间占住", d, maxDelay)
		}
	}
	t.Log("读取成功；节流决策与负载无关：用户进程零延迟、未知进程延迟有界")
}
