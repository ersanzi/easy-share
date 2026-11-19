//go:build !windows

package shellundo

import (
	"io"
	"time"
)

func Start(root string, callback func([]string) error) (io.Closer, error) { return nil, ErrUnsupported }
func Forward(paths []string, timeout time.Duration) error                 { return ErrUnsupported }
func ValidatePaths(root string, paths []string) ([]string, error)         { return nil, ErrUnsupported }
func UnregisterMenu() error                                               { return ErrUnsupported }
