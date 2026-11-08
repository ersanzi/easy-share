//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 真文件、真实CFAPI、独立模拟远端确认；不访问生产根或任意用户正文。
func cacheTestSetup(t *testing.T, payload ...[]byte) (*Mount, string, []byte, CacheBaseline) {
	t.Helper()
	root := directoryRestoreTempRoot(t)
	content := bytes.Repeat([]byte("cloud-cache-correctness"), 32768)
	if len(payload) == 1 {
		content = payload[0]
	}
	m, err := mountSpaceForTest(t, MountOptions{RootPath: root, Provider: testProvider, SyncRootIdentity: []byte("safe-cache-test"), Policies: &SyncPolicies{Hydration: HydrationPolicy{Primary: 1}, Population: PopulationPolicy{Primary: 3}}, ProtectedPaths: []string{"file.bin"}, List: func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{Path: "file.bin", Size: int64(len(content)), Identity: EncodeIdentity(1171)}}, nil
	}, Open: func(ctx context.Context, p string, off, n int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content[off : off+n])), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "file.bin")
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := ReadDirectoryNTFSIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	return m, path, content, CacheBaseline{RelativePath: "file.bin", RemoteFileID: 1171, Version: 1, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), LocalInfo: info, LocalIdentity: localID, Confirm: func(context.Context) error { return nil }}
}

func TestVerifiedCacheReleaseAndRead(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	var fetches atomic.Int64
	m.SetFetchObserver(func(FetchRequest) { fetches.Add(1) })
	checks := 0
	baseline.Confirm = func(context.Context) error { checks++; return nil }
	result, err := m.ReleaseVerifiedCache(context.Background(), baseline)
	if err != nil || !result.Released || result.AllocatedBefore == 0 || result.AllocatedAfter != 0 || checks != 1 {
		t.Fatalf("release: %+v %v confirms=%d", result, err, checks)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("rehydrate: %v", err)
	}
	info, _ := os.Lstat(path)
	baseline.LocalInfo = info
	result, err = m.ReleaseVerifiedCache(context.Background(), baseline)
	if err != nil || !result.Released || result.AllocatedAfter != 0 {
		t.Fatalf("second release: %+v %v", result, err)
	}
	beforeFetch, beforeChecks := fetches.Load(), checks
	result, err = m.ReleaseVerifiedCache(context.Background(), baseline)
	if err != nil || !result.AlreadyOnline || result.Released {
		t.Fatalf("idempotent: %+v %v", result, err)
	}
	if fetches.Load() != beforeFetch || checks != beforeChecks || allocatedSize(t, path) != 0 {
		t.Fatal("already-online check downloaded bytes or reconfirmed remote content")
	}
}

func TestVerifiedCacheRejectsChangedBytesAndRemoteFailure(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	baseline.Confirm = func(context.Context) error { return errors.New("remote version changed") }
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err == nil || result.Released {
		t.Fatal("remote failure released file")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, content) {
		t.Fatal("remote failure changed content")
	}
	baseline.Confirm = func(context.Context) error { return nil }
	changed := append([]byte(nil), content...)
	changed[0] ^= 0xff
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err == nil || result.Released {
		t.Fatal("changed local file released")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, changed) {
		t.Fatal("local modification lost")
	}
}

func TestVerifiedCacheRejectsBusyAndIdentitySwap(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	u, _ := windows.UTF16PtrFromString(path)
	held, err := windows.CreateFile(u, windows.GENERIC_READ, 7, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); !errors.Is(err, ErrCacheFileBusy) || result.Released {
		windows.CloseHandle(held)
		t.Fatalf("busy accepted: %v", err)
	}
	windows.CloseHandle(held)
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err == nil || result.Released {
		t.Fatal("replaced NTFS identity accepted")
	}
}

func TestVerifiedCacheRejectsPinAndWrongIndex(t *testing.T) {
	m, path, _, baseline := cacheTestSetup(t)
	wrong := baseline
	wrong.RemoteFileID = 9999
	if _, err := m.ReleaseVerifiedCache(context.Background(), wrong); err == nil {
		t.Fatal("wrong remote identity accepted")
	}
	if _, err := m.ReleaseVerifiedCache(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
	u, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(u, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := proc("CfSetPinState")
	if err != nil {
		windows.CloseHandle(h)
		t.Fatal(err)
	}
	hr, _, _ := pin.Call(uintptr(h), 1, 0, 0)
	windows.CloseHandle(h)
	if hr != 0 {
		t.Fatal(checkHResult(hr, "test pin"))
	}
	info, _ := os.Lstat(path)
	baseline.LocalInfo = info
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); !errors.Is(err, ErrCacheRetained) || result.Released {
		t.Fatalf("pinned released: %v", err)
	}
}

func TestVerifiedCacheRetainsPartialWithoutHydration(t *testing.T) {
	m, path, _, baseline := cacheTestSetup(t)
	var fetches atomic.Int64
	m.SetFetchObserver(func(FetchRequest) { fetches.Add(1) })
	u, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(u, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 从已完整写入的测试文件转换，再仅释放尾段，避免 os.Open 自动整文件水合。
	convert, err := proc("CfConvertToPlaceholder")
	if err != nil {
		windows.CloseHandle(h)
		t.Fatal(err)
	}
	id := EncodeIdentity(baseline.RemoteFileID)
	var usn int64
	hr, _, _ := convert.Call(uintptr(h), uintptr(unsafe.Pointer(&id[0])), uintptr(len(id)), 1, uintptr(unsafe.Pointer(&usn)), 0)
	if err = checkHResult(hr, "partial fixture convert"); err != nil {
		windows.CloseHandle(h)
		t.Fatal(err)
	}
	dehydrate, err := proc("CfDehydratePlaceholder")
	if err != nil {
		windows.CloseHandle(h)
		t.Fatal(err)
	}
	hr, _, _ = dehydrate.Call(uintptr(h), 65536, ^uintptr(0), 0, 0)
	windows.CloseHandle(h)
	if err = checkHResult(hr, "partial fixture dehydrate tail"); err != nil {
		t.Fatal(err)
	}
	allocated := allocatedSize(t, path)
	if allocated <= 0 || allocated >= baseline.Size {
		t.Fatalf("fixture must be partial, allocated=%d", allocated)
	}
	baseline.LocalInfo, err = os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	before := fetches.Load()
	baseline.Confirm = func(context.Context) error {
		t.Error("partial cache must not reach remote confirmation")
		return nil
	}
	result, err := m.ReleaseVerifiedCache(context.Background(), baseline)
	if !errors.Is(err, ErrCacheRetained) || result.Released || result.AlreadyOnline {
		t.Fatalf("partial cache accepted: %+v %v", result, err)
	}
	if fetches.Load() != before || allocatedSize(t, path) != allocated {
		t.Fatal("partial-cache scan triggered hydration")
	}
}

func TestVerifiedCacheRejectsHardlinkAndCanceledConfirmation(t *testing.T) {
	t.Run("hardlink", func(t *testing.T) {
		m, path, content, baseline := cacheTestSetup(t)
		if err := os.Link(path, path+".link"); err != nil {
			t.Fatal(err)
		}
		baseline.Confirm = func(context.Context) error {
			t.Error("hardlink must not reach confirmation")
			return nil
		}
		if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err == nil || result.Released {
			t.Fatal("hardlink released")
		}
		if got, _ := os.ReadFile(path + ".link"); !bytes.Equal(got, content) {
			t.Fatal("linked content changed")
		}
	})
	t.Run("canceled-confirmation", func(t *testing.T) {
		m, path, content, baseline := cacheTestSetup(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		baseline.Confirm = func(context.Context) error { cancel(); return nil }
		if result, err := m.ReleaseVerifiedCache(ctx, baseline); !errors.Is(err, context.Canceled) || result.Released {
			t.Fatalf("cancellation ignored: %+v %v", result, err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, content) || allocatedSize(t, path) == 0 {
			t.Fatal("canceled confirmation released local bytes")
		}
	})
}

func TestVerifiedCacheRejectsWritableMapping(t *testing.T) {
	for _, placeholder := range []bool{false, true} {
		name := "ordinary"
		if placeholder {
			name = "hydrated-placeholder"
		}
		t.Run(name, func(t *testing.T) {
			m, path, content, baseline := cacheTestSetup(t)
			if placeholder {
				if _, err := m.ReleaseVerifiedCache(context.Background(), baseline); err != nil {
					t.Fatal(err)
				}
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
					t.Fatalf("mapping fixture hydration: %v", err)
				}
				var err error
				baseline.LocalInfo, err = os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			u, _ := windows.UTF16PtrFromString(path)
			h, err := windows.CreateFile(u, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			mapping, err := windows.CreateFileMapping(h, nil, windows.PAGE_READWRITE, 0, 0, nil)
			windows.CloseHandle(h)
			if err != nil {
				t.Fatal(err)
			}
			addr, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_WRITE, 0, 0, uintptr(len(content)))
			windows.CloseHandle(mapping)
			if err != nil {
				t.Fatal(err)
			}
			defer windows.UnmapViewOfFile(addr)
			move := windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlMoveMemory")
			if err := move.Find(); err != nil {
				t.Fatal(err)
			}
			changed := false
			baseline.Confirm = func(context.Context) error {
				// 文件句柄已关闭，模拟仍存活的编辑器可写映射在校验后修改正文。
				value := content[0] ^ 0xff
				move.Call(addr, uintptr(unsafe.Pointer(&value)), 1)
				changed = true
				return windows.FlushViewOfFile(addr, uintptr(len(content)))
			}
			result, err := m.ReleaseVerifiedCache(context.Background(), baseline)
			if err == nil || result.Released || result.AlreadyOnline {
				t.Fatalf("writable mapping accepted: %+v %v", result, err)
			}
			t.Logf("mapping retained, confirmation reached=%t, reason=%v", changed, err)
			if placeholder && changed {
				meta, openErr := restoreOpen(path, windows.FILE_READ_ATTRIBUTES)
				if openErr != nil {
					t.Fatal(openErr)
				}
				info, _, queryErr := queryCachePlaceholder(meta)
				windows.CloseHandle(meta)
				if queryErr != nil || info.InSyncState == 1 {
					t.Fatalf("unconfirmed mapped edit marked in sync: %+v %v", info, queryErr)
				}
			}
			want := append([]byte(nil), content...)
			if changed {
				want[0] ^= 0xff
			}
			if got, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(got, want) {
				t.Fatalf("mapped edit lost: %v", readErr)
			}
		})
	}
}

func TestVerifiedCacheExternalReopenAndEdit(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	var fetches atomic.Int64
	m.SetFetchObserver(func(FetchRequest) { fetches.Add(1) })
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err != nil || !result.Released {
		t.Fatalf("initial release: %+v %v", result, err)
	}
	quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	// 独立系统进程使用默认 .NET 共享模式，不能只在提供方进程中读回自证成功。
	script := "$ErrorActionPreference='Stop';$f=[IO.File]::Open(" + quoted + ",[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]7);try{$h=[Security.Cryptography.SHA256]::Create();$hash=[BitConverter]::ToString($h.ComputeHash($f)).Replace('-','').ToLowerInvariant();if($hash -ne '" + baseline.SHA256 + "'){throw 'hash mismatch'}}finally{$f.Dispose()};$f=[IO.File]::Open(" + quoted + ",[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]7);try{$f.WriteByte(88);$f.Flush($true)}finally{$f.Dispose()};Write-Output 'REOPEN_EDIT_OK'"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "REOPEN_EDIT_OK") {
		t.Fatalf("external reopen/edit failed: %v context=%v output=%s", err, ctx.Err(), output)
	}
	if fetches.Load() == 0 {
		t.Fatal("external consumer did not hydrate released placeholder")
	}
	baseline.LocalInfo, err = os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	baseline.Confirm = func(context.Context) error {
		t.Error("unuploaded external edit reached remote confirmation")
		return nil
	}
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err == nil || result.Released {
		t.Fatalf("external unuploaded edit released: %+v %v", result, err)
	}
	want := append([]byte(nil), content...)
	want[0] = 'X'
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("external edit lost: %v", err)
	}
	t.Log("EXTERNAL_RELEASE_REOPEN_EDIT_PASS: real PowerShell hash, hydration, edit, unuploaded bytes retained")
}

func TestVerifiedCacheReconnectAndExport(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err != nil || !result.Released {
		t.Fatalf("release before reconnect: %+v %v", result, err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int64
	reconnected, err := mountSpaceForTest(t, MountOptions{RootPath: filepath.Dir(path), Provider: testProvider, SyncRootIdentity: []byte("safe-cache-test"), Policies: &SyncPolicies{Hydration: HydrationPolicy{Primary: 1}, Population: PopulationPolicy{Primary: 3}}, ProtectedPaths: []string{}, List: func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{Path: baseline.RelativePath, Size: baseline.Size, Identity: EncodeIdentity(baseline.RemoteFileID)}}, nil
	}, Open: func(_ context.Context, _ string, off, n int64) (io.ReadCloser, error) {
		fetches.Add(1)
		return io.NopCloser(bytes.NewReader(content[off : off+n])), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if allocatedSize(t, path) != 0 || fetches.Load() != 0 {
		t.Fatal("reconnect downloaded cloud-only bytes")
	}
	if id, err := ReadDirectoryNTFSIdentity(path); err != nil || id != baseline.LocalIdentity {
		t.Fatalf("reconnect changed file identity: %v", err)
	}
	export := filepath.Join(t.TempDir(), "export.bin")
	src, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.Create(export)
	if err != nil {
		src.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(dst, src)
	err = errors.Join(copyErr, dst.Close(), src.Close())
	if err != nil {
		t.Fatal(err)
	}
	baseline.LocalInfo, err = os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconnected.ReleaseVerifiedCache(context.Background(), baseline); err != nil || !result.Released {
		t.Fatalf("release after export: %+v %v", result, err)
	}
	if err = reconnected.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(export); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("export not independent from cloud connection: %v", err)
	}
	h, err := restoreOpen(export, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if _, placeholder, err := queryCachePlaceholder(h); err != nil || placeholder {
		t.Fatalf("explicit export must be ordinary file: placeholder=%t error=%v", placeholder, err)
	}
	t.Log("RECONNECT_EXPORT_PASS: identity and online-only state preserved, explicit ordinary copy survives disconnect")
}

func TestVerifiedCacheChecksContentWhenTimestampUnchanged(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	changed := append([]byte(nil), content...)
	changed[len(changed)-1] ^= 0xff
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, baseline.LocalInfo.ModTime(), baseline.LocalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	baseline.Confirm = func(context.Context) error {
		t.Error("same-size same-time changed bytes must be rejected before confirmation")
		return nil
	}
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err == nil || result.Released {
		t.Fatalf("metadata-only cache acceptance: %+v %v", result, err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, changed) {
		t.Fatalf("timestamp-preserving edit lost: %v", err)
	}
}

func TestVerifiedCacheRejectsForeignCloudIdentity(t *testing.T) {
	m, path, _, baseline := cacheTestSetup(t)
	if _, err := m.ReleaseVerifiedCache(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	// 新清单中的同名文件不能授权回收旧 cloud identity，即使 NTFS 身份仍相同。
	baseline.RemoteFileID = 1173
	m.mu.Lock()
	m.index = map[string]string{"1173": baseline.RelativePath}
	m.mu.Unlock()
	baseline.Confirm = func(context.Context) error {
		t.Error("foreign placeholder must not reach confirmation")
		return nil
	}
	var fetched atomic.Int64
	m.SetFetchObserver(func(FetchRequest) { fetched.Add(1) })
	if result, err := m.ReleaseVerifiedCache(context.Background(), baseline); err == nil || result.Released || result.AlreadyOnline {
		t.Fatalf("foreign cloud identity accepted: %+v %v", result, err)
	}
	if allocatedSize(t, path) != 0 || fetched.Load() != 0 {
		t.Fatal("foreign identity check hydrated file")
	}
}

func TestVerifiedCacheSmallFiles(t *testing.T) {
	for _, n := range []int{0, 1, 7, 256, 1024, 4096} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			m, path, content, baseline := cacheTestSetup(t, bytes.Repeat([]byte("x"), n))
			var fetched atomic.Int64
			m.SetFetchObserver(func(FetchRequest) { fetched.Add(1) })
			result, err := m.ReleaseVerifiedCache(context.Background(), baseline)
			t.Logf("small release result=%+v error=%v allocation-after-close=%d", result, err, allocatedSize(t, path))
			h, openErr := restoreOpen(path, windows.FILE_READ_ATTRIBUTES)
			if openErr != nil {
				t.Fatal(openErr)
			}
			ph, _, queryErr := queryCachePlaceholder(h)
			var standard struct {
				Allocation, End   int64
				Links             uint32
				Delete, Directory byte
				Padding           uint16
			}
			statErr := windows.GetFileInformationByHandleEx(h, windows.FileStandardInfo, (*byte)(unsafe.Pointer(&standard)), uint32(unsafe.Sizeof(standard)))
			windows.CloseHandle(h)
			got, readErr := os.ReadFile(path)
			t.Logf("small info=%+v standard=%+v query=%v stat=%v read=%v fetches=%d equal=%t", ph, standard, queryErr, statErr, readErr, fetched.Load(), bytes.Equal(got, content))
			if readErr != nil || !bytes.Equal(got, content) {
				t.Fatalf("small file readback: %v", readErr)
			}
			if queryErr != nil || statErr != nil || ph.OnDisk != 0 || ph.Modified != 0 || (n > 0 && fetched.Load() == 0) {
				t.Fatal("small file not proved cloud-only by CFAPI and real fetch")
			}
			if err != nil || !result.Released {
				t.Fatal("small file release not confirmed")
			}
		})
	}
}

func TestVerifiedExplicitCacheActions(t *testing.T) {
	m, path, content, baseline := cacheTestSetup(t)
	if err := m.KeepVerifiedCache(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReleaseVerifiedCache(context.Background(), baseline); !errors.Is(err, ErrCacheRetained) {
		t.Fatalf("automatic release ignored pin: %v", err)
	}
	result, err := m.ReleaseVerifiedCacheNow(context.Background(), baseline)
	if err != nil || !result.Released || result.AllocatedAfter != 0 {
		t.Fatalf("explicit release: %+v %v", result, err)
	}
	if err := m.KeepVerifiedCache(context.Background(), baseline); err != nil {
		t.Fatalf("keep online file: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("explicit keep lost bytes: %v", err)
	}
	changed := append([]byte(nil), content...)
	changed[0] ^= 1
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	baseline.LocalInfo, err = os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := m.ReleaseVerifiedCacheNow(context.Background(), baseline); err == nil || result.Released {
		t.Fatal("explicit release accepted unsent modification")
	}
	h, err := restoreOpen(path, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		t.Fatal(err)
	}
	info, _, err := queryCachePlaceholder(h)
	windows.CloseHandle(h)
	if err != nil || info.PinState != 1 {
		t.Fatalf("failed explicit release canceled pin: %+v %v", info, err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, changed) {
		t.Fatal("unsent edit changed")
	}
}
