package desktop

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// healthServer 起一个健康端点，delay 用来模拟「Core 活着但响应慢」。
func healthServer(t *testing.T, token string, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		nonce := request.URL.Query().Get("nonce")
		mac := hmac.New(sha256.New, []byte(token))
		_, _ = mac.Write([]byte(nonce))
		_, _ = writer.Write([]byte(`{"deviceId":"device","proof":"` + hex.EncodeToString(mac.Sum(nil)) + `"}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestProbeTimeoutIsNotAbsent 是本文件的核心回归：**探测超时不能被当成「Core 不在」**。
//
// 为什么重要：调用方会据此做不可逆决策——EnsureCore 会去拉起新进程、
// cmd/core 会 log.Fatal。把「没问到」读成「不在」，代价是拉起**重复的 Core**，
// 然后那条重复进程因抢不到端口而留下一条误导性的「端口占用」致命日志。
//
// 触发条件不是假设：实测在 CPU 被占满时（四层回归同时跑 + GOMAXPROCS=2），
// 旧的 300ms 预算下 300 次探测会失败 2~10 次，且失败的耗时**全部卡在 300ms**。
func TestProbeTimeoutIsNotAbsent(t *testing.T) {
	const token = "secret"
	// 响应 400ms：超过旧的 300ms 预算，但在新的 1s 预算内。
	server := healthServer(t, token, 400*time.Millisecond)
	options := ProcessOptions{BaseURL: server.URL, Token: token, DeviceID: "device"}

	state := probeCore(options)
	if state == coreStateAbsent {
		t.Fatalf("Core 明明活着（只是响应慢），却被判成 absent —— " +
			"调用方会据此拉起重复的 Core / log.Fatal")
	}
	if state != coreStateHealthy {
		t.Fatalf("Core 响应在预算内且身份/证明都对，期望 healthy，得到 %v", state)
	}
}

// TestProbeTimeoutBeyondBudgetIsUnknown：响应慢到超过预算时，结论必须是
// 「说不清」（unknown），而不是「不在」（absent）。
func TestProbeTimeoutBeyondBudgetIsUnknown(t *testing.T) {
	const token = "secret"
	server := healthServer(t, token, coreProbeTimeout+500*time.Millisecond)
	options := ProcessOptions{BaseURL: server.URL, Token: token, DeviceID: "device"}

	if state := probeCore(options); state != coreStateUnknown {
		t.Fatalf("探测超时应当得到 unknown，得到 %v（absent 会导致拉起重复进程）", state)
	}
}

// TestWaitCoreAbsentRefusesToSpawnOnUnknown：连续超时时必须**拒绝**拉起新进程，
// 并把原因说出来，而不是当作「Core 不在」。
func TestWaitCoreAbsentRefusesToSpawnOnUnknown(t *testing.T) {
	const token = "secret"
	server := healthServer(t, token, coreProbeTimeout+500*time.Millisecond)
	options := ProcessOptions{BaseURL: server.URL, Token: token, DeviceID: "device"}

	absent, err := waitCoreAbsent(context.Background(), options)
	if err == nil {
		t.Fatalf("探测连续超时却给出了确定结论（absent=%v）：会拉起重复的 Core", absent)
	}
	if absent {
		t.Fatalf("探测连续超时却报告「确定不在」：会拉起重复的 Core")
	}
}

// TestWaitCoreAbsentOnRefusedConnection：端口上确实没人监听时，才允许拉起。
// 顺带钉住「这条路径很快」——它是启动时的常态路径，放宽预算不该拖慢它。
func TestWaitCoreAbsentOnRefusedConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	listener.Close()

	start := time.Now()
	absent, err := waitCoreAbsent(context.Background(), ProcessOptions{BaseURL: baseURL, Token: "x", DeviceID: "d"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("连接被拒是确定结论，不该报错：%v", err)
	}
	if !absent {
		t.Fatal("端口无人监听却没判定为 absent：Core 永远拉不起来")
	}
	// 连接被拒是毫秒级的；这里只钉住「没有白等整个预算」，给足余量。
	if elapsed > coreProbeTimeout/2 {
		t.Fatalf("「Core 不在」的判定耗时 %v，接近探测预算 %v —— 启动路径被拖慢", elapsed, coreProbeTimeout)
	}
}

// TestWaitCoreAbsentSeesHealthyCore：已有健康 Core 时返回 (false, nil)，即「不要拉起」。
func TestWaitCoreAbsentSeesHealthyCore(t *testing.T) {
	const token = "secret"
	server := healthServer(t, token, 0)
	absent, err := waitCoreAbsent(context.Background(),
		ProcessOptions{BaseURL: server.URL, Token: token, DeviceID: "device"})
	if err != nil {
		t.Fatal(err)
	}
	if absent {
		t.Fatal("已有健康 Core 却判定为 absent：会拉起重复进程")
	}
}

// TestCoreHealthyStillDistinguishesIdentityAndProof 守住原有的身份/证明语义不被三态改造破坏。
func TestCoreHealthyStillDistinguishesIdentityAndProof(t *testing.T) {
	const token = "secret"
	server := healthServer(t, token, 0)
	options := ProcessOptions{BaseURL: server.URL, Token: token, DeviceID: "device"}
	if !CoreHealthy(options) {
		t.Fatal("身份与证明都对，却报告不健康")
	}
	if CoreHealthy(ProcessOptions{BaseURL: server.URL, Token: "wrong", DeviceID: "device"}) {
		t.Fatal("证明错误却报告健康")
	}
	if CoreHealthy(ProcessOptions{BaseURL: server.URL, Token: token, DeviceID: "wrong"}) {
		t.Fatal("设备身份不符却报告健康")
	}
}
