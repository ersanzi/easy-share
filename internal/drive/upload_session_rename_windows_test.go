//go:build windows

package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestSessionRenameTransientRetriesWrappedWindowsErrors(t *testing.T) {
	for _, code := range []error{windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION} {
		t.Run(code.Error(), func(t *testing.T) {
			attempts := 0
			err := renameUploadSessionWithRetry(func() error {
				attempts++
				if attempts < 3 {
					return &os.LinkError{Op: "rename", Old: "snapshot.tmp", New: "snapshot.json", Err: fmt.Errorf("占用: %w", code)}
				}
				return nil
			})
			if err != nil || attempts != 3 {
				t.Fatalf("短暂占用必须重试同操作后成功: attempts=%d err=%v", attempts, err)
			}
		})
	}
}

func TestSessionRenamePermanentRejectionReturnsLastError(t *testing.T) {
	for _, code := range []error{windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION} {
		t.Run(code.Error(), func(t *testing.T) {
			attempts := 0
			var last *os.LinkError
			err := renameUploadSessionWithRetry(func() error {
				attempts++
				last = &os.LinkError{Op: "rename", Old: "snapshot.tmp", New: "snapshot.json", Err: code}
				return last
			})
			if err != last || !errors.Is(err, code) || attempts != 6 {
				t.Fatalf("持续拒绝必须有限失败并保留最后错误: attempts=%d err=%v", attempts, err)
			}
		})
	}
}

func TestSessionRenameOtherErrorsNotRetried(t *testing.T) {
	for _, code := range []error{windows.ERROR_FILE_NOT_FOUND, windows.ERROR_INVALID_NAME, windows.ERROR_DISK_FULL, windows.ERROR_NOT_SAME_DEVICE, os.ErrPermission} {
		t.Run(code.Error(), func(t *testing.T) {
			attempts := 0
			err := renameUploadSessionWithRetry(func() error { attempts++; return code })
			if err != code || attempts != 1 {
				t.Fatalf("非明确临时错误不能重试或吞错: attempts=%d err=%v", attempts, err)
			}
		})
	}
}

func TestSessionSaveRetryKeepsSameSnapshotAndTarget(t *testing.T) {
	store := NewSessionStore(t.TempDir())
	state := &sessionState{SessionID: 9, UploadID: "uid-1", PartSize: 4, Space: SpacePersonal, Path: "nested/big.bin", Size: 10, ModTime: 100,
		Done: []PartETag{{PartNumber: 1, ETag: "pre"}}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	target := store.fileOf(state.Space, state.Path, state.Size, state.ModTime)
	old, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	state.Done = append(state.Done, PartETag{PartNumber: 2, ETag: "next"})
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	err = store.saveWithRename(state, func(src, dst string) error {
		return renameUploadSessionWithRetry(func() error {
			attempts++
			if src != target+".tmp" || dst != target || filepath.Dir(src) != store.dir {
				t.Fatal("重试改变了会话指纹、basename 或临时正文路径")
			}
			body, readErr := os.ReadFile(src)
			if readErr != nil || !bytes.Equal(body, payload) {
				t.Fatalf("重试正文发生变化: %q %v", body, readErr)
			}
			current, readErr := os.ReadFile(dst)
			if readErr != nil || !bytes.Equal(current, old) {
				t.Fatalf("原子替换前删除或改写了旧目标: %q %v", current, readErr)
			}
			if attempts < 3 {
				return &os.LinkError{Op: "rename", Old: src, New: dst, Err: windows.ERROR_SHARING_VIOLATION}
			}
			return os.Rename(src, dst)
		})
	})
	if err != nil || attempts != 3 {
		t.Fatalf("同操作替换失败: attempts=%d err=%v", attempts, err)
	}
	if loaded := store.Load(state.Space, state.Path, state.Size, state.ModTime); !reflect.DeepEqual(loaded, state) {
		t.Fatalf("持久化分片快照不符: %+v", loaded)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("成功 rename 后不应残留临时正文: %v", err)
	}
}

func TestSessionSavePermanentFailurePreservesTargetAndPayload(t *testing.T) {
	store := NewSessionStore(t.TempDir())
	state := &sessionState{SessionID: 9, UploadID: "uid-1", PartSize: 4, Space: SpacePersonal, Path: "big.bin", Size: 10, ModTime: 100}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	target := store.fileOf(state.Space, state.Path, state.Size, state.ModTime)
	old, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	state.Done = []PartETag{{PartNumber: 1, ETag: "new"}}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	err = store.saveWithRename(state, func(src, dst string) error {
		return renameUploadSessionWithRetry(func() error {
			attempts++
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: windows.ERROR_ACCESS_DENIED}
		})
	})
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || attempts != 6 {
		t.Fatalf("永久拒绝必须继续报告: attempts=%d err=%v", attempts, err)
	}
	current, readErr := os.ReadFile(target)
	if readErr != nil || !bytes.Equal(current, old) {
		t.Fatalf("失败不得删除或破坏旧会话: %q %v", current, readErr)
	}
	pending, readErr := os.ReadFile(target + ".tmp")
	if readErr != nil || !bytes.Equal(pending, payload) {
		t.Fatalf("失败不得丢弃待持久化正文: %q %v", pending, readErr)
	}
}

func TestMultipartSessionSaveFailureStopsBeforeComplete(t *testing.T) {
	server := &serverState{}
	client, store, _ := newSessionEnv(t, server)
	content := []byte("0123456789")
	localPath, modTime := writeFile(t, content)
	state := &sessionState{SessionID: 9, UploadID: "uid-1", PartSize: 4, Space: SpacePersonal, Path: "big.bin", Size: 10, ModTime: modTime.Unix(),
		Done: []PartETag{{PartNumber: 1, ETag: "pre"}}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	target := store.fileOf(state.Space, state.Path, state.Size, state.ModTime)
	old, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	// 只锁自有会话文件，保留读共享但拒绝 delete/rename 共享；全程持锁使拒绝持续。
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	err = client.uploadFileMultipart(context.Background(), "tok", SpacePersonal, "big.bin", localPath, 10, modTime, nil, "", nil)
	if err == nil || !strings.Contains(err.Error(), "保存上传会话失败") ||
		!(errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
		t.Fatalf("持久化失败必须传到上传调用者: %v", err)
	}
	if server.createCalls.Load() != 0 || server.partCalls.Load() != 1 || server.completed.Load() != 0 || server.aborted.Load() != 0 {
		t.Fatalf("失败不能重建会话、上传下一片或 Complete: create=%d part=%d complete=%d abort=%d", server.createCalls.Load(), server.partCalls.Load(), server.completed.Load(), server.aborted.Load())
	}
	current, readErr := os.ReadFile(target)
	if readErr != nil || !bytes.Equal(current, old) {
		t.Fatalf("旧分片回执必须保留以供安全续传: %q %v", current, readErr)
	}
	original, readErr := os.ReadFile(localPath)
	if readErr != nil || !bytes.Equal(original, content) {
		t.Fatalf("保存失败不能破坏上传 payload: %q %v", original, readErr)
	}
}

func TestSessionSaveRetriesActualTemporaryWindowsLock(t *testing.T) {
	store := NewSessionStore(t.TempDir())
	state := &sessionState{SessionID: 9, UploadID: "uid-1", PartSize: 4, Space: SpacePersonal, Path: "big.bin", Size: 10, ModTime: 100}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(store.fileOf(state.Space, state.Path, state.Size, state.ModTime))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { time.Sleep(25 * time.Millisecond); closed <- windows.CloseHandle(handle) }()
	state.Done = []PartETag{{PartNumber: 1, ETag: "new"}}
	err = store.Save(state)
	if closeErr := <-closed; closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatalf("短暂真实占用释放后保存失败: %v", err)
	}
	if loaded := store.Load(state.Space, state.Path, state.Size, state.ModTime); !reflect.DeepEqual(loaded, state) {
		t.Fatalf("真实重试后的分片回执不符: %+v", loaded)
	}
}
