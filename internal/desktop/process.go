package desktop

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type ProcessOptions struct {
	BaseURL, ConfigPath, Token, DeviceID string
	LogPath                              string
}

// EnsureCore 确保 EasyShare Core 正在运行；未运行则拉起并等待其健康。
// 平台差异（可执行文件名、进程属性）由 coreBinaryName / configureProcess 提供。
func EnsureCore(ctx context.Context, options ProcessOptions) error {
	absent, err := waitCoreAbsent(ctx, options)
	if err != nil {
		return err
	}
	if !absent {
		return nil // 已经有一个健康的 Core
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	name := coreBinaryName()
	candidate := filepath.Join(filepath.Dir(executable), name)
	if _, err = os.Stat(candidate); err != nil {
		candidate = filepath.Join("build", "bin", name)
	}
	command := exec.Command(candidate, "-config", options.ConfigPath)
	configureProcess(command)
	var processLog *os.File
	if options.LogPath != "" {
		if mkdirErr := os.MkdirAll(filepath.Dir(options.LogPath), 0o700); mkdirErr == nil {
			processLog, _ = os.OpenFile(options.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if processLog != nil {
				command.Stdout = processLog
				command.Stderr = processLog
			}
		}
	}
	if err := command.Start(); err != nil {
		if processLog != nil {
			_ = processLog.Close()
		}
		return err
	}
	if processLog != nil {
		_ = processLog.Close()
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if CoreHealthy(options) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return errors.New("core did not become healthy")
}

// coreProbeTimeout 是一次健康探测的时间预算。
//
// 这里曾是 300ms。实测在 CPU 被占满时（同时跑四层回归、GOMAXPROCS 压到 2），
// 300 次探测会失败 2~10 次，且**每次失败的耗时都恰好卡在预算上**
// （307 / 312 / 315 / 320 / 325ms）——失败来自调度抖动，不是 Core 有问题：
// 同样条件下无负载时最慢只有 11ms。
//
// 代价是不对称的：把「慢」误读成「不在」，EnsureCore 会去拉起**重复的 Core**、
// cmd/core 会 log.Fatal；而这些代价换不来什么——Core 真的不在时是
// 「连接被拒」，实测 20 次最慢 14ms，与预算无关，放宽它几乎不花钱。
const coreProbeTimeout = time.Second

// 探测不确定（超时）时的重试次数与间隔。
const (
	coreProbeAttempts   = 3
	coreProbeRetryDelay = 200 * time.Millisecond
)

// coreState 是一次健康探测的结论。
//
// 为什么不能只用布尔：调用方会据此做**不可逆**决策（拉起进程 / 退出）。
// 「超时」只说明没问到答案，不说明「没有 Core」——把两者混为一谈，
// 正是误拉重复进程与误重启健康 Core 的来源。
type coreState int

const (
	// coreStateHealthy 是我们的 Core，身份与证明都对。
	coreStateHealthy coreState = iota
	// coreStateAbsent 确定没有我们的 Core 在监听（连接被拒等）——可以安全拉起。
	coreStateAbsent
	// coreStateUnknown 说不清（探测超时）——**不是**「不在」的证据。
	coreStateUnknown
)

// waitCoreAbsent 直到能确定「我们的 Core 不在监听」为止。
//
// 返回 (true, nil) 表示确定不在、可以拉起；(false, nil) 表示已经有一个健康的 Core；
// 连续超时（说不清）返回错误——**绝不据此拉起新进程**。
//
// 依据是代价不对称：超时说明「有个东西占着那个地址但没答话」（卡死的 Core、
// 别人的进程、或丢包）。这几种情况拉起新进程都帮不上忙，只会多一个抢端口的
// 重复进程，再附送一条把原因指向「端口冲突」的误导性致命日志——
// 真正的毛病（老 Core 卡死）反而被这条日志盖住了。
func waitCoreAbsent(ctx context.Context, options ProcessOptions) (bool, error) {
	for attempt := 1; attempt <= coreProbeAttempts; attempt++ {
		switch probeCore(options) {
		case coreStateHealthy:
			return false, nil
		case coreStateAbsent:
			return true, nil
		}
		if attempt == coreProbeAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(coreProbeRetryDelay):
		}
	}
	return false, errors.New("core 健康探测连续超时，无法确定 Core 是否在运行：不拉起重复进程，请检查 Core 是否卡死")
}

// probeCore 探测 Core 状态，并区分「确定」与「说不清」。
func probeCore(options ProcessOptions) coreState {
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return coreStateAbsent // 本机随机源失败：重试无益，如实当作没有可用 Core
	}
	nonce := hex.EncodeToString(nonceBytes)
	client := http.Client{Timeout: coreProbeTimeout}
	response, err := client.Get(options.BaseURL + "/health?nonce=" + nonce)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return coreStateUnknown // 没问到答案就是没问到，不能当作「不在」
		}
		return coreStateAbsent // 连接被拒等：确定没有监听者
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return coreStateAbsent
	}
	var result struct {
		DeviceID string `json:"deviceId"`
		Proof    string `json:"proof"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil || result.DeviceID != options.DeviceID {
		return coreStateAbsent
	}
	mac := hmac.New(sha256.New, []byte(options.Token))
	_, _ = mac.Write([]byte(nonce))
	if !hmac.Equal([]byte(result.Proof), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return coreStateAbsent
	}
	return coreStateHealthy
}

// CoreHealthy reports whether the configured EasyShare Core is already serving the expected identity.
func CoreHealthy(options ProcessOptions) bool {
	return probeCore(options) == coreStateHealthy
}
