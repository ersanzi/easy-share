//go:build windows && cfapi_streaming_experiment

package cfapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// 仅供隔离研发：保留失败即失败的策略实验，不把 S_OK 当成消费者成功。
func TestCloudPrimaryStreamingOpenExperiment(t *testing.T) {
	for _, tc := range []struct {
		name              string
		primary, modifier uint16
		synchronous       bool
		native            bool
		nativeFlags       uint32
		compatibility     int
	}{
		{"full-sync", 2, 0, true, false, 0, -1},
		{"progressive-sync", 1, 0, true, false, 0, -1},
		{"stream-sync", 1, 2, true, false, 0, -1},
		{"stream-async", 1, 2, false, false, 0, -1},
		{"stream-native", 1, 2, true, true, 0x80, -1},
		{"full-native-sqos-anonymous", 2, 0, true, true, 0x100000, -1},
		{"progressive-native-sqos-anonymous", 1, 0, true, true, 0x100000, -1},
		{"stream-native-sqos-anonymous", 1, 2, true, true, 0x100000, -1},
		{"stream-native-sqos-identification", 1, 2, true, true, 0x110000, -1},
		{"stream-native-sqos-impersonation", 1, 2, true, true, 0x120000, -1},
		{"stream-expose", 1, 2, true, false, 0, 2},
		{"stream-hide", 1, 2, true, false, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "sample.bin")
			data := bytes.Repeat([]byte("abcdefgh"), 4<<20)
			policy := SyncPolicies{Hydration: HydrationPolicy{Primary: tc.primary, Modifier: tc.modifier}, Population: PopulationPolicy{Primary: PopulationPolicyAlwaysFull}}
			m, err := mountSpaceWithForTest(t, MountOptions{
				RootPath: root, Provider: testProvider, SyncRootIdentity: []byte(tc.name), Policies: &policy, ProtectedPaths: []string{},
				List: func(context.Context) ([]CloudEntry, error) {
					return []CloudEntry{{Path: "sample.bin", Size: int64(len(data)), Identity: EncodeIdentity(9799)}}, nil
				},
				Open: func(context.Context, string, int64, int64) (io.ReadCloser, error) {
					return nil, fmt.Errorf("single-transfer experiment")
				},
			}, mountSpace)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			registered, exists, err := QuerySyncRootStandardInfo(root)
			if err != nil || !exists || registered.Hydration != policy.Hydration {
				t.Fatalf("policy=%+v exists=%v err=%v", registered.Hydration, exists, err)
			}
			t.Logf("root=%s hydration=%+v synchronous=%v", root, registered.Hydration, tc.synchronous)
			m.session.synchronousProbe = tc.synchronous
			m.session.fetcher = func(r FetchRequest) FetchResult {
				t.Logf("FETCH process=%s pid=%d required=%d+%d optional=%d+%d size=%d", r.ProcessName, r.ProcessId, r.RequiredOffset, r.RequiredLength, r.OptionalOffset, r.OptionalLength, r.FileSize)
				return FetchResult{Data: data, Offset: 0}
			}
			m.session.parameterObserver = func(raw []byte) { t.Logf("raw=%x", raw) }
			m.session.transferObserver = func(off, n int64, hr uintptr) { t.Logf("TRANSFER %d+%d hr=%08x", off, n, uint32(hr)) }
			logStreamingPlaceholderInfo(t, path, "before")
			prefix := "$ErrorActionPreference='Stop';Write-Output 'start';"
			if tc.native || tc.compatibility >= 0 {
				prefix += `Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class Probe { [DllImport("ntdll.dll")] public static extern sbyte RtlQueryProcessPlaceholderCompatibilityMode(); [DllImport("ntdll.dll")] public static extern sbyte RtlSetProcessPlaceholderCompatibilityMode(sbyte mode); [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] public static extern IntPtr CreateFileW(string p,uint a,uint s,IntPtr sa,uint d,uint f,IntPtr t); [DllImport("kernel32.dll", SetLastError=true)] public static extern uint GetFileType(IntPtr h); [DllImport("kernel32.dll", SetLastError=true)] public static extern bool ReadFile(IntPtr h,byte[] b,uint n,out uint read,IntPtr ov); [DllImport("kernel32.dll")] public static extern bool CloseHandle(IntPtr h); }';Write-Output ('compatBefore='+[Probe]::RtlQueryProcessPlaceholderCompatibilityMode());`
				if tc.compatibility >= 0 {
					prefix += fmt.Sprintf("[void][Probe]::RtlSetProcessPlaceholderCompatibilityMode(%d);Write-Output ('compatAfter='+[Probe]::RtlQueryProcessPlaceholderCompatibilityMode());", tc.compatibility)
				}
			}
			quoted := strings.ReplaceAll(path, "'", "''")
			script := prefix + fmt.Sprintf(`Write-Output 'opening';$f=[IO.File]::Open('%s',[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]7);Write-Output 'opened';$b=New-Object byte[] 4096;$n=$f.Read($b,0,$b.Length);$f.Dispose();Write-Output ('bytes='+$n);Write-Output ([Convert]::ToBase64String($b));`, quoted)
			if tc.native {
				script = prefix + fmt.Sprintf(`Write-Output 'opening-native flags=%d';$h=[Probe]::CreateFileW('%s',2147483648,7,[IntPtr]::Zero,3,%d,[IntPtr]::Zero);Write-Output ('opened-native='+$h+' error='+[Runtime.InteropServices.Marshal]::GetLastWin32Error());Write-Output ('filetype='+[Probe]::GetFileType($h));$b=New-Object byte[] 4096;[uint32]$n=0;$ok=[Probe]::ReadFile($h,$b,4096,[ref]$n,[IntPtr]::Zero);[void][Probe]::CloseHandle($h);if(!$ok){throw 'ReadFile failed'};Write-Output ('bytes='+$n);`, tc.nativeFlags, quoted, tc.nativeFlags)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
			logStreamingPlaceholderInfo(t, path, "after-consumer")
			if err != nil {
				t.Fatalf("consumer=%s err=%v context=%v output=%s", tc.name, err, ctx.Err(), output)
			}
			if !bytes.Contains(output, []byte("bytes=4096")) {
				t.Fatalf("missing read: %s", output)
			}
			t.Logf("consumer=%s output=%s", tc.name, bytes.Split(output, []byte("YWJj"))[0])
		})
	}
}

func logStreamingPlaceholderInfo(t *testing.T, path, stage string) {
	t.Helper()
	h, err := OpenPlaceholderHandle(path)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)
	p, err := proc("CfGetPlaceholderInfo")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8192)
	var returned uint32
	hr, _, _ := p.Call(uintptr(h), 1, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&returned)))
	if err := checkHResult(hr, "CfGetPlaceholderInfo"); err != nil {
		t.Fatal(err)
	}
	if returned < 64 || returned > uint32(len(buf)) {
		t.Fatalf("bad placeholder info length=%d", returned)
	}
	t.Logf("%s disk=%d validated=%d modified=%d pin=%d inSync=%d allocation=%d", stage, binary.LittleEndian.Uint64(buf[0:8]), binary.LittleEndian.Uint64(buf[8:16]), binary.LittleEndian.Uint64(buf[16:24]), binary.LittleEndian.Uint32(buf[32:36]), binary.LittleEndian.Uint32(buf[36:40]), allocatedSize(t, path))
}
