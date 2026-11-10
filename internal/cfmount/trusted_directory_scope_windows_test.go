//go:build windows

package cfmount

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
)

// 不注册同步根，不转换目录；只在临时目录检查授权、提交记录与真实 NTFS 绑定。
func TestTrustedDirectoryExactScopeFailClosed(t *testing.T) {
	root, state := trustedDirectoryTemp(t), trustedDirectoryTemp(t)
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	s := trustedDirectorySource(t, root, state)
	publish := func(source cfapi.VerifiedDirectoryRestore) {
		t.Helper()
		b, e := json.Marshal(map[string]any{"version": 1, "kind": "verified-directory-metadata-conversion", "source": source, "actual": source.ExpectedDirectory, "usn": 0})
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(trustedDirectoryRecordPath(s), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	publish(s)
	remote := drive.Object{Path: "docs", Directory: true, FileId: 701}
	for _, kind := range []string{"valid", "namespace", "root", "path", "remote-path", "remote-file-id", "remote-file", "source-root-id", "source-directory-id", "source-remote-id", "record-remote-id", "record-root"} {
		t.Run(kind, func(t *testing.T) {
			source, r, ns, rt, rel := s, remote, s.Namespace, root, "docs"
			publish(s)
			switch kind {
			case "namespace":
				ns += "other"
			case "root":
				rt = state
			case "path":
				rel = "unknown"
			case "remote-path":
				r.Path = "unknown"
			case "remote-file-id":
				r.FileId++
			case "remote-file":
				r.Directory = false
			case "source-root-id":
				source.ExpectedRoot.FileID[0] ^= 1
				publish(source)
			case "source-directory-id":
				source.ExpectedDirectory.FileID[0] ^= 1
				publish(source)
			case "source-remote-id":
				source.RemoteDirectoryFileID = "702"
				publish(source)
			case "record-remote-id":
				forged := s
				forged.RemoteDirectoryFileID = "702"
				publish(forged)
			case "record-root":
				forged := s
				forged.Root = state
				publish(forged)
			}
			v, e := NewTrustedDirectoryVerifier([]cfapi.VerifiedDirectoryRestore{source})
			if e != nil {
				t.Fatal(e)
			}
			ok, e := v.Verify(context.Background(), ns, rt, rel, r)
			if kind == "valid" {
				if !ok || e != nil {
					t.Fatalf("valid rejected: %v", e)
				}
			} else if ok {
				t.Fatalf("wrong %s accepted", kind)
			}
		})
	}
}
