package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/drive"
	"easyshare/internal/fsutil"
	"easyshare/internal/namespace"
	"easyshare/internal/spacedav"
)

// 「此电脑」里的两个盘：网盘（个人）与共享。
//
// 这里是「两个盘」与空间模型的接合点：登录后按账号**实际拥有的空间**挂载，
// 没有配额的个人空间、没有授权的共享空间都不会出现——挂一个点进去就报错的盘，
// 比不挂更糟。
//
// 每个操作都经控制面，因此配额与共享授权对资源管理器同样生效（不只是客户端内）。
// 个人盘的条目名带登录账号，换账号时「此电脑」里看到的就是当前账号的盘。

// 空间 WebDAV 的端口分配。
//
// 刻意跳过 +1：Core 历史上把已废弃的云盘 WebDAV 挂在 WebDAVPort+1，旧版 Core 进程
// 仍会占用它。撞上时个人空间挂不起来，而症状只是「此电脑里少一个盘」，很难反推到端口。
// 从 +2 起排，与 Core 彻底错开。
const (
	personalPortOffset = 2 // 19082：个人空间
	sharedPortOffset   = 3 // 19083：共享空间
)

// spaceMounts 持有两个空间的 WebDAV 服务与当前已挂载状态。
type spaceMounts struct {
	mu       sync.Mutex
	personal *spacedav.Service
	shared   *spacedav.Service
	// mounted 记录本次已挂上的 CLSID，退出登录时据此精确清理
	mounted map[string]bool
}

func newSpaceMounts() *spaceMounts {
	return &spaceMounts{
		personal: spacedav.NewService(),
		shared:   spacedav.NewService(),
		mounted:  map[string]bool{},
	}
}

// sessionToken 返回一个现取令牌的函数。
//
// 不能捕获令牌字符串：挂载点长期存在，而会话会过期、会换账号。每次操作现取，
// 退出登录后 WebDAV 层立刻拿到空串并拒绝访问。
func (a *App) sessionToken() spacedav.TokenFunc {
	return func() string {
		a.accountMu.RLock()
		defer a.accountMu.RUnlock()
		if a.accountSession == nil {
			return ""
		}
		return a.accountSession.Token
	}
}

// refreshSpaceMounts 按当前登录账号的空间重挂「此电脑」条目，并刷新悬浮窗切换器。
//
// 登录后、以及管理员改了配额或授权之后调用。未登录时全部卸载。
func (a *App) refreshSpaceMounts() {
	// 悬浮窗切换器与挂载读的是同一份空间数据，借登录态通道触发它重读，
	// 免得为此再拉一条 App→托盘 的通路。
	defer a.publishTrayUser()
	a.accountMu.RLock()
	session := a.accountSession
	a.accountMu.RUnlock()

	if session == nil || session.Token == "" {
		a.unmountAllSpaces()
		return
	}

	spaces, err := a.MySpaces()
	if err != nil {
		// 读不到空间就别乱挂：宁可这次不变动，也不要挂出一个连不上的盘
		a.logger.Printf("space mount: read spaces failed: %v", err)
		return
	}

	base := strings.TrimSpace(a.config.PlatformBaseURL)
	if base == "" {
		a.logger.Printf("space mount: 未配置账号服务地址，跳过挂载")
		return
	}
	client := drive.New(base)
	userID := session.User.UserID
	// 昵称优先、退回账号名：条目名要让人一眼认出是谁的盘
	displayName := session.User.NickName
	if displayName == "" {
		displayName = session.User.UserName
	}

	var mounts []namespace.SpaceMount
	var keep = map[string]bool{}

	for _, space := range spaces {
		// 未分配容量 = 待开空间：客户端里显示提示，但不挂盘
		if space.QuotaBytes == account.QuotaUnset {
			continue
		}
		switch space.SpaceType {
		case account.SpacePersonal:
			port := a.config.WebDAVPort + personalPortOffset
			fs := spacedav.New(spacedav.Options{
				Client: client,
				Space:  drive.SpacePersonal,
				Token:  a.sessionToken(),
				Label:  displayName + " 的网盘",
			})
			if err := a.mounts.personal.Start(port, fs); err != nil {
				a.logger.Printf("space mount: personal webdav on %d failed: %v", port, err)
				continue
			}
			mounts = append(mounts, namespace.SpaceMount{
				Kind:        "personal",
				Port:        port,
				UserID:      userID,
				DisplayName: displayName,
				Subtitle:    usageSubtitle(space),
			})
			keep[namespace.PersonalCLSID()] = true

		case account.SpaceShared:
			// 只读授权也挂，但 WebDAV 层拒写：用户能看见团队文件，写入会明确报「拒绝访问」
			readOnly := space.Permission != account.PermWrite
			port := a.config.WebDAVPort + sharedPortOffset
			fs := spacedav.New(spacedav.Options{
				Client:   client,
				Space:    drive.SpaceShared,
				Token:    a.sessionToken(),
				ReadOnly: readOnly,
				Label:    "EasyShare 共享",
			})
			if err := a.mounts.shared.Start(port, fs); err != nil {
				a.logger.Printf("space mount: shared webdav on %d failed: %v", port, err)
				continue
			}
			mounts = append(mounts, namespace.SpaceMount{
				Kind:     "shared",
				Port:     port,
				UserID:   userID,
				Subtitle: usageSubtitle(space),
				ReadOnly: readOnly,
			})
			keep[namespace.SharedCLSID()] = true
		}
	}

	iconPath := namespace.IconFromBuild()
	if len(mounts) > 0 {
		if err := namespace.Register(namespace.SpaceEntries(iconPath, mounts)); err != nil {
			a.logger.Printf("space mount: register entries failed: %v", err)
		}
	}

	// 清掉这次不该出现的条目：配额被收回、共享授权被撤销时，盘要跟着消失
	a.mounts.mu.Lock()
	var stale []namespace.Entry
	for _, clsid := range []string{namespace.PersonalCLSID(), namespace.SharedCLSID()} {
		if !keep[clsid] && a.mounts.mounted[clsid] {
			stale = append(stale, namespace.Entry{CLSID: clsid})
		}
	}
	a.mounts.mounted = keep
	a.mounts.mu.Unlock()

	if len(stale) > 0 {
		if err := namespace.Unregister(stale); err != nil {
			a.logger.Printf("space mount: unregister stale failed: %v", err)
		}
		a.stopUnkeptServices(keep)
	}
	a.logger.Printf("space mount: %d 个空间已挂载", len(mounts))
}

// stopUnkeptServices 停掉本次不再挂载的空间的 WebDAV 端点。
func (a *App) stopUnkeptServices(keep map[string]bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !keep[namespace.PersonalCLSID()] {
		_ = a.mounts.personal.Stop(ctx)
	}
	if !keep[namespace.SharedCLSID()] {
		_ = a.mounts.shared.Stop(ctx)
	}
}

// unmountAllSpaces 退出登录时卸掉两个云端空间（局域网收件箱不受影响）。
func (a *App) unmountAllSpaces() {
	a.mounts.mu.Lock()
	had := len(a.mounts.mounted) > 0
	a.mounts.mounted = map[string]bool{}
	a.mounts.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.mounts.personal.Stop(ctx)
	_ = a.mounts.shared.Stop(ctx)

	if had {
		stale := []namespace.Entry{
			{CLSID: namespace.PersonalCLSID()},
			{CLSID: namespace.SharedCLSID()},
		}
		if err := namespace.Unregister(stale); err != nil {
			a.logger.Printf("space mount: unregister on logout failed: %v", err)
		}
		a.logger.Printf("space mount: 已退出登录，云端空间已卸载")
	}
}

// spaceCLSID 把空间常量映射到「此电脑」里对应条目的 CLSID。
//
// 单独抽出来是为了让「打开哪个盘」与「上传进哪个空间」共用同一处判断：
// 两边各写一份的话，错位后用户以为打开了共享盘、文件却进了个人空间，界面上看不出来。
func spaceCLSID(space string) string {
	if space == drive.SpaceShared {
		return namespace.SharedCLSID()
	}
	return namespace.PersonalCLSID()
}

// spaceLabel 把空间常量翻成界面上的短标签。
//
// 浮窗切换器与拖放区提示都用它，避免两处各写一份而对不上——
// 文案说「共享」而实际传进个人空间，用户是察觉不到的。
func spaceLabel(space string) string {
	if space == drive.SpaceShared {
		return "共享"
	}
	return "网盘"
}

// uploadDroppedToSpace 把拖入悬浮窗的文件上传到当前选中的空间，并把进度写回浮窗。
//
// 与主窗口的拖放走同一套上传逻辑（uploadSingleFile / uploadDir），差别只在
// 目标空间取自浮窗的切换器、且状态回显在浮窗自己的拖放区里。
//
// report 用来回写状态；传 nil 表示不回写（浮窗不可用时）。
func (a *App) uploadDroppedToSpace(paths []string, report func(title, hint, kind string)) {
	if report == nil {
		report = func(string, string, string) {}
	}
	core, driveClient, token, err := a.uploadClients()
	if err != nil {
		// 未登录/未配置是最常见的失败，必须说清楚，不能静默
		report("上传失败", err.Error(), "err")
		return
	}

	space := a.targetSpace()
	label := spaceLabel(space)

	// 先确认目标空间当前真的可写：拖进去再被控制面拒，用户已经等了一轮
	if err := a.assertSpaceWritable(space); err != nil {
		report("无法上传到"+label, err.Error(), "err")
		return
	}

	report("正在上传…", fmt.Sprintf("%d 项 → %s", len(paths), label), "busy")

	var failed int
	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil {
			failed++
			continue
		}
		if info.IsDir() {
			a.uploadDir(core, driveClient, token, space, path)
		} else {
			a.uploadSingleFile(core, driveClient, token, space, path, filepath.Base(path))
		}
	}

	if failed > 0 {
		report("部分未上传", fmt.Sprintf("%d 项读取失败，详见任务中心", failed), "err")
	} else {
		report("已提交上传", fmt.Sprintf("%d 项 → %s，进度见任务中心", len(paths), label), "ok")
	}
	// 用量变了，副标题与切换器上的数字要跟上
	a.refreshSpaceMounts()
}

// assertSpaceWritable 检查目标空间此刻是否可写。
//
// 这只是**提前给出清楚的错误**，不是鉴权：真正的拦截在控制面签发预签名 URL 时。
func (a *App) assertSpaceWritable(space string) error {
	spaces, err := a.MySpaces()
	if err != nil {
		return err
	}
	want := account.SpacePersonal
	if space == drive.SpaceShared {
		want = account.SpaceShared
	}
	for _, s := range spaces {
		if s.SpaceType != want {
			continue
		}
		if s.QuotaBytes == account.QuotaUnset {
			return fmt.Errorf("尚未分配容量，请联系管理员开通")
		}
		if want == account.SpaceShared && s.Permission != account.PermWrite {
			return fmt.Errorf("只有只读权限，不能上传")
		}
		return nil
	}
	return fmt.Errorf("当前账号没有这个空间")
}

// OpenCurrentSpace 打开「此电脑」里当前选中的那个盘。
//
// 滑到哪个空间就打开哪个，与「此电脑」里的条目一一对应：
// 网盘档 → 「<昵称> 的网盘」，共享档 → 「EasyShare 共享」。
//
// 刻意按 CLSID（shell:::{GUID}）打开，而不是打开背后的 WebDAV UNC 路径。
// 两者内容相同，但 UNC 打开后标题栏显示的是 \\127.0.0.1@19082\DavWWWRoot 这种地址、
// 也没有图标，用户会觉得「这不是我那个盘」。按 CLSID 打开的就是此电脑里那一项本身。
func (a *App) OpenCurrentSpace() error {
	space := a.targetSpace()
	clsid := spaceCLSID(space)

	running := a.mounts.personal.Running()
	if space == drive.SpaceShared {
		running = a.mounts.shared.Running()
	}

	// 没挂起来就别开：条目此刻不存在，打开会得到一个空窗口或错误
	if !running {
		err := fmt.Errorf("%s空间未挂载，请确认已登录且已分配容量", spaceLabel(space))
		a.reportError("open current space", err)
		return err
	}

	if err := fsutil.OpenShellLocation("shell:::" + clsid); err != nil {
		a.reportError("open current space", err)
		return err
	}
	a.logger.Printf("opened %s space via %s", space, clsid)
	return nil
}

// SetDropSpace 记下悬浮窗切换器选中的目标空间。由浮窗线程调用。
func (a *App) SetDropSpace(kind string) {
	a.dropSpaceMu.Lock()
	a.dropSpace = kind
	a.dropSpaceMu.Unlock()
	a.logger.Printf("drop target space set to %q", kind)
}

// targetSpace 返回拖入文件应上传到的空间。
//
// 未选择时回落个人空间：这是任何账号开通后都有的那个，比报错更合理。
func (a *App) targetSpace() string {
	a.dropSpaceMu.Lock()
	defer a.dropSpaceMu.Unlock()
	if a.dropSpace == "" {
		return drive.SpacePersonal
	}
	return a.dropSpace
}

// usageSubtitle 生成磁贴副标题。
//
// 资源管理器对注册表型条目不显示可用的容量条（容量取自目标所在物理磁盘，与配额无关），
// 所以配额只能放这行。WPS 也是这么做的。
func usageSubtitle(space account.Space) string {
	if space.QuotaBytes == account.QuotaUnlimited {
		return fmt.Sprintf("已用 %s / 不限", humanBytes(space.UsedBytes))
	}
	return fmt.Sprintf("已用 %s / %s", humanBytes(space.UsedBytes), humanBytes(space.QuotaBytes))
}

// humanBytes 把字节数格式化成便于阅读的单位。
func humanBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(bytes) / 1024
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}
