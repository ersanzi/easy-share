//go:build windows

package cfapi

import (
	"context"
	"os"
	"path"
	"strings"
)

// protectionRequest lets a ListFunc publish its pre-build scan without removing
// entries from the identity index. It is scoped to a single serialized sync.
type protectionRequest struct {
	paths   []string
	enabled bool
}
type protectionKey struct{}

// ProtectPaths publishes initial writeback scan protection to MountSpace's list
// context. Unlike filtering the returned list, this preserves hydration identities.
// The caller must fail the list if this returns false.
func ProtectPaths(ctx context.Context, paths []string) bool {
	p, ok := ctx.Value(protectionKey{}).(*protectionRequest)
	if !ok {
		return false
	}
	p.enabled = true
	p.paths = append(p.paths, paths...)
	return true
}

func protectionPath(p string) string {
	return strings.ToLower(strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/"))
}

// Protect both descendants and ancestors (case-insensitive on Windows).
func pathProtected(p string, protected []string) bool {
	p = protectionPath(p)
	for _, item := range protected {
		q := protectionPath(item)
		if q == "" || q == "." || p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
			return true
		}
	}
	return false
}

// safeCreateParent refuses traversal through reparse points. A concurrent writer
// can win creation, but protected mode never updates an existing entry afterward.
func safeCreateParent(root, rel string) bool {
	for p := path.Dir(strings.ReplaceAll(rel, "\\", "/")); p != "." && p != "/"; p = path.Dir(p) {
		info, err := os.Lstat(joinPath(root, p))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func validProtectedEntry(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\:\x00") || strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}
