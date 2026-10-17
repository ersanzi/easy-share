package drive_test

// 活栈回归（KI-24）：单请求上传必须能自愈，且**重试不得虚增版本**。
//
// 背景：单请求路径原本既无停滞检测也无重试。transfer 客户端 30 分钟超时
// 对它而言是上万倍余量（一次 PUT 健康时 ~170ms），实测（本机，8 MiB，8 次）
// 有 1 次挂死且**无任何自愈**。
//
// **关键在重试方式**：控制面在**签发预签名时就登记并推进版本**
// （`registerOnPresign`）。实测确认：连着签发两次、一个字节都没写，
// 库里 version_id 已经是 2。因此重试必须**复用第一次的 URL**；
// 若重签，版本会虚增、CAS 语义失真。
//
// 本用例**必须真的触发一次重试**才有意义——只在happy path 上跑的话，
// 第一次尝试就成功返回，根本走不到重试，「重签会虚增版本」这个错误也就测不出来
// （第一版就是这样，回退到重签后依然通过）。所以这里注入一次
// **首包必失败的传输层**，强制走重试分支。

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"easyshare/internal/drive"
)

// failingFirstRoundTripper 让**第一次** PUT 失败，之后放行。
// 用来强制客户端走重试分支（这是本用例能发现「重签虚增版本」的前提）。
type failingFirstRoundTripper struct {
	inner  http.RoundTripper
	failed bool
}

func (f *failingFirstRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !f.failed {
		f.failed = true
		return nil, errors.New("注入的传输层故障（第一次尝试）")
	}
	return f.inner.RoundTrip(req)
}

func TestLiveSinglePutRetryDoesNotInflateVersion(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()
	const path = "live-cas/single-put-version.txt"
	const content = "single-put-content"

	_ = client.Delete(ctx, token, drive.SpacePersonal, path)
	_ = purgeRecycledFor(t, client, ctx, token, path)
	cleanupPath(t, client, ctx, token, path)

	local := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 强制第一次直传失败 → 必然走重试
	client.SetTransferTransportForTest(&failingFirstRoundTripper{inner: http.DefaultTransport})

	if err := client.UploadFile(ctx, token, drive.SpacePersonal, path, local, nil); err != nil {
		t.Fatalf("注入一次故障后上传应自愈，实得: %v", err)
	}

	if got := readObject(t, ctx, client, token, path); got != content {
		t.Errorf("内容应为 %q，实得 %q", content, got)
	}

	// 版本必须正好是 1：**一次上传只登记一次**。
	// 若重试是重新签发而不是复用 URL，这里会看到 2。
	v := currentVersionOf(t, ctx, client, token, path)
	if v != 1 {
		t.Errorf("一次单请求上传（含一次重试）后版本应为 1，实得 %d——"+
			"说明重试重新签发了预签名 URL，版本被虚增（CAS 语义会失真）", v)
	}
	t.Logf("✅ 注入一次传输故障后自愈成功，版本正好为 %d（重试未重签）", v)

}
