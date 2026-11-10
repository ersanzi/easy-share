//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
)

// nativeDirectoryCapabilities 必须基于认证后的显式能力响应实现。
// 具有 CreateDirectory 方法或从 Objects 过滤目录都不能证明服务端支持持久空目录。
// 旧服务端或缺少探测接口时，在创建同步根之前明确拒绝挂载。
type nativeDirectoryCapabilities interface {
	SupportsPersistentDirectories(context.Context, string, string) (bool, error)
}

type nativeCacheController struct {
	Apply func(context.Context, *cfapi.Mount, string, bool) error
}

type nativeCacheControllerKey struct{}

// withNativeCacheController 保持原调度接口不变，为当前挂载世代接上前台缓存动作。
func withNativeCacheController(ctx context.Context, controller *nativeCacheController) context.Context {
	return context.WithValue(ctx, nativeCacheControllerKey{}, controller)
}

func nativeCacheBaseline(c cfmount.CacheCandidate) cfapi.CacheBaseline {
	return cfapi.CacheBaseline{RelativePath: c.RelativePath, RemoteFileID: c.RemoteFileID, Version: c.Version, Size: c.Size, SHA256: c.SHA256, LocalInfo: c.LocalInfo, LocalIdentity: cfapi.DirectoryNTFSIdentity{VolumeSerial: c.LocalIdentity.VolumeSerial, FileID: c.LocalIdentity.FileID}, Confirm: c.Confirm}
}

// lifetime is separate from the short-lived capability-probe timeout. The
// variadic argument preserves existing callers while desktop mounts bind all I/O
// to their cancellable mount session.
func prepareNativeCFAPI(ctx context.Context, cfg cfmount.Config, stateDir, identity string, lifetime ...context.Context) (cfapi.ListFunc, func(*cfapi.Mount) error, error) {
	parent := ctx
	if len(lifetime) > 0 {
		parent = lifetime[0]
	}
	if cfg.Client == nil || cfg.Token == nil || cfg.RootPath == "" || stateDir == "" || identity == "" {
		return nil, nil, fmt.Errorf("CFAPI 可写配置不完整")
	}
	client, ok := cfg.Client.(cfmount.WritebackClient)
	if !ok {
		return nil, nil, fmt.Errorf("CFAPI 客户端缺少带版本条件的写回能力")
	}
	if _, ok := cfg.Client.(cfmount.DirectoryCreator); !ok {
		return nil, nil, fmt.Errorf("CFAPI 客户端缺少持久化目录创建能力")
	}
	probe, ok := cfg.Client.(nativeDirectoryCapabilities)
	if !ok {
		return nil, nil, fmt.Errorf("CFAPI 未启用：客户端缺少可靠 GET capabilities 目录能力探测")
	}
	token := cfg.Token()
	if token == "" {
		return nil, nil, fmt.Errorf("CFAPI 未登录")
	}
	supported, err := probe.SupportsPersistentDirectories(ctx, token, cfg.Space)
	if err != nil {
		return nil, nil, fmt.Errorf("CFAPI 目录能力探测失败: %w", err)
	}
	if !supported {
		return nil, nil, fmt.Errorf("CFAPI 服务端未声明持久化空目录、幂等创建及文件冲突保护能力")
	}
	cfg.Writable = true
	remote := cfmount.List(cfg)
	// 桌面通过能力闸门后才创建同步根；在首次 List 中、创建任何占位符之前初始化写回引擎。
	var mu sync.Mutex
	var engine *cfmount.WritebackEngine
	if controller, ok := ctx.Value(nativeCacheControllerKey{}).(*nativeCacheController); ok {
		controller.Apply = func(request context.Context, m *cfapi.Mount, path string, keep bool) error {
			request, cancel := nativeLifetimeContext(request, parent)
			defer cancel()
			mu.Lock()
			defer mu.Unlock()
			if engine == nil || m == nil {
				return fmt.Errorf("云空间写回引擎未就绪")
			}
			return engine.WithCommittedCache(request, path, func(ctx context.Context, c cfmount.CacheCandidate) error {
				baseline := nativeCacheBaseline(c)
				if keep {
					return m.KeepVerifiedCache(ctx, baseline)
				}
				_, err := m.ReleaseVerifiedCacheNow(ctx, baseline)
				return err
			})
		}
	}
	initial := func(ctx context.Context) ([]cfapi.CloudEntry, error) {
		ctx, cancel := nativeLifetimeContext(ctx, parent)
		defer cancel()
		mu.Lock()
		defer mu.Unlock()
		if engine != nil {
			return nil, fmt.Errorf("CFAPI 初始 List 不可复用；后续必须调用 reconcile")
		}
		writebackConfig, err := nativeWritebackConfig(cfg, stateDir, identity, client)
		if err != nil {
			log.Printf("cfapi mount: 恢复授权加载失败，禁止初始化写回: %v", err)
			return nil, err
		}
		e, err := cfmount.NewWritebackEngine(writebackConfig)
		if err != nil {
			return nil, err
		}
		result, err := e.Tick(ctx)
		if err != nil {
			return nil, errors.Join(err, nativeWritebackIssues(result))
		}
		if !cfapi.ProtectPaths(ctx, result.ProtectedPaths) {
			return nil, fmt.Errorf("CFAPI 初始清单必须通过支持保护路径的 MountSpace 调用")
		}
		// 初始冲突明确记录但不阻止访问已受保护的同步根；后续问题返回桌面日志。
		if issue := nativeWritebackIssues(result); issue != nil {
			log.Printf("cfapi mount: 写回/同步失败，保留本地内容: %v", issue)
		}
		entries, err := remote(ctx)
		if err != nil {
			return nil, err
		}
		engine = e
		return entries, nil
	}
	reconcile := func(m *cfapi.Mount) error {
		mu.Lock()
		defer mu.Unlock()
		if engine == nil || m == nil {
			return fmt.Errorf("CFAPI 写回引擎尚未完成初始保护扫描")
		}
		ctx, cancel := context.WithTimeout(parent, 60*time.Second)
		defer cancel()
		result, err := engine.Reconcile(ctx, func(ctx context.Context, protected []string) error {
			return m.SyncProtected(func(_ context.Context) ([]cfapi.CloudEntry, error) { return remote(ctx) }, protected)
		}, func(ctx context.Context, c cfmount.CacheCandidate) (bool, error) {
			r, err := m.ReleaseVerifiedCache(ctx, nativeCacheBaseline(c))
			if errors.Is(err, cfapi.ErrCacheFileBusy) || errors.Is(err, cfapi.ErrCacheRetained) {
				return false, nil
			}
			return r.Released, err
		})
		return errors.Join(err, nativeWritebackIssues(result))
	}
	return initial, reconcile, nil
}

// Keep CFAPI's request context values (including ProtectPaths), while also
// cancelling initial listing/hydration when the owning login is torn down.
func nativeLifetimeContext(request, lifetime context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(request)
	stop := context.AfterFunc(lifetime, cancel)
	if lifetime.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func nativeWritebackIssues(result cfmount.WritebackResult) error {
	var issues []error
	for _, issue := range result.Issues {
		issues = append(issues, fmt.Errorf("%s [%s]: %s", issue.Path, issue.Kind, issue.Message))
	}
	return errors.Join(issues...)
}
