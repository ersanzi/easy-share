//go:build windows

package main

import (
	"context"
	"io/fs"
	"path/filepath"
	"sync"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/shellstatus"
)

// nativeSyncStatus 收集写回活动并投递Shell，不能反向影响上传或修改用户正文。
// 云端确认事件与本机可用性独立：收到synced之后仍需CFAPI元数据和版本基线。
type nativeSyncStatus struct {
	root      string
	publisher *shellstatus.Publisher
	mu        sync.Mutex
	latest    map[string]cfmount.WritebackStatus
}

func newNativeSyncStatus(ctx context.Context, root string, logf func(string, ...any)) *nativeSyncStatus {
	return &nativeSyncStatus{root: root, publisher: shellstatus.NewPublisher(ctx, root, logf), latest: map[string]cfmount.WritebackStatus{}}
}

func (s *nativeSyncStatus) record(status cfmount.WritebackStatus) {
	s.mu.Lock()
	s.latest[status.Path] = status
	s.mu.Unlock()
	phase := shellstatus.Phase(status.Phase)
	if status.Phase == cfmount.WritebackSynced {
		// 远端确认后不能在本机内容检查前提前清除同步活动。
		phase = shellstatus.PhaseVerifying
	}
	s.publisher.Publish(shellstatus.Update{Path: filepath.Join(s.root, filepath.FromSlash(status.Path)), Phase: phase, Total: status.Total, Completed: status.Completed, Message: status.Message})
}

func (s *nativeSyncStatus) fetch(path string, total, completed int64, err error) {
	phase := shellstatus.PhaseDownloading
	message := "正在从服务器读取"
	if err != nil {
		phase, message = shellstatus.PhaseFailed, err.Error()
	} else if completed == total {
		phase, message = shellstatus.PhaseSynced, "所需区间读取完成"
	}
	s.publisher.Publish(shellstatus.Update{Path: filepath.Join(s.root, filepath.FromSlash(path)), Phase: phase, Total: total, Completed: completed, Message: message})
}

func nativeStatusPhase(local cfapi.PlaceholderStatus, baseline cfmount.SyncBaseline, activity cfmount.WritebackStatus) shellstatus.Phase {
	if activity.Phase == cfmount.WritebackConflict {
		return shellstatus.PhaseConflict
	}
	if activity.Phase == cfmount.WritebackFailed {
		return shellstatus.PhaseFailed
	}
	if baseline.Pending || local.Modified > 0 {
		return shellstatus.PhasePending
	}
	if activity.Phase == cfmount.WritebackPending || activity.Phase == cfmount.WritebackUploading || activity.Phase == cfmount.WritebackVerifying {
		return shellstatus.Phase(activity.Phase)
	}
	if !local.Placeholder || local.PinState == uint32(cfapi.PinStateExcluded) {
		return shellstatus.PhaseUnknown
	}
	if local.Directory {
		if local.InSyncState == 1 {
			return shellstatus.PhaseSynced
		}
		return shellstatus.PhaseUnknown
	}
	if baseline.FileID > 0 && (local.RemoteID != baseline.FileID || local.Size != baseline.Size || (local.RemoteVersion > 0 && local.RemoteVersion != baseline.Version)) {
		return shellstatus.PhaseConflict
	}
	if local.InSyncState == 1 && local.OnDisk == 0 && !local.FullContent {
		return shellstatus.PhaseSynced
	}
	if local.InSyncState == 1 && local.FullContent && baseline.LocalVerified {
		return shellstatus.PhaseSynced
	}
	return shellstatus.PhaseUnknown
}

func (s *nativeSyncStatus) reconcile(ctx context.Context, m *cfapi.Mount, engine *cfmount.WritebackEngine) {
	baselines := engine.SyncBaselines()
	s.mu.Lock()
	activity := make(map[string]cfmount.WritebackStatus, len(s.latest))
	for p, status := range s.latest {
		activity[p] = status
	}
	s.mu.Unlock()
	paths := map[string]bool{}
	for p := range baselines {
		paths[p] = true
	}
	for p := range activity {
		paths[p] = true
	}
	// 枚举只读目录元数据；普通未知历史项保守显示unknown，不读取正文。
	_ = filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == s.root {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		paths[filepath.ToSlash(rel)] = true
		return nil
	})
	statuses := make(map[string]shellstatus.Phase, len(paths))
	for p := range paths {
		if ctx.Err() != nil {
			return
		}
		baseline := baselines[p]
		local, err := m.QueryPlaceholderStatus(p)
		if err == nil && !baseline.Pending && !baseline.Directory && baseline.LocalVerified && (!local.Placeholder || local.InSyncState != 1 || local.Modified > 0) {
			// 只有本轮完整快照匹配的候选才锁定重查。普通未知文件不能仅按同名转换。
			err = engine.WithCommittedCache(ctx, p, func(ctx context.Context, candidate cfmount.CacheCandidate) error {
				return m.MarkVerifiedCacheSynced(ctx, nativeCacheBaseline(candidate))
			})
			if err == nil {
				local, err = m.QueryPlaceholderStatus(p)
			}
		}
		phase := shellstatus.PhaseUnknown
		if err == nil {
			phase = nativeStatusPhase(local, baseline, activity[p])
		}
		statuses[p] = phase
	}
	// 两次遍历：先收齐子项，再聚合父项，避免map遍历顺序让目录错误被覆盖。
	for p, phase := range statuses {
		for parent := filepath.Dir(filepath.FromSlash(p)); parent != "."; parent = filepath.Dir(parent) {
			key := filepath.ToSlash(parent)
			if nativeStatusPriority(phase) > nativeStatusPriority(statuses[key]) {
				statuses[key] = phase
			}
		}
	}
	for p, phase := range statuses {
		baseline := baselines[p]
		s.publisher.Publish(shellstatus.Update{Path: filepath.Join(s.root, filepath.FromSlash(p)), Phase: phase, Total: baseline.Size, Completed: baseline.Size})
	}
}

func nativeStatusPriority(phase shellstatus.Phase) int {
	switch phase {
	case shellstatus.PhaseFailed, shellstatus.PhaseConflict:
		return 4
	case shellstatus.PhaseUploading, shellstatus.PhaseDownloading, shellstatus.PhasePending, shellstatus.PhaseVerifying:
		return 3
	case shellstatus.PhaseUnknown:
		return 2
	case shellstatus.PhaseSynced:
		return 1
	default:
		return 0
	}
}
