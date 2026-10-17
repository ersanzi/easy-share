//go:build windows

package cfapi

import (
	"strings"
	"sync"
	"time"
)

// 读取节流（roadmap 切片 5 第 8 步）——为什么必须有这一步：
//
// 回调一旦接上，**本机所有程序**读该目录都会触发下载。索引器
// （`SearchProtocolHost.exe`）与杀软会无差别扫盘：用户装好客户端、第一次打开
// 同步根，就会看到它满速下载整个目录——流量、磁盘、配额一起被吃掉，
// 而用户什么都没做。WPS 为此内置了一份进程名单 + 智能限流器
// （`KSyncEngineHydrationSmartLimiter`，取证 §3.5）。
//
// 本文件是那份限流器的 Go 版：**判据是纯函数**（`decide`/`Delay`），
// 可以在没有 CfApi 的机器上完整单测；只有「在回调里真的 sleep」这一步
// 依赖 Windows。
const (
	// limitWindow 是限流窗口：同一受限进程在窗口内只放行 limitPerWindow 次取数。
	//
	// 取 1 秒 8 次：正常用户打开一个文档只会触发 1–2 次（元数据 + 内容），
	// 不受影响；而逐文件扫盘的工具会被压到 ~8 文件/秒——一万个文件的目录
	// 从「几十秒跑完」变成「二十分钟」，用户有充足时间察觉并处理。
	limitWindow = time.Second
	// limitPerWindow 见上。
	limitPerWindow = 8
	// maxDelay 是单次限流的上限。
	//
	// **不能无限等**：回调是同步的（见 callback_windows.go 的已知局限），
	// 占着回调线程不放会拖垮整个同步根的正常读取。
	// 200ms 足以把扫描速率压下来，又不至于让并发额度被一个扫地工具吃光。
	maxDelay = 200 * time.Millisecond
	// unknownProcessKey 是「拿不到进程名」时用的窗口键。
	// 用一个不可能与真实进程名相同的串，避免与某个进程共用配额。
	unknownProcessKey = "\x00unknown"
)

// 进程名单：**逐字取自 WPS 取证 §3.5**（两段字面量拼接）。
//
// 分两组，对应两种处理：
//
//   - userFacing：用户主动触发的读取（资源管理器、WPS 全家桶、第三方文件浏览器）。
//     **一律放行**——用户双击文件就该立刻打开，慢一点都是体验事故。
//   - background：后台扫描类（杀软、索引、其它网盘客户端）。
//     命中就限流——它们不在乎快慢，而我们很在乎流量。
//
// 名单照抄而不自行增删，是因为它是**实测证据**（WPS 的二进制字面量），
// 自行发明名单等于把「已知有效」换成「我猜的」。要加没关系，
// 但要像 WPS 那样基于真实观测。
var (
	userFacingProcesses = []string{
		"explorer.exe", "kexplorermain.exe",
		"360FileBrowser64.exe", "360FileBrowser.exe",
		"wps.exe", "et.exe", "wpp.exe", "wpspdf.exe", "wpsofd.exe",
		"wpscloudsvr.exe", "wpsphoto+.exe", "photolaunch.exe",
	}
	backgroundProcesses = []string{
		"TQSafeUI.exe", "360rp.exe", "2345VirusScan.exe", "AvastSvc.exe",
		"SysCleanerUI.exe", "UniSensitive.exe", "ksysslim.exe",
		"SearchProtocolHost.exe", "ESCCIndex.exe", "DFSSearchService.exe",
		"svchost.exe", "dllhost.exe",
		"baidunetdisk.exe", "cloud-drive-daemon.exe", "Dropbox.exe", "weiyun_daemon.exe",
	}
)

// Limiter 是读取节流器。零值不可用，用 NewLimiter 创建。
//
// 并发安全：回调可能来自多个线程。
type Limiter struct {
	mu      sync.Mutex
	hits    map[string][]time.Time // 进程名 → 窗口内的取数时刻
	lastMap time.Time              // 清理用，避免 map 无限增长
}

// NewLimiter 创建限流器。
func NewLimiter() *Limiter {
	return &Limiter{hits: map[string][]time.Time{}}
}

// Delay 返回该进程这次取数应当等待多久（0 = 立即放行）。
//
// 判据（按顺序）：
//  1. **用户交互进程一律 0**——用户双击就该立刻打开；
//  2. 名单里的后台扫描进程 → 按窗口配额限流；
//  3. **名单外的未知进程 → 也限流**。
//
// 第 3 条是刻意的：名单是**已知**的扫描工具，而真正的扫描器列表永远是
// 不完整的（Windows 自带索引器的宿主进程会变、第三方工具层出不穷）。
// 把「不认识」当「安全」会让名单一过时就全盘失效；把「不认识」当
// 「可疑」则最坏只是让某个新工具慢一点。**代价不对称，所以取保守一侧**——
// 这与 KI-14 里「漏藏一个状态是删不掉、漏出一行是删了还在」是同一种取舍。
func (l *Limiter) Delay(processName string, now time.Time) time.Duration {
	if l == nil {
		return 0
	}
	name := normalizeProcessName(processName)

	// ① 用户主动触发的读取：不延迟
	if isUserFacing(name) {
		return 0
	}

	// ②③ 后台扫描 / 未知进程：按窗口配额限流
	l.mu.Lock()
	defer l.mu.Unlock()

	// 空进程名先归类，再取该键的窗口记录。
	// ⚠️ 归类必须在**读 map 之前**：写成先读 `l.hits[""]` 再改名，
	// 就会读到一个没人写过的空键（配额永远是 0），空名请求因此完全不受限——
	// 那正好给了扫描器一个绕过口子。本仓的用例抓过这个错。
	if name == "" {
		name = unknownProcessKey
	}

	windowStart := now.Add(-limitWindow)
	kept := l.hits[name][:0]
	for _, t := range l.hits[name] {
		if t.After(windowStart) {
			kept = append(kept, t)
		}
	}
	recent := kept
	l.hits[name] = append(recent, now)
	l.cleanupLocked(now)

	if len(recent) < limitPerWindow {
		return 0 // 配额内：放行
	}
	// 超出配额：等到窗口中最旧的一次过期为止，但不超过 maxDelay。
	oldest := recent[0]
	for _, t := range recent {
		if t.Before(oldest) {
			oldest = t
		}
	}
	wait := oldest.Add(limitWindow).Sub(now)
	if wait <= 0 {
		return 0
	}
	if wait > maxDelay {
		wait = maxDelay
	}
	return wait
}

// cleanupLocked 清掉长时间没有活动的进程条目，避免 map 随见过的进程数无限增长。
func (l *Limiter) cleanupLocked(now time.Time) {
	if now.Sub(l.lastMap) < time.Minute {
		return
	}
	l.lastMap = now
	cutoff := now.Add(-time.Minute)
	for name, times := range l.hits {
		alive := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				alive = append(alive, t)
			}
		}
		if len(alive) == 0 {
			delete(l.hits, name)
			continue
		}
		l.hits[name] = alive
	}
}

// normalizeProcessName 归一化进程名用于比对。
//
// 大小写不敏感：Windows 上同一个程序可能报 "Explorer.EXE"。
func normalizeProcessName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// isUserFacing 判断是否用户主动触发的读取。
func isUserFacing(name string) bool {
	if name == "" {
		return false
	}
	for _, candidate := range userFacingProcesses {
		if strings.EqualFold(name, candidate) {
			return true
		}
	}
	return false
}
