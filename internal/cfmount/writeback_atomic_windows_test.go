//go:build windows

package cfmount

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWritebackAtomicReplaceTransientFailureRetriesSameOperation(t *testing.T) {
	attempts := 0
	err := replaceWritebackStateWithRetry(func() error {
		attempts++
		if attempts < 3 {
			return windows.ERROR_ACCESS_DENIED
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("短暂占用重试不符: attempts=%d err=%v", attempts, err)
	}
}

func TestWritebackAtomicReplacePermanentFailureStillFails(t *testing.T) {
	attempts := 0
	err := replaceWritebackStateWithRetry(func() error { attempts++; return windows.ERROR_ACCESS_DENIED })
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || attempts != 6 {
		t.Fatalf("永久拒绝不能吞错或无限重试: attempts=%d err=%v", attempts, err)
	}
	attempts = 0
	err = replaceWritebackStateWithRetry(func() error { attempts++; return windows.ERROR_FILE_NOT_FOUND })
	if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || attempts != 1 {
		t.Fatalf("非占用错误不能重试: attempts=%d err=%v", attempts, err)
	}
}
