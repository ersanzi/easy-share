package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func safeName(value string) (string, error) {
	name := filepath.Base(strings.TrimSpace(value))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `\/:*?"<>|`) {
		return "", errors.New("invalid file name")
	}
	return name, nil
}
func availablePath(directory, name string) (string, error) {
	clean, err := safeName(name)
	if err != nil {
		return "", err
	}
	extension := filepath.Ext(clean)
	base := strings.TrimSuffix(clean, extension)
	for index := 0; index < 10000; index++ {
		candidate := clean
		if index > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", base, index, extension)
		}
		path := filepath.Join(directory, candidate)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
	}
	return "", errors.New("no available destination name")
}
