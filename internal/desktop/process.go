package desktop

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if CoreHealthy(options) {
		return nil
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

// CoreHealthy reports whether the configured EasyShare Core is already serving the expected identity.
func CoreHealthy(options ProcessOptions) bool {
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return false
	}
	nonce := hex.EncodeToString(nonceBytes)
	client := http.Client{Timeout: 300 * time.Millisecond}
	response, err := client.Get(options.BaseURL + "/health?nonce=" + nonce)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var result struct {
		DeviceID string `json:"deviceId"`
		Proof    string `json:"proof"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil || result.DeviceID != options.DeviceID {
		return false
	}
	mac := hmac.New(sha256.New, []byte(options.Token))
	_, _ = mac.Write([]byte(nonce))
	return hmac.Equal([]byte(result.Proof), []byte(hex.EncodeToString(mac.Sum(nil))))
}
