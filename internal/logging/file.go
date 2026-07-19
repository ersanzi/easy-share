package logging

import (
	"fmt"
	"os"
	"path/filepath"
)

const maxLogSize int64 = 5 << 20

// Directory returns the per-user EasyShare log directory.
func Directory() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if cache, err := os.UserCacheDir(); err == nil {
			base = cache
		} else {
			base = "."
		}
	}
	return filepath.Join(base, "EasyShare", "logs")
}

func Path(name string) string { return filepath.Join(Directory(), name) }

// Open creates an append-only log file and keeps one rotated 5 MiB backup.
func Open(name string) (*os.File, string, error) {
	directory := Directory()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(directory, filepath.Base(name))
	if info, err := os.Stat(path); err == nil && info.Size() >= maxLogSize {
		backup := path + ".1"
		_ = os.Remove(backup)
		if err := os.Rename(path, backup); err != nil {
			return nil, "", fmt.Errorf("rotate log: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open log: %w", err)
	}
	return file, path, nil
}
