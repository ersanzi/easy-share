//go:build windows

package cfapi

import (
	"testing"
	"time"
)

// 这批用例覆盖**限流策略本身**，不碰 CfApi——判据是纯函数，
// 所以在任何 Windows 机器上都能跑（不需要 cldapi.dll 可用）。
//
// 为什么值得单独测：这段代码平时**看不出在不在工作**——
// 它只影响"慢一点"，不影响正确性。一旦被改坏（比如误把 explorer.exe
// 归到限流组），症状是「双击文件要等一会儿才打开」，
// 没有任何报错，也没人会想到去查限流器。

func base() time.Time { return time.Unix(1767225600, 0) }

// TestUserFacingProcessIsNeverDelayed 钉住最要紧的一条：
// **用户主动打开文件不能被延迟**。
//
// 限流的存在意义是挡后台扫描，不是挡用户。若 explorer.exe 被限流，
// 用户双击文件会莫名其妙地卡一下——这类"偶发卡顿"极难归因到限流器。
func TestUserFacingProcessIsNeverDelayed(t *testing.T) {
	l := NewLimiter()
	now := base()
	// 连续 100 次（远超配额 8）都必须零延迟
	for i := 0; i < 100; i++ {
		if d := l.Delay("explorer.exe", now.Add(time.Duration(i)*time.Millisecond)); d != 0 {
			t.Fatalf("第 %d 次 explorer.exe 取数被延迟 %v——用户打开文件不该等待", i, d)
		}
	}
}

func TestUserFacingMatchIsCaseInsensitive(t *testing.T) {
	l := NewLimiter()
	if d := l.Delay("Explorer.EXE", base()); d != 0 {
		t.Errorf("大小写不同的 explorer.exe 仍应放行，实得延迟 %v", d)
	}
}

// TestBackgroundScannerIsThrottled 钉住后台扫描被限流。
//
// 这是本功能存在的理由：没有它，用户装好客户端第一次打开同步根，
// 索引器会把整个目录拉下来。
func TestBackgroundScannerIsThrottled(t *testing.T) {
	l := NewLimiter()
	now := base()
	// 前 limitPerWindow 次放行
	for i := 0; i < limitPerWindow; i++ {
		if d := l.Delay("SearchProtocolHost.exe", now); d != 0 {
			t.Fatalf("配额内第 %d 次不应延迟，实得 %v", i, d)
		}
	}
	// 超出配额必须延迟
	if d := l.Delay("SearchProtocolHost.exe", now); d <= 0 {
		t.Errorf("超出配额（%d 次/窗口）后应被延迟，实得 %v", limitPerWindow, d)
	}
}

// TestDelayIsCapped 钉住单次延迟有上限。
//
// 回调是**同步**的：占着回调线程不放会拖垮整个同步根的正常读取。
// 所以限流只能"降速"，不能"阻塞"。
func TestDelayIsCapped(t *testing.T) {
	l := NewLimiter()
	now := base()
	for i := 0; i < limitPerWindow; i++ {
		l.Delay("360rp.exe", now)
	}
	d := l.Delay("360rp.exe", now)
	if d > maxDelay {
		t.Errorf("单次延迟 %v 超过上限 %v——同步回调会被长时间占住", d, maxDelay)
	}
}

// TestQuotaRecoversAfterWindow 钉住窗口过后配额恢复。
//
// 若配额不恢复，一个扫描器扫完第一轮就永久被限——这不是节流是拉黑，
// 而名单里的 svchost.exe / dllhost.exe 是系统进程，永久拉黑会误伤正常读取。
func TestQuotaRecoversAfterWindow(t *testing.T) {
	l := NewLimiter()
	now := base()
	for i := 0; i < limitPerWindow; i++ {
		l.Delay("svchost.exe", now)
	}
	if d := l.Delay("svchost.exe", now); d <= 0 {
		t.Fatalf("前置条件不成立：配额内应被延迟，实得 %v", d)
	}
	// 窗口之后：配额已过期，应重新放行
	if d := l.Delay("svchost.exe", now.Add(limitWindow+time.Millisecond)); d != 0 {
		t.Errorf("窗口过后应恢复配额，实得延迟 %v", d)
	}
}

// TestUnknownProcessIsThrottled 钉住「未知进程按可疑处理」。
//
// 名单是**已知**扫描工具，永远不完整（Windows 索引器宿主进程会变、
// 第三方工具层出不穷）。把「不认识」当「安全」会让名单一过时就全盘失效；
// 把「不认识」当「可疑」最坏只是让某个新工具慢一点。
// **代价不对称，取保守一侧**（与 KI-14 的取舍同源）。
func TestUnknownProcessIsThrottled(t *testing.T) {
	l := NewLimiter()
	now := base()
	for i := 0; i < limitPerWindow; i++ {
		l.Delay("SomeBrandNewIndexer.exe", now)
	}
	if d := l.Delay("SomeBrandNewIndexer.exe", now); d <= 0 {
		t.Error("名单外的进程也应被限流——否则名单过期即全盘失效")
	}
}

// TestUnknownAndKnownHaveSeparateQuotas 钉住配额按进程独立。
//
// 若所有受限进程共用一个窗口计数，一个扫描器就能把配额吃光，
// 进而拖累另一个本来无辜的进程（例如系统 dllhost.exe）。
func TestUnknownAndKnownHaveSeparateQuotas(t *testing.T) {
	l := NewLimiter()
	now := base()
	for i := 0; i < limitPerWindow; i++ {
		l.Delay("av1.exe", now)
	}
	// av1 已超额，但另一个进程的配额不该被它吃掉
	if d := l.Delay("av2.exe", now); d != 0 {
		t.Errorf("不同进程应各有配额，实得延迟 %v", d)
	}
}

// TestEmptyProcessNameStillThrottled 钉住拿不到进程名时仍然限流。
//
// 取不到进程信息是异常路径（本包已置 REQUIRE_PROCESS_INFO），
// 但异常时**不能因此放行**——那正好给了扫描器一个绕过口子。
func TestEmptyProcessNameStillThrottled(t *testing.T) {
	l := NewLimiter()
	now := base()
	for i := 0; i < limitPerWindow; i++ {
		l.Delay("", now)
	}
	if d := l.Delay("", now); d <= 0 {
		t.Error("进程名未知时仍应限流（异常不应成为绕过口子）")
	}
}

// TestListMatchesEvidence 钉住名单与取证一致。
//
// 名单是**实测证据**（WPS 二进制字面量，取证 §3.5）。自行增删等于把
// 「已知有效」换成「我猜的」——若确要调整，应该是基于新的观测，
// 并同时更新这里与取证文档。
func TestListMatchesEvidence(t *testing.T) {
	// 取证 §3.5 的字面量：两组共 29 项
	if got := len(userFacingProcesses); got != 12 {
		t.Errorf("用户侧名单 = %d 项，取证为 12 项", got)
	}
	if got := len(backgroundProcesses); got != 16 {
		t.Errorf("后台名单 = %d 项，取证为 16 项", got)
	}
	// 抽几个关键项存在性（防止被误删）
	for _, name := range []string{"explorer.exe", "SearchProtocolHost.exe", "svchost.exe"} {
		found := false
		for _, c := range append(append([]string{}, userFacingProcesses...), backgroundProcesses...) {
			if c == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("名单缺少 %q（取证 §3.5 明列）", name)
		}
	}
}
