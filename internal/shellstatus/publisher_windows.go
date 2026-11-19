//go:build windows

// Package shellstatus 将真实同步活动写入 Explorer 的逐文件 volatile 属性。
// 它不读取正文、不决定上传成功，也不改变 CFAPI pin 或 in-sync 标记。
package shellstatus

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

type Phase string

const (
	PhaseUnknown     Phase = "unknown"
	PhasePending     Phase = "pending"
	PhaseUploading   Phase = "uploading"
	PhaseDownloading Phase = "downloading"
	PhaseVerifying   Phase = "verifying"
	PhaseSynced      Phase = "synced"
	PhaseFailed      Phase = "failed"
	PhaseConflict    Phase = "conflict"
)

type Update struct {
	Path             string
	Phase            Phase
	Total, Completed int64
	Message          string
}

// Publisher 合并每路径的最新状态，COM 调用只发生在自身固定线程。
// 生命周期取消后停止发布，不因隐藏宠物面板取消业务上传。
type Publisher struct {
	root     string
	mu       sync.Mutex
	pending  map[string]Update
	last     map[string]Update
	failures uint64
	lastErr  error
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	logf     func(string, ...any)
}

func NewPublisher(ctx context.Context, root string, logf func(string, ...any)) *Publisher {
	ctx, cancel := context.WithCancel(ctx)
	p := &Publisher{root: filepath.Clean(root), ctx: ctx, cancel: cancel, done: make(chan struct{}), pending: map[string]Update{}, last: map[string]Update{}, logf: logf}
	go p.run()
	return p
}

func within(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (p *Publisher) Publish(update Update) {
	if p == nil || p.ctx.Err() != nil || !within(p.root, update.Path) {
		return
	}
	update.Path = filepath.Clean(update.Path)
	update.Total = max(update.Total, 0)
	update.Completed = min(max(update.Completed, 0), update.Total)
	p.mu.Lock()
	if previous, ok := p.last[update.Path]; ok && previous == update {
		p.mu.Unlock()
		return
	}
	p.pending[update.Path] = update
	p.mu.Unlock()
}

func (p *Publisher) recordFailure(err error) {
	p.mu.Lock()
	first := p.failures == 0
	p.failures++
	p.lastErr = err
	p.mu.Unlock()
	if first && p.logf != nil {
		p.logf("Explorer 同步属性发布失败: %v；业务传输继续，状态显示尚未通过", err)
	}
}

func (p *Publisher) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.done)
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		p.recordFailure(fmt.Errorf("初始化状态 COM: %w", err))
		return
	}
	defer windows.CoUninitialize()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			updates := p.pending
			p.pending = map[string]Update{}
			p.mu.Unlock()
			for path, update := range updates {
				if p.ctx.Err() != nil {
					return
				}
				if err := Apply(update); err != nil {
					p.recordFailure(err)
					continue
				}
				p.mu.Lock()
				p.last[path] = update
				p.mu.Unlock()
			}
		}
	}
}

func (p *Publisher) Failures() (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failures, p.lastErr
}

func (p *Publisher) Close() {
	if p != nil {
		p.cancel()
		<-p.done
	}
}

// TransferFlags 不从完成字节数推断成功；确认阶段仍表示同步活动。
func TransferFlags(phase Phase) uint32 {
	switch phase {
	case PhasePending:
		return 0x1
	case PhaseUploading, PhaseDownloading:
		return 0x4
	case PhaseVerifying:
		return 0x4 | 0x20
	case PhaseFailed, PhaseConflict:
		return 0x10
	case PhaseUnknown:
		return 0x80
	default:
		return 0
	}
}
