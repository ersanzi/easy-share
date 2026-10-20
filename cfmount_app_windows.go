//go:build windows

package main

// CfApi 按需占位符的登录态联动（路线图切片 5 的最后一步：接入 app.go）。
//
// 定位：与「此电脑」的 WebDAV 盘（spacemount.go）是**同一份云盘的两种本地呈现**
// （internal/cfmount 包注）。生产上当前主形态仍是 WebDAV（注册表 NameSpace 指 UNC），
// 本文件把 CfApi 挂成一个**普通目录下的同步根**（数据根下的 cloud-drive/<space>），
// 用户可以在资源管理器直接打开它体验「万级文件 0 字节占盘、点开才拉」。
// 两入口共存不冲突：同一份内容，一个走 HTTP 物化、一个走按需水合。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"easyshare/internal/fsutil"
)

// cfapiMounts 持有 CfApi 同步根挂载状态。
//
// 与 spaceMounts（WebDAV）分开管理：生命周期回调点相同（登录挂载/登出卸载/定期同步），
// 但 Close 语义完全不同（CfApi 要断连接 + 注销同步根），不能混在一起。
type cfapiMounts struct {
	mu       sync.Mutex
	personal *cfapi.Mount
	// root 记录同步根本地路径（卸载时即使 Mount 已损也要能注销根目录）。
	personalRoot string
	// listState 是增量列举的跨调用状态：挂载与每轮定期同步**共用一个**，
	// 否则每轮都从「没底」开始、每次全量，增量就白做了。
	listState *cfmount.ListState
	// syncStop 停掉定期同步循环。
	syncStop chan struct{}
	// mounting 标记「正在挂载中」——单飞闸门。
	//
	// 为什么需要：refreshSpaceMounts 会在多个时机被并发触发（登录、配额刷新、
	// 上传完成）。实测日志里一次登录打了两条挂载，第一条撞
	// `CfConnectSyncRoot` 的 0x8007017A（ERROR_CLOUD_FILE_ALREADY_CONNECTED）失败、
	// 第二条成功。结果虽然对（有重复挂载保护），但**日志里留了一条误导性的失败**，
	// 排查时会被当成真故障。挂载是有状态的内核操作，应当串行。
	mounting bool
}

// cfaPiProviderID 个人空间同步根的提供方 GUID（固定不变，换了系统会当成新提供方）。
//
// {C5D8B7A2-3E1F-4A6B-9C2D-EASY0001} 便于在注册表 HKLM\SyncRootManager 里认出来。
var cfapiPersonalProviderID = windows.GUID{
	Data1: 0xC5D8B7A2, Data2: 0x3E1F, Data3: 0x4A6B,
	Data4: [8]byte{0x9C, 0x2D, 0xEA, 0x53, 0x00, 0x01, 0x00, 0x01},
}

// cfapiSyncInterval 定期重新拉清单的间隔。
//
// CfApi 占位符是本地视图，云端在别处改了文件这里不会自动知道——定期 Sync
// 是只读形态下的对账手段（增量列举接上后这里可以换成 /changes 驱动，见切片 2）。
const cfapiSyncInterval = 5 * time.Minute

// mountCfapiSpaces 登录后挂载个人空间的 CfApi 占位符同步根。
//
// 由 refreshSpaceMounts（spacemount.go）在登录态变化时调用。任何失败都只记日志：
// CfApi 是增强形态，挂不上不该影响登录主流程（WebDAV 盘照常挂）。
func (a *App) mountCfapiSpaces() {
	// 旧系统（Win10 1709 之前）没有 cldapi.dll：降级为不挂，WebDAV 不受影响
	if !cfapi.Available() {
		a.logger.Printf("cfapi mount: 系统不支持 Cloud Files API，跳过（WebDAV 盘不受影响）")
		return
	}

	// ── 单飞闸门 ──────────────────────────────────────────────
	//
	// refreshSpaceMounts 会被并发触发（登录、配额刷新、上传完成）。
	// 实测过一次登录打了两条：第一条撞 0x8007017A（已连接）失败、
	// 第二条成功——结果对，但日志里那条失败是**误导性噪声**。
	// 挂载是有状态的内核操作，并发进来只该有一个真干活。
	//
	// 判定必须在**取 client 之前**做：越早挡住，浪费越少。
	a.cfapi.mu.Lock()
	if a.cfapi.personal != nil || a.cfapi.mounting {
		a.cfapi.mu.Unlock()
		return
	}
	a.cfapi.mounting = true
	a.cfapi.mu.Unlock()
	// 无论成功失败都要清标记，否则一次失败会让后续再也不敢挂
	defer func() {
		a.cfapi.mu.Lock()
		a.cfapi.mounting = false
		a.cfapi.mu.Unlock()
	}()

	client, token, err := a.driveClient()
	if err != nil {
		// 未登录/未配置：这不是错误，卸载语义在别处处理
		return
	}
	_ = token // 令牌由 TokenFunc 每次现取，这里只需要 client

	root := filepath.Join(filepath.Dir(a.configPath), "cloud-drive", drive.SpacePersonal)
	if err := os.MkdirAll(root, 0o700); err != nil {
		a.logger.Printf("cfapi mount: 建同步根目录失败: %v", err)
		return
	}

	// 增量状态挂在挂载上：首轮全量拿底，之后每轮只问「那个版本之后发生了啥」
	listState := cfmount.NewListState()

	cfg := cfmount.Config{
		RootPath:     root,
		Space:        drive.SpacePersonal,
		Client:       client,
		Token:        cfmount.TokenFunc(a.sessionToken()),
		ProviderName: "EasyShare 网盘",
		ProviderID:   cfapiPersonalProviderID,
		State:        listState,
	}

	mount, err := cfapi.MountSpace(cfapi.MountOptions{
		RootPath:         cfg.RootPath,
		Provider:         cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "1.0", ID: cfg.ProviderID},
		SyncRootIdentity: []byte("easyshare:personal"),
		List:             cfmount.List(cfg),
		Open:             cfmount.Open(cfg),
	})
	if err != nil {
		a.logger.Printf("cfapi mount: 挂载个人空间占位符失败: %v", err)
		return
	}

	a.cfapi.mu.Lock()
	// 重复挂载保护：refreshSpaceMounts 可能被多次触发（登录/配额变化/上传后），
	// 已挂上就不再重复挂（重复注册同步根会报 ERROR_ALREADY_EXISTS）
	if a.cfapi.personal != nil {
		a.cfapi.mu.Unlock()
		_ = mount.Close()
		return
	}
	a.cfapi.personal = mount
	a.cfapi.personalRoot = root
	a.cfapi.listState = listState
	stop := make(chan struct{})
	a.cfapi.syncStop = stop
	clientRef := client
	a.cfapi.mu.Unlock()

	a.logger.Printf("cfapi mount: 个人空间占位符同步根已挂载: %s", root)
	go a.cfapiSyncLoop(stop, clientRef)
}

// cfapiSyncLoop 定期重新拉云端清单、更新占位符树（含幽灵清理）。
//
// client 在挂载成功时取一次、循环内复用：drive.Client 是无状态 HTTP 客户端，
// 令牌由 TokenFunc 每次现取，会话过期表现为清单拉取失败（下轮重试），不需要换实例。
func (a *App) cfapiSyncLoop(stop chan struct{}, client *drive.Client) {
	ticker := time.NewTicker(cfapiSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.cfapi.mu.Lock()
			mount := a.cfapi.personal
			root := a.cfapi.personalRoot
			state := a.cfapi.listState
			a.cfapi.mu.Unlock()
			if mount == nil {
				return
			}
			if client == nil {
				return
			}
			if err := mount.Sync(cfmount.List(cfmount.Config{
				RootPath:     root,
				Space:        drive.SpacePersonal,
				Client:       client,
				Token:        cfmount.TokenFunc(a.sessionToken()),
				ProviderName: "EasyShare 网盘",
				ProviderID:   cfapiPersonalProviderID,
				State:        state,
			})); err != nil {
				a.logger.Printf("cfapi mount: 定期同步失败（下轮重试）: %v", err)
			}
		}
	}
}

// unmountCfapiSpaces 登出/退出时断开 CfApi 同步根。
func (a *App) unmountCfapiSpaces() {
	a.cfapi.mu.Lock()
	mount := a.cfapi.personal
	root := a.cfapi.personalRoot
	stop := a.cfapi.syncStop
	a.cfapi.personal = nil
	a.cfapi.personalRoot = ""
	// 增量状态一并丢掉：换账号后不能沿用上一个账号的清单与空间游标，
	// 否则会拿别人的版本号去要自己的差异。
	a.cfapi.listState = nil
	a.cfapi.syncStop = nil
	a.cfapi.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if mount == nil {
		return
	}
	if err := mount.Close(); err != nil {
		a.logger.Printf("cfapi mount: 卸载同步根失败: %v", err)
		// Close 失败时再兜底注销一次根目录，避免留下指向已退出进程的注册表残留
		_ = cfapi.UnregisterSyncRoot(root)
		return
	}
	a.logger.Printf("cfapi mount: 个人空间占位符同步根已卸载")
}

// CfapiMountPath 返回个人空间 CfApi 同步根的本地路径（前端「本地挂载」入口用）。
//
// 未挂载返回空串：前端据此隐藏入口，而不是点开报错。
func (a *App) CfapiMountPath() string {
	a.cfapi.mu.Lock()
	defer a.cfapi.mu.Unlock()
	if a.cfapi.personal == nil {
		return ""
	}
	return a.cfapi.personalRoot
}

// OpenCfapiFolder 在资源管理器中打开占位符同步根目录。
//
// 用 OpenFolder 而不是 OpenFile：OpenFile 是「选中并高亮」，对目录会在父目录里
// 选中它，而不是打开它本身。
func (a *App) OpenCfapiFolder() error {
	path := a.CfapiMountPath()
	if path == "" {
		return fmt.Errorf("占位符目录未挂载")
	}
	if err := fsutil.OpenFolder(path); err != nil {
		a.reportError("open cfapi folder", err)
		return err
	}
	return nil
}

// CloudKeepOnDevice 「始终保留在这台电脑上」——钉住并立刻取回内容。
//
// 对标钉盘/WPS 的桌面标配（WPS 取证 §3.5 `freeUpPlaceholder` 的同族动作）。
// 只钉不取是不够的：SDK 明说钉住后平台是**异步**通知提供方拉数据，
// 用户点这个按钮的期望是「现在就在这台电脑上」，所以钉完再 Hydrate 一次。
func (a *App) CloudKeepOnDevice(key string) error {
	path, err := a.placeholderLocalPath(key)
	if err != nil {
		return err
	}
	if err := cfapi.SetPinState(path, cfapi.PinStatePinned, cfapi.SetPinFlagNone); err != nil {
		a.reportError("cfapi keep on device", err)
		return err
	}
	if err := cfapi.Hydrate(path); err != nil {
		a.reportError("cfapi hydrate", err)
		return err
	}
	return nil
}

// CloudFreeUpSpace 「释放空间」——解除钉住后把本地内容退回空壳。
//
// 先解除钉住是刻意的：平台**拒绝**对已钉占位符脱水（真机实测 `0x80070188`，
// 见 state_ops_windows_test.go），若直接 Dehydrate，用户点了「释放空间」只会拿到一个
// 看不懂的错误。云上的文件不受影响，重新点开会自动拉回来。
func (a *App) CloudFreeUpSpace(key string) error {
	path, err := a.placeholderLocalPath(key)
	if err != nil {
		return err
	}
	if err := cfapi.SetPinState(path, cfapi.PinStateUnspecified, cfapi.SetPinFlagNone); err != nil {
		a.reportError("cfapi unpin", err)
		return err
	}
	if err := cfapi.Dehydrate(path); err != nil {
		a.reportError("cfapi dehydrate", err)
		return err
	}
	return nil
}

// placeholderLocalPath 把云端相对路径映射到同步根下的本地路径。
//
// 三道闸：同步根必须已挂载；路径不能是绝对路径、不能含 `..` 段；
// 拼接后的结果必须仍在同步根内。占位符操作是对真实文件系统下手，
// 一个越界的路径就是删/钉用户磁盘上的别的文件。
func (a *App) placeholderLocalPath(key string) (string, error) {
	root := a.CfapiMountPath()
	if root == "" {
		return "", fmt.Errorf("本地挂载未启用")
	}
	if key == "" || strings.HasPrefix(key, "/") || filepath.IsAbs(key) {
		return "", fmt.Errorf("非法路径：%s", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == ".." {
			return "", fmt.Errorf("非法路径：%s", key)
		}
	}
	path := filepath.Join(root, filepath.FromSlash(key))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越过同步根：%s", key)
	}
	return absPath, nil
}
