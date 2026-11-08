//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"easyshare/internal/fsutil"
	"easyshare/internal/namespace"
	"golang.org/x/sys/windows"
)

// cfapiMounts 的世代在卸载时失效；旧网络请求即使成功也不能再发布入口。
type cfapiMounts struct {
	// lifecycleMu is never acquired while holding mu. Cancellation must precede
	// waiting for mu, because reconcile holds mu during cancellable network I/O.
	lifecycleMu  sync.Mutex
	lifetime     *nativeMountLifetime
	mu           sync.Mutex
	personal     *cfapi.Mount
	personalRoot string
	syncStop     chan struct{}
	mounting     bool
	generation   uint64
	session      *account.Session
	lastError    error
	cacheApply   func(context.Context, *cfapi.Mount, string, bool) error
}

type nativeLifetimeReader struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *nativeLifetimeReader) Close() error {
	r.cancel()
	return r.ReadCloser.Close()
}

type nativeMountLifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newNativeMountLifetime() *nativeMountLifetime {
	ctx, cancel := context.WithCancel(context.Background())
	return &nativeMountLifetime{ctx: ctx, cancel: cancel}
}

// cancelBeforeLock serializes lifecycle replacement with teardown. In particular,
// an old teardown must never cancel a newer mount's context.
func (m *cfapiMounts) cancelBeforeLock(expected *nativeMountLifetime) bool {
	m.lifecycleMu.Lock()
	if expected != nil && m.lifetime != expected {
		m.lifecycleMu.Unlock()
		return false
	}
	if m.lifetime != nil {
		m.lifetime.cancel()
	}
	m.mu.Lock()
	return true // caller releases mu, then lifecycleMu
}

var cfapiPersonalProviderID = windows.GUID{
	Data1: 0xC5D8B7A2, Data2: 0x3E1F, Data3: 0x4A6B,
	Data4: [8]byte{0x9C, 0x2D, 0xEA, 0x53, 0x00, 0x01, 0x00, 0x01},
}

const cfapiSyncInterval = 5 * time.Second

func validateNativeMountIdentity(session *account.Session) error {
	if session == nil || strings.TrimSpace(session.User.UserID) == "" {
		return fmt.Errorf("CFAPI 未挂载：登录会话缺少用户 ID，拒绝使用共用同步根")
	}
	return nil
}

// nativeSpaceIdentity 不含令牌，按服务器、账号、空间隔离本地根和持久化写回状态。
func nativeSpaceIdentity(base, userID, space string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(strings.TrimSpace(base), "/") + "\x00" + userID + "\x00" + space))
	return fmt.Sprintf("%x", sum)
}

// boundSessionToken 只能为创建挂载的那次登录取令牌；同账号重新登录也不能复用旧根回调。
func (a *App) boundSessionToken(session *account.Session, base string) cfmount.TokenFunc {
	return func() string {
		a.accountMu.RLock()
		defer a.accountMu.RUnlock()
		if a.accountSession != session || session == nil || strings.TrimSpace(a.config.PlatformBaseURL) != base {
			return ""
		}
		return session.Token
	}
}

// refreshNativeSpaceMounts 完全接管 Windows 云空间；不影响局域网 WebDAV。
func (a *App) refreshNativeSpaceMounts() bool {
	defer a.publishTrayUser()
	// 清理历史云空间端点，不允许失败时回退 UNC。
	a.stopUnkeptServices(map[string]bool{})
	a.cfapi.mu.Lock()
	stale := []namespace.Entry{{CLSID: namespace.SharedCLSID()}}
	if a.cfapi.personal == nil {
		stale = append(stale, namespace.Entry{CLSID: namespace.PersonalCLSID()})
	}
	_ = namespace.Unregister(stale)
	a.cfapi.mu.Unlock()
	a.mountCfapiSpaces()
	return true
}

func (a *App) mountCfapiSpaces() {
	a.accountMu.RLock()
	session := a.accountSession
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	a.accountMu.RUnlock()
	if session == nil || session.Token == "" || base == "" {
		a.unmountCfapiSpaces()
		return
	}

	if err := validateNativeMountIdentity(session); err != nil {
		a.unmountCfapiSpaces()
		a.cfapi.mu.Lock()
		a.cfapi.lastError = err
		a.cfapi.mu.Unlock()
		a.logger.Printf("cfapi mount: %v", err)
		return
	}

	a.cfapi.mu.Lock()
	expectedRoot := filepath.Join(filepath.Dir(a.configPath), "cloud-drive", nativeSpaceIdentity(base, session.User.UserID, drive.SpacePersonal))
	changed := a.cfapi.session != nil && (a.cfapi.session != session || a.cfapi.personalRoot != expectedRoot)
	a.cfapi.mu.Unlock()
	if changed {
		a.unmountCfapiSpaces()
	}
	a.cfapi.lifecycleMu.Lock()
	a.cfapi.mu.Lock()
	if a.cfapi.mounting || (a.cfapi.personal != nil && a.cfapi.session == session) {
		a.cfapi.mu.Unlock()
		a.cfapi.lifecycleMu.Unlock()
		return
	}
	lifetime := newNativeMountLifetime()
	a.cfapi.lifetime = lifetime
	a.cfapi.mounting = true
	generation := a.cfapi.generation
	a.cfapi.mu.Unlock()
	a.cfapi.lifecycleMu.Unlock()
	published := false
	defer func() {
		a.cfapi.lifecycleMu.Lock()
		a.cfapi.mu.Lock()
		if a.cfapi.lifetime == lifetime {
			a.cfapi.mounting = false
			if !published {
				lifetime.cancel()
				a.cfapi.lifetime = nil
			}
		}
		a.cfapi.mu.Unlock()
		a.cfapi.lifecycleMu.Unlock()
	}()
	fail := func(err error) {
		a.cfapi.mu.Lock()
		if a.cfapi.generation == generation {
			a.cfapi.lastError = err
		}
		a.cfapi.mu.Unlock()
		a.logger.Printf("cfapi mount: %v", err)
	}
	token := a.boundSessionToken(session, base)
	ctx, cancel := context.WithTimeout(lifetime.ctx, 60*time.Second)
	defer cancel()
	spaces, err := account.New(base).MySpaces(ctx, token())
	if err != nil {
		a.unmountCfapiLifetime(lifetime)
		fail(fmt.Errorf("远端空间认证失败: %w", err))
		return
	}
	var personal *account.Space
	for i := range spaces {
		s := &spaces[i]
		if s.SpaceType == account.SpacePersonal && s.QuotaBytes != account.QuotaUnset {
			personal = s
		}
		if s.SpaceType == account.SpaceShared && s.QuotaBytes != account.QuotaUnset {
			// 只读属性不能保护目录新建/删除；没有执行层拒写保障时明确拒绝挂载。
			a.logger.Printf("cfapi mount: 共享空间暂不支持安全挂载（权限 %s），不发布入口", s.Permission)
		}
	}
	if personal == nil {
		a.unmountCfapiLifetime(lifetime)
		return
	}
	if !cfapi.Available() {
		fail(fmt.Errorf("系统不支持 Cloud Files API，不回退云空间 WebDAV"))
		return
	}
	a.cfapi.mu.Lock()
	mounted := a.cfapi.personal != nil && a.cfapi.session == session
	a.cfapi.mu.Unlock()
	if mounted {
		return
	}
	identity := nativeSpaceIdentity(base, session.User.UserID, drive.SpacePersonal)
	root := filepath.Join(filepath.Dir(a.configPath), "cloud-drive", identity)
	stateDir := filepath.Join(filepath.Dir(a.configPath), "cfapi-state", identity)
	client := drive.New(base)
	client.Sessions = drive.NewSessionStore(filepath.Join(stateDir, "upload-sessions"))
	cfg := cfmount.Config{RootPath: root, Space: drive.SpacePersonal, Client: client, Token: token, ProviderName: "EasyShare 网盘", ProviderID: cfapiPersonalProviderID}
	// 能力探测必须先于 Mkdir/注册；旧服务端或尚未接好写回保护时 fail closed。
	controller := &nativeCacheController{}
	list, reconcile, err := prepareNativeCFAPI(withNativeCacheController(ctx, controller), cfg, stateDir, identity, lifetime.ctx)
	if err != nil {
		fail(err)
		return
	}
	if lifetime.ctx.Err() != nil || token() == "" {
		return
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		fail(err)
		return
	}
	openRemote := cfmount.Open(cfg)
	open := func(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
		ctx, cancel := nativeLifetimeContext(ctx, lifetime.ctx)
		reader, err := openRemote(ctx, path, offset, length)
		if err != nil {
			cancel()
			return nil, err
		}
		return &nativeLifetimeReader{ReadCloser: reader, cancel: cancel}, nil
	}
	mount, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "1.0", ID: cfg.ProviderID}, SyncRootIdentity: []byte(identity), List: list, Open: open})
	if err != nil {
		fail(err)
		return
	}

	// 持账号读锁至注册完成，登出必须先改变会话再卸载，因此无法越过此发布屏障。
	a.accountMu.RLock()
	a.cfapi.mu.Lock()
	if lifetime.ctx.Err() != nil || a.cfapi.generation != generation || a.accountSession != session || session.Token == "" || strings.TrimSpace(session.User.UserID) == "" || strings.TrimSpace(a.config.PlatformBaseURL) != base {
		a.cfapi.mu.Unlock()
		a.accountMu.RUnlock()
		_ = mount.Close()
		return
	}
	name := session.User.NickName
	if name == "" {
		name = session.User.UserName
	}
	entries := namespace.SpaceEntries(namespace.IconFromBuild(), []namespace.SpaceMount{{Kind: "personal", TargetPath: root, UserID: session.User.UserID, DisplayName: name, Subtitle: usageSubtitle(*personal)}})
	if err = namespace.Register(entries); err != nil {
		_ = namespace.Unregister(entries)
		a.cfapi.mu.Unlock()
		a.accountMu.RUnlock()
		_ = mount.Close()
		fail(err)
		return
	}
	a.cfapi.personal, a.cfapi.personalRoot, a.cfapi.session = mount, root, session
	a.cfapi.cacheApply = controller.Apply
	a.cfapi.lastError = nil
	stop := make(chan struct{})
	a.cfapi.syncStop = stop
	published = true
	a.cfapi.mu.Unlock()
	a.accountMu.RUnlock()
	a.logger.Printf("cfapi mount: 个人空间已挂载 %s", root)
	go a.cfapiSyncLoop(stop, mount, reconcile)
}

// cfapiSyncLoop 由写回适配器串行执行扫描/写回/保护树同步，不直接调用破坏性 Sync。
func (a *App) cfapiSyncLoop(stop chan struct{}, mount *cfapi.Mount, reconcile func(*cfapi.Mount) error) {
	ticker := time.NewTicker(cfapiSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.cfapi.mu.Lock()
			if a.cfapi.personal != mount {
				a.cfapi.mu.Unlock()
				return
			}
			err := reconcile(mount)
			a.cfapi.mu.Unlock()
			if err != nil {
				a.logger.Printf("cfapi mount: 写回/同步失败，保留本地内容: %v", err)
			}
		}
	}
}

func (a *App) unmountCfapiSpaces() {
	a.unmountCfapiLifetime(nil)
}

func (a *App) unmountCfapiLifetime(expected *nativeMountLifetime) {
	if !a.cfapi.cancelBeforeLock(expected) {
		return
	}
	defer a.cfapi.lifecycleMu.Unlock()
	defer a.cfapi.mu.Unlock()
	a.cfapi.lifetime = nil
	a.cfapi.mounting = false
	a.cfapi.generation++
	// 即使本进程尚未发布入口，也清理上次退出遗留的云空间 UNC 条目。
	_ = namespace.Unregister([]namespace.Entry{{CLSID: namespace.PersonalCLSID()}, {CLSID: namespace.SharedCLSID()}})
	if a.cfapi.syncStop != nil {
		close(a.cfapi.syncStop)
	}
	if a.cfapi.personal != nil {
		if err := a.cfapi.personal.Close(); err != nil {
			a.logger.Printf("cfapi mount: 卸载失败: %v", err)
		}
	}
	a.cfapi.personal, a.cfapi.session = nil, nil
	a.cfapi.cacheApply = nil
	a.cfapi.personalRoot, a.cfapi.syncStop = "", nil
}

func (a *App) CfapiMountPath() string {
	a.cfapi.mu.Lock()
	defer a.cfapi.mu.Unlock()
	if a.cfapi.personal == nil {
		return ""
	}
	return a.cfapi.personalRoot
}

func (a *App) openNativeCurrentSpace() (bool, error) {
	if a.targetSpace() == drive.SpaceShared {
		return true, fmt.Errorf("共享空间尚无安全的 CFAPI 权限执行适配，未挂载")
	}
	return true, a.OpenCfapiFolder()
}

func (a *App) OpenCfapiFolder() error {
	path := a.CfapiMountPath()
	if path == "" {
		a.cfapi.mu.Lock()
		err := a.cfapi.lastError
		a.cfapi.mu.Unlock()
		if err != nil {
			return fmt.Errorf("云空间未挂载: %w", err)
		}
		return fmt.Errorf("云空间未挂载，请确认登录、空间权限及服务器目录能力")
	}
	// 使用已注册的此电脑入口；文件系统路径被 Explorer 参数解析后可能误落到文档目录。
	return fsutil.OpenShellLocation("shell:::" + namespace.PersonalCLSID())
}

// CloudKeepOnDevice 与 CloudFreeUpSpace 保留新版公开接口，但不绕过上传确认和挂载世代。
func (a *App) CloudKeepOnDevice(key string) error { return a.applyNativeCache(key, true) }
func (a *App) CloudFreeUpSpace(key string) error  { return a.applyNativeCache(key, false) }

func (a *App) applyNativeCache(key string, keep bool) error {
	if key == "" || strings.ContainsAny(key, "\\:\x00") || filepath.IsAbs(key) || filepath.ToSlash(filepath.Clean(key)) != key || key == "." || key == ".." || strings.HasPrefix(key, "../") {
		return fmt.Errorf("云文件路径无效")
	}
	a.cfapi.mu.Lock()
	defer a.cfapi.mu.Unlock()
	if a.cfapi.personal == nil || a.cfapi.cacheApply == nil || a.cfapi.lifetime == nil {
		return fmt.Errorf("云空间未连接，不能修改缓存状态")
	}
	ctx, cancel := context.WithTimeout(a.cfapi.lifetime.ctx, 60*time.Second)
	defer cancel()
	return a.cfapi.cacheApply(ctx, a.cfapi.personal, key, keep)
}

func (a *App) nativeDropAvailable(space string) error {
	if space != drive.SpacePersonal {
		return fmt.Errorf("共享云盘尚未支持安全挂载，请在主窗口使用共享空间")
	}
	a.accountMu.RLock()
	session := a.accountSession
	a.accountMu.RUnlock()
	a.cfapi.mu.Lock()
	defer a.cfapi.mu.Unlock()
	if session == nil || a.cfapi.session != session || a.cfapi.personal == nil || a.cfapi.lifetime == nil || a.cfapi.lifetime.ctx.Err() != nil {
		return fmt.Errorf("云盘尚未连接，请等待此电脑入口就绪")
	}
	return nil
}
