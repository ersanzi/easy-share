//go:build windows

package cfmount

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

// TrustedDirectoryVerifier is deliberately not a directory discovery service.
// Only the explicitly supplied, previously verified sources can be recognized.
// StateDirectory is application-owned, ACL-protected storage outside the root;
// JSON is not a signature and must never be imported from an untrusted party.
// A caller with write access to that trusted store is outside this trust model.
type TrustedDirectoryVerifier struct {
	sources map[string]cfapi.VerifiedDirectoryRestore
}

// NewTrustedDirectoryVerifier does not convert/adopt any directory. Sources
// must originate from the operator-controlled validation that authorized the
// one-shot restore, NOT ordinary Directory=true writeback state or local names.
// Namespace must include the server/account/space scope of the writeback engine.
func NewTrustedDirectoryVerifier(sources []cfapi.VerifiedDirectoryRestore) (*TrustedDirectoryVerifier, error) {
	v := &TrustedDirectoryVerifier{sources: map[string]cfapi.VerifiedDirectoryRestore{}}
	for _, s := range sources {
		rel := filepath.ToSlash(s.RelativePath)
		id, e := strconv.ParseInt(s.RemoteDirectoryFileID, 10, 64)
		if e != nil || id <= 0 || strconv.FormatInt(id, 10) != s.RemoteDirectoryFileID || s.Namespace == "" || !validWritebackPath(rel) || !filepath.IsAbs(s.Root) || filepath.Clean(s.Root) != s.Root || len(filepath.VolumeName(s.Root)) != 2 || strings.HasPrefix(s.Root, `\\`) || strings.TrimRight(s.Root, " .") != s.Root || len(s.PlaceholderIdentity) == 0 || s.ExpectedRoot == (cfapi.DirectoryNTFSIdentity{}) || s.ExpectedDirectory == (cfapi.DirectoryNTFSIdentity{}) || s.ExpectedRoot.VolumeSerial != s.ExpectedDirectory.VolumeSerial {
			return nil, fmt.Errorf("trusted directory: incomplete explicit authorization")
		}
		for _, part := range strings.Split(rel, "/") {
			if strings.TrimRight(part, " .") != part {
				return nil, fmt.Errorf("trusted directory: aliased component")
			}
		}
		if !filepath.IsAbs(s.StateDirectory) || pathWithin(s.Root, s.StateDirectory) {
			return nil, fmt.Errorf("trusted directory: external trusted state required")
		}
		key := s.Namespace + "\x00" + s.Root + "\x00" + rel
		if _, exists := v.sources[key]; exists {
			return nil, fmt.Errorf("trusted directory: duplicate authorization")
		}
		s.PlaceholderIdentity = bytes.Clone(s.PlaceholderIdentity)
		s.AllowedChildren = append([]string(nil), s.AllowedChildren...)
		v.sources[key] = s
	}
	return v, nil
}

// Verify matches WritebackConfig.TrustedDirectory. Unknown/missing provenance
// returns false. Malformed/conflicting provenance returns an error, which the
// scanner treats as protected. No file content is read, written or hydrated.
func (v *TrustedDirectoryVerifier) Verify(ctx context.Context, namespace, root, rel string, remote drive.Object) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s, ok := v.sources[namespace+"\x00"+root+"\x00"+rel]
	if !ok || remote.Path != rel || !remote.Directory || remote.FileId <= 0 || strconv.FormatInt(int64(remote.FileId), 10) != s.RemoteDirectoryFileID {
		return false, nil
	}
	digest := sha256.Sum256([]byte(s.Namespace + "\x00" + strings.ToLower(s.Root) + "\x00" + strings.ToLower(s.RelativePath)))
	recordPath := filepath.Join(s.StateDirectory, "directory-restore-v1-"+hex.EncodeToString(digest[:])+".json")
	// Pin the provenance file; reject symlink/junction records rather than follow.
	recordName, err := windows.UTF16PtrFromString(recordPath)
	if err != nil {
		return false, err
	}
	// Unlike directory metadata handles, pin provenance against writers as well
	// as rename/delete while decoding and checking the authorization.
	h, err := windows.CreateFile(recordName, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	f := os.NewFile(uintptr(h), recordPath)
	defer f.Close()
	var attrs struct{ Attributes, Tag uint32 }
	if err = windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&attrs)), uint32(unsafe.Sizeof(attrs))); err != nil {
		return false, err
	}
	if attrs.Attributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return false, fmt.Errorf("trusted directory: non-ordinary provenance file")
	}
	var record struct {
		Version int                            `json:"version"`
		Kind    string                         `json:"kind"`
		Source  cfapi.VerifiedDirectoryRestore `json:"source"`
		Actual  cfapi.DirectoryNTFSIdentity    `json:"actual"`
		USN     int64                          `json:"usn"`
	}
	decoder := json.NewDecoder(io.LimitReader(f, 65537))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&record); err != nil {
		return false, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return false, fmt.Errorf("trusted directory: trailing provenance")
	}
	a, b := record.Source, s
	a.StateDirectory, b.StateDirectory = "", ""
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if record.Version != 1 || record.Kind != "verified-directory-metadata-conversion" || record.Actual != s.ExpectedDirectory || !bytes.Equal(ab, bb) {
		return false, fmt.Errorf("trusted directory: committed provenance differs from authorization")
	}
	// Every ancestor is pinned against rename/delete while final paths and IDs
	// are verified. Do not follow unknown reparse points anywhere in the chain.
	target := filepath.Join(root, filepath.FromSlash(rel))
	path := filepath.VolumeName(target) + `\`
	paths := []string{path}
	for _, part := range strings.Split(strings.TrimPrefix(target, path), `\`) {
		path = filepath.Join(path, part)
		paths = append(paths, path)
	}
	handles := []windows.Handle{}
	defer func() {
		for i := len(handles) - 1; i >= 0; i-- {
			windows.CloseHandle(handles[i])
		}
	}()
	rootSeen := false
	for _, p := range paths {
		h, e := trustedDirectoryOpen(p, windows.FILE_READ_ATTRIBUTES)
		if e != nil {
			return false, e
		}
		handles = append(handles, h)
		var attr struct{ Attributes, Tag uint32 }
		if e = windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&attr)), uint32(unsafe.Sizeof(attr))); e != nil {
			return false, e
		}
		const unavailable = 0x1000 | 0x40000 | 0x400000
		if attr.Attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attr.Attributes&unavailable != 0 || (attr.Attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && attr.Tag&0xffff0fff != 0x9000001a) {
			return false, fmt.Errorf("trusted directory: unsafe directory chain")
		}
		buf := make([]uint16, 32768)
		n, e := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if e != nil {
			return false, e
		}
		if n >= uint32(len(buf)) || strings.TrimPrefix(windows.UTF16ToString(buf[:n]), `\\?\`) != p {
			return false, fmt.Errorf("trusted directory: final path differs")
		}
		var identity cfapi.DirectoryNTFSIdentity
		if e = windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&identity)), uint32(unsafe.Sizeof(identity))); e != nil {
			return false, e
		}
		if p == root {
			if identity != s.ExpectedRoot {
				return false, fmt.Errorf("trusted directory: root NTFS identity changed")
			}
			rootSeen = true
		}
		if p == target && identity != s.ExpectedDirectory {
			return false, fmt.Errorf("trusted directory: directory NTFS identity changed")
		}
	}
	return rootSeen, nil
}

func trustedDirectoryOpen(path string, access uint32) (windows.Handle, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return windows.InvalidHandle, e
	}
	return windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
}
