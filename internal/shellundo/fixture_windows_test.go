//go:build windows

package shellundo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const canonicalTestProcess = "EASYSHARE_SHELLUNDO_CANONICAL_TEST"

// Go 从 TEMP 的 ADMINI~1 启动测试 exe；仅 fixture 重执行为真实长路径。
// 同用户 IPC 的生产对端程序比较保持原样，不能用测试别名放松鉴权。
func TestMain(m *testing.M) {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	canonical, err := canonicalExistingTestPath(executable)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !strings.EqualFold(executable, canonical) {
		if os.Getenv(canonicalTestProcess) == "1" {
			fmt.Fprintln(os.Stderr, "测试进程仍使用短别名，拒绝绕过生产对端校验")
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, canonical, os.Args[1:]...)
		command.Env = append(os.Environ(), canonicalTestProcess+"=1")
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			if failure, ok := err.(*exec.ExitError); ok {
				os.Exit(failure.ExitCode())
			}
			fmt.Fprintln(os.Stderr, "canonical 测试 fixture 执行失败:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func canonicalExistingTestPath(path string) (string, error) {
	text, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(text, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", fmt.Errorf("打开已存在 fixture 元数据: %w", err)
	}
	buffer := make([]uint16, 32768)
	n, pathErr := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	closeErr := windows.CloseHandle(handle)
	if pathErr != nil || n == 0 || n >= uint32(len(buffer)) {
		return "", fmt.Errorf("canonical fixture 路径: 长度=%d / %v", n, pathErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	return strings.TrimPrefix(windows.UTF16ToString(buffer[:n]), `\\?\`), nil
}

// 仅测试 fixture 使用最终长路径；生产 inspectComponent 仍然拒绝别名/重定向。
func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := canonicalExistingTestPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func canonicalTestDirectory(t *testing.T) string {
	t.Helper()
	return canonicalTestPath(t, t.TempDir())
}
