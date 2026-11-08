//go:build windows

package cfapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DirectoryNTFSIdentity is FILE_ID_INFO, not a name or a remote file ID.
// Both fields must have been captured while verifying the restore source.
type DirectoryNTFSIdentity struct {
	VolumeSerial uint64   `json:"volumeSerial"`
	FileID       [16]byte `json:"fileId"`
}

// VerifiedDirectoryRestore is an explicit, per-directory authorization supplied
// by the caller after validating its remote listing/namespace. Merely finding a
// directory with the same name is NOT authorization. This API does not perform
// discovery, migrate existing Directory=true state, or adopt arbitrary folders.
// StateDirectory must be an existing, application-owned ACL-protected directory
// OUTSIDE the sync root. Its records must not be accepted from untrusted users.
type VerifiedDirectoryRestore struct {
	Namespace             string                `json:"namespace"`
	Root                  string                `json:"root"`
	RelativePath          string                `json:"relativePath"`
	RemoteDirectoryFileID string                `json:"remoteDirectoryFileId"`
	PlaceholderIdentity   []byte                `json:"placeholderIdentity"`
	ExpectedRoot          DirectoryNTFSIdentity `json:"expectedRoot"`
	ExpectedDirectory     DirectoryNTFSIdentity `json:"expectedDirectory"`
	// nil means no enumeration constraint; non-nil is an exact immediate-child
	// name allowlist (including an explicitly empty list). No child is opened,
	// hydrated, deleted, replaced, or marked in sync by this operation.
	AllowedChildren []string `json:"allowedChildren,omitempty"`
	StateDirectory  string   `json:"-"`
}

// DirectoryRestoreResult.Converted remains true if conversion succeeded but
// verification/persistence failed. Never retry by deleting/recreating that
// directory: stop and reconcile explicitly. RecordPath is nonempty only after
// a successful atomic, no-replace publication of provenance.
type DirectoryRestoreResult struct {
	Converted  bool
	RecordPath string
}

type directoryRestoreRecord struct {
	Version int                      `json:"version"`
	Kind    string                   `json:"kind"`
	Source  VerifiedDirectoryRestore `json:"source"`
	Actual  DirectoryNTFSIdentity    `json:"actual"`
	USN     int64                    `json:"usn"`
}

// ReadDirectoryNTFSIdentity captures an identity, not authorization to restore.
// It does not follow the final reparse point.
func ReadDirectoryNTFSIdentity(path string) (DirectoryNTFSIdentity, error) {
	h, err := restoreOpen(path, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return DirectoryNTFSIdentity{}, err
	}
	defer windows.CloseHandle(h)
	return restoreIdentity(h)
}

func restoreOpen(path string, access uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	// In particular omit FILE_SHARE_DELETE: keep each ancestor pinned against
	// rename/delete until all verification and provenance publication completes.
	return windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

func restoreIdentity(h windows.Handle) (DirectoryNTFSIdentity, error) {
	var id DirectoryNTFSIdentity
	err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id)))
	return id, err
}

func restoreFinalPath(h windows.Handle) (string, error) {
	b := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &b[0], uint32(len(b)), 0)
	if err != nil {
		return "", err
	}
	if n >= uint32(len(b)) {
		return "", fmt.Errorf("restore: final path too long")
	}
	return strings.TrimPrefix(windows.UTF16ToString(b[:n]), `\\?\`), nil
}

func restoreAttributes(h windows.Handle) (uint32, uint32, error) {
	var info struct{ Attributes, Tag uint32 }
	err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return info.Attributes, info.Tag, err
}

func restoreSamePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func validateDirectoryRestore(s VerifiedDirectoryRestore) (string, error) {
	if s.Namespace == "" || s.RemoteDirectoryFileID == "" || len(s.PlaceholderIdentity) == 0 || len(s.PlaceholderIdentity) > 4096 {
		return "", fmt.Errorf("restore: explicit namespace, remote directory ID and identity required")
	}
	if s.ExpectedRoot == (DirectoryNTFSIdentity{}) || s.ExpectedDirectory == (DirectoryNTFSIdentity{}) {
		return "", fmt.Errorf("restore: verified root and directory NTFS identities required")
	}
	// Deliberately support local drive paths only; no UNC/device paths, ADS,
	// drive-relative paths, normalization aliases or traversal components.
	if !filepath.IsAbs(s.Root) || len(filepath.VolumeName(s.Root)) != 2 || filepath.Clean(s.Root) != s.Root || strings.HasPrefix(s.Root, `\\`) {
		return "", fmt.Errorf("restore: canonical local absolute root required")
	}
	rel := s.RelativePath
	if rel == "" || strings.ContainsAny(rel, "/:\x00") || filepath.IsAbs(rel) || filepath.Clean(rel) != rel {
		return "", fmt.Errorf("restore: exact relative directory path required")
	}
	for _, c := range strings.Split(rel, `\`) {
		if c == "" || c == "." || c == ".." || strings.TrimRight(c, " .") != c {
			return "", fmt.Errorf("restore: unsafe path component")
		}
	}
	state, err := filepath.Abs(s.StateDirectory)
	if err != nil || s.StateDirectory == "" {
		return "", fmt.Errorf("restore: external state directory required")
	}
	r, err := filepath.Rel(s.Root, state)
	if err != nil || (r != ".." && !strings.HasPrefix(r, `..\`)) {
		return "", fmt.Errorf("restore: state directory must be outside root")
	}
	return filepath.Join(s.Root, rel), nil
}

// RestoreVerifiedDirectory converts ONLY the explicitly authorized ordinary
// directory using CfConvertToPlaceholder with flags=0. It never calls
// CfCreatePlaceholders, CfSetInSyncState, hydration, deletion or unregister.
// A reparse target (including an already converted target) fails closed.
func RestoreVerifiedDirectory(s VerifiedDirectoryRestore) (result DirectoryRestoreResult, err error) {
	target, err := validateDirectoryRestore(s)
	if err != nil {
		return result, err
	}
	p, err := proc("CfConvertToPlaceholder")
	if err != nil {
		return result, err
	}
	// Pin every ancestor from the volume root, not just the final directory.
	volume := filepath.VolumeName(target)
	current := volume + `\`
	parts := strings.Split(strings.TrimPrefix(target, current), `\`)
	paths := []string{current}
	for _, part := range parts {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	handles := make([]windows.Handle, 0, len(paths))
	defer func() {
		for i := len(handles) - 1; i >= 0; i-- {
			windows.CloseHandle(handles[i])
		}
	}()
	var targetHandle windows.Handle
	var before DirectoryNTFSIdentity
	rootSeen := false
	for i, path := range paths {
		access := uint32(windows.FILE_READ_ATTRIBUTES)
		if i == len(paths)-1 {
			access |= windows.FILE_WRITE_DATA
		}
		h, e := restoreOpen(path, access)
		if e != nil {
			return result, fmt.Errorf("restore: pin %q: %w", path, e)
		}
		handles = append(handles, h)
		attrs, tag, e := restoreAttributes(h)
		if e != nil {
			return result, e
		}
		if attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return result, fmt.Errorf("restore: non-directory ancestor")
		}
		reparse := attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
		// Only known cloud tags may occur in ancestors, never in the target.
		if reparse && (i == len(paths)-1 || tag&0xffff0fff != 0x9000001a) {
			return result, fmt.Errorf("restore: reparse point refused: %q", path)
		}
		final, e := restoreFinalPath(h)
		if e != nil {
			return result, e
		}
		if !restoreSamePath(final, path) {
			return result, fmt.Errorf("restore: final path escaped verified chain: got %q, expected %q", final, path)
		}
		id, e := restoreIdentity(h)
		if e != nil {
			return result, e
		}
		if restoreSamePath(path, s.Root) {
			if id != s.ExpectedRoot {
				return result, fmt.Errorf("restore: root identity changed")
			}
			rootSeen = true
		}
		if i == len(paths)-1 {
			targetHandle, before = h, id
		}
	}
	if !rootSeen || before != s.ExpectedDirectory || before.VolumeSerial != s.ExpectedRoot.VolumeSerial {
		return result, fmt.Errorf("restore: directory identity changed")
	}
	if s.AllowedChildren != nil {
		entries, e := os.ReadDir(target)
		if e != nil {
			return result, e
		}
		allowed := make(map[string]bool, len(s.AllowedChildren))
		for _, name := range s.AllowedChildren {
			if allowed[name] {
				return result, fmt.Errorf("restore: duplicate child authorization")
			}
			allowed[name] = true
		}
		if len(entries) != len(allowed) {
			return result, fmt.Errorf("restore: child enumeration differs")
		}
		for _, entry := range entries {
			if !allowed[entry.Name()] {
				return result, fmt.Errorf("restore: unauthorized child %q", entry.Name())
			}
		}
	}
	// Name is a digest of authorization scope, not user-controlled path syntax.
	key := sha256.Sum256([]byte(s.Namespace + "\x00" + strings.ToLower(s.Root) + "\x00" + strings.ToLower(s.RelativePath)))
	recordPath := filepath.Join(s.StateDirectory, "directory-restore-v1-"+hex.EncodeToString(key[:])+".json")
	if _, e := os.Lstat(recordPath); !os.IsNotExist(e) {
		return result, fmt.Errorf("restore: provenance exists or cannot be inspected")
	}
	// Preflight persistence before modifying filesystem metadata. No mkdir and no
	// overwrite: callers must provision the trusted state directory themselves.
	tmp, err := os.CreateTemp(s.StateDirectory, ".directory-restore-*.tmp")
	if err != nil {
		return result, err
	}
	tmpName := tmp.Name()
	defer func() { tmp.Close(); os.Remove(tmpName) }()
	var usn int64
	hr, _, _ := p.Call(uintptr(targetHandle), uintptr(unsafe.Pointer(&s.PlaceholderIdentity[0])), uintptr(uint32(len(s.PlaceholderIdentity))),
		0, uintptr(unsafe.Pointer(&usn)), 0)
	runtime.KeepAlive(s.PlaceholderIdentity)
	if int32(hr) < 0 {
		return result, fmt.Errorf("restore: CfConvertToPlaceholder HRESULT 0x%08x", uint32(hr))
	}
	result.Converted = true
	// With flags=0 CFAPI may leave a directory without a reparse tag (fully
	// populated metadata-only directory). Do not infer provenance from attrs;
	// only the successful HRESULT plus this explicit record authorizes it.
	after, err := restoreIdentity(targetHandle)
	if err != nil {
		return result, err
	}
	if before != after {
		return result, fmt.Errorf("restore: NTFS identity changed after conversion")
	}
	final, err := restoreFinalPath(targetHandle)
	if err != nil {
		return result, err
	}
	if !restoreSamePath(final, target) {
		return result, fmt.Errorf("restore: final path changed after conversion")
	}
	record := directoryRestoreRecord{Version: 1, Kind: "verified-directory-metadata-conversion", Source: s, Actual: after, USN: usn}
	if err = json.NewEncoder(tmp).Encode(record); err != nil {
		return result, err
	}
	if err = tmp.Sync(); err != nil {
		return result, err
	}
	if err = tmp.Close(); err != nil {
		return result, err
	}
	from, err := windows.UTF16PtrFromString(tmpName)
	if err != nil {
		return result, err
	}
	to, err := windows.UTF16PtrFromString(recordPath)
	if err != nil {
		return result, err
	}
	// WRITE_THROUGH without REPLACE_EXISTING: atomic publication fails safely if
	// another writer already published the record. Never overwrite provenance.
	if err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return result, err
	}
	result.RecordPath = recordPath
	return result, nil
}
