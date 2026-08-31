// Package fsutil provides local filesystem browsing: drive/volume enumeration
// and directory listing for the "我的电脑" file manager feature.
//
// ListDrives 的实现按平台拆分：Windows 枚举逻辑盘符（fsutil_windows.go），
// macOS 枚举已挂载卷（fsutil_darwin.go）。ListDir 与数据类型是跨平台的。
package fsutil

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DriveInfo describes a logical drive (Windows) or mounted volume (macOS).
type DriveInfo struct {
	Letter     string  `json:"letter"` // Windows: "C:"; macOS: 挂载路径如 "/" 或 "/Volumes/xxx"
	Label      string  `json:"label"`  // volume label
	Type       string  `json:"type"`   // fixed, removable, network, cdrom, ramdisk
	TotalBytes int64   `json:"totalBytes"`
	FreeBytes  int64   `json:"freeBytes"`
	UsedPct    float64 `json:"usedPct"` // 0-100
}

// FileEntry describes a single file or directory.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Ext     string    `json:"ext"` // lowercase extension without dot, empty for dirs
}

// ListDir returns the entries in the given directory path, sorted with
// directories first then files, both alphabetically.
func ListDir(dir string) ([]FileEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var result []FileEntry
	for _, entry := range entries {
		// Skip system/hidden entries that clutter the view.
		name := entry.Name()
		if strings.HasPrefix(name, "$") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			// Permission denied or similar — include with minimal info.
			result = append(result, FileEntry{
				Name:  name,
				Path:  filepath.Join(dir, name),
				IsDir: entry.IsDir(),
			})
			continue
		}

		ext := ""
		if !entry.IsDir() {
			ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		}

		result = append(result, FileEntry{
			Name:    name,
			Path:    filepath.Join(dir, name),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Ext:     ext,
		})
	}

	// Sort: directories first, then files; alphabetical within each group.
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}
