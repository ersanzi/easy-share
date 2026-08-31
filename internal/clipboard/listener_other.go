//go:build !windows

package clipboard

import "fmt"

// Start 非 Windows 平台暂无监听实现（macOS changeCount 轮询在批次 3）。
func (s *Service) Start() error { return ErrUnsupportedPlatform }

// Stop 停止监听（非 Windows 空实现）。
func (s *Service) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// Write 非 Windows 平台暂不支持回写（随批次 3 的 macOS 监听一起补）。
func (s *Service) Write(req WriteRequest) error {
	return fmt.Errorf("当前平台暂不支持剪切板回写")
}
