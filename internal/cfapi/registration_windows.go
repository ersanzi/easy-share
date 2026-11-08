//go:build windows

package cfapi

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SyncRootStandardInfo is CF_SYNC_ROOT_STANDARD_INFO. STANDARD has no ProviderId:
// never infer a GUID from unrelated fields. Policies contain USHORT pairs.
type SyncRootStandardInfo struct {
	SyncRootFileID  int64
	Hydration       HydrationPolicy
	Population      PopulationPolicy
	InSync          InSyncPolicy
	HardLink        HardlinkPolicy
	ProviderStatus  uint32
	ProviderName    string
	ProviderVersion string
	Identity        []byte
}

// QuerySyncRootStandardInfo reads registration metadata, never file contents.
// registered=false is returned ONLY for ERROR_CLOUD_FILE_NOT_UNDER_SYNC_ROOT.
// The API also accepts descendants; callers must verify SyncRootFileID when
// claiming ownership of the exact supplied directory.
func QuerySyncRootStandardInfo(root string) (info SyncRootStandardInfo, registered bool, err error) {
	p, err := proc("CfGetSyncRootInfoByPath")
	if err != nil {
		return info, false, err
	}
	u, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return info, false, err
	}
	// SDK: IdentityLength at 1052, flexible identity bytes at 1056; max 64 KiB.
	buf := make([]byte, 1056+65536+8)
	var returned uint32
	hr, _, _ := p.Call(uintptr(unsafe.Pointer(u)), 1, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&returned)))
	if uint32(hr) == 0x80070186 {
		return info, false, nil
	} // HRESULT_FROM_WIN32(390)
	if err := checkHResult(hr, "CfGetSyncRootInfoByPath"); err != nil {
		return info, false, err
	}
	info, err = parseSyncRootStandardInfo(buf, returned)
	return info, err == nil, err
}

func parseSyncRootStandardInfo(buf []byte, returned uint32) (SyncRootStandardInfo, error) {
	var out SyncRootStandardInfo
	if returned < 1056 || uint64(returned) > uint64(len(buf)) {
		return out, fmt.Errorf("cfapi: invalid STANDARD returned length %d", returned)
	}
	n := binary.LittleEndian.Uint32(buf[1052:1056])
	if n > 65536 || n > returned-1056 {
		return out, fmt.Errorf("cfapi: invalid STANDARD identity length %d", n)
	}
	text := func(start int) (string, error) {
		units := make([]uint16, 256)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(buf[start+i*2:])
		}
		for _, u := range units {
			if u == 0 {
				return windows.UTF16ToString(units), nil
			}
		}
		return "", fmt.Errorf("cfapi: unterminated STANDARD provider string")
	}
	var err error
	out.ProviderName, err = text(28)
	if err != nil {
		return out, err
	}
	out.ProviderVersion, err = text(540)
	if err != nil {
		return out, err
	}
	out.SyncRootFileID = int64(binary.LittleEndian.Uint64(buf[:8]))
	out.Hydration = HydrationPolicy{binary.LittleEndian.Uint16(buf[8:]), binary.LittleEndian.Uint16(buf[10:])}
	out.Population = PopulationPolicy{binary.LittleEndian.Uint16(buf[12:]), binary.LittleEndian.Uint16(buf[14:])}
	out.InSync = InSyncPolicy(binary.LittleEndian.Uint32(buf[16:]))
	out.HardLink = HardlinkPolicy(binary.LittleEndian.Uint32(buf[20:]))
	out.ProviderStatus = binary.LittleEndian.Uint32(buf[24:])
	out.Identity = append([]byte(nil), buf[1056:1056+n]...)
	return out, nil
}

// ensureSyncRoot preserves persistent registration across normal disconnects.
// A nested root, unknown provider, or different account identity is never adopted.
func ensureSyncRoot(root string, provider ProviderInfo, identity []byte, sr *SyncRootResult) error {
	info, registered, err := QuerySyncRootStandardInfo(root)
	if err != nil {
		return err
	}
	flags := uint32(RegisterFlagDisableOnDemandPopulationOnRoot)
	if registered {
		if info.ProviderName != provider.Name || !bytes.Equal(info.Identity, identity) {
			return fmt.Errorf("cfapi: refusing foreign provider or mismatched sync root identity")
		}
		h, err := OpenPlaceholderHandle(root)
		if err != nil {
			return err
		}
		var local syscall.ByHandleFileInformation
		err = syscall.GetFileInformationByHandle(h, &local)
		syscall.CloseHandle(h)
		if err != nil {
			return err
		}
		id := uint64(local.FileIndexHigh)<<32 | uint64(local.FileIndexLow)
		if id != uint64(info.SyncRootFileID) {
			return fmt.Errorf("cfapi: refusing descendant of an existing sync root")
		}
		flags |= RegisterFlagUpdate
	}
	// UPDATE changes only registration/strategy, not existing placeholder contents.
	return RegisterSyncRoot(root, &sr.Registration, &sr.Policies, flags)
}
