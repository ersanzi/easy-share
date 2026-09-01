//go:build !windows && !darwin

package clipboard

import "fmt"

// startListener 其余平台暂无监听实现（当前目标平台仅 Windows 与 macOS）。
func (s *Service) startListener() error { return ErrUnsupportedPlatform }

// stopListener 占位平台无监听可停。
func (s *Service) stopListener() {}

// Write 占位平台暂不支持回写。
func (s *Service) Write(req WriteRequest) error {
	return fmt.Errorf("当前平台暂不支持剪切板回写")
}
