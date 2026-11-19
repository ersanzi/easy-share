//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"easyshare/internal/account"
)

const hoverTestGB int64 = 1024 * 1024 * 1024

func TestHoverCapacityProjectsRealBytesGBAndPercent(t *testing.T) {
	space := account.Space{SpaceType: account.SpacePersonal, OwnerID: "9007199254740993", UsedBytes: 3 * hoverTestGB / 2, QuotaBytes: 4 * hoverTestGB}
	list := projectHoverSpaces([]account.Space{space}, true)
	if len(list) != 1 || list[0].UsedBytes != space.UsedBytes || list[0].QuotaBytes != space.QuotaBytes || list[0].OwnerID != space.OwnerID {
		t.Fatalf("真实字节/账号ID未投影：%+v", list)
	}
	view, err := projectHoverCapacity(space, true)
	if err != nil || !view.Known || view.UsedGB != "1.50" || view.QuotaGB != "4.00" || view.Percent != 37.5 || !view.BarVisible || !view.UploadEnabled || !view.OpenEnabled {
		t.Fatalf("GB/真实容量百分比错误：%+v err=%v", view, err)
	}
	if view.UsageText != "已用 1.50 GB / 4.00 GB" {
		t.Fatalf("容量文案错误：%q", view.UsageText)
	}
	payload, err := json.Marshal(list[0])
	if err != nil || !strings.Contains(string(payload), `"usedBytes":1610612736`) || !strings.Contains(string(payload), `"quotaBytes":4294967296`) {
		t.Fatalf("数值JSON字段缺失：%s %v", payload, err)
	}
}

func TestHoverCapacityUnsetUnlimitedFullOverquotaAndUnmounted(t *testing.T) {
	cases := []struct {
		name, state                string
		used, quota                int64
		mounted, bar, upload, open bool
		percent                    float64
	}{
		{"未开通", "unset", 0, 0, true, false, false, false, 0},
		{"未开但已有数据", "unset", hoverTestGB, 0, true, false, false, false, 0},
		{"不限", "unlimited", 8 * hoverTestGB, -1, true, false, true, true, 0},
		{"已满", "finite", hoverTestGB, hoverTestGB, true, true, false, true, 100},
		{"超额", "overquota", 3 * hoverTestGB, 2 * hoverTestGB, true, true, false, true, 150},
		{"未挂载", "finite", hoverTestGB, 4 * hoverTestGB, false, true, false, false, 25},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			view, err := projectHoverCapacity(account.Space{SpaceType: account.SpacePersonal, UsedBytes: test.used, QuotaBytes: test.quota}, test.mounted)
			if err != nil || view.State != test.state || view.Percent != test.percent || view.BarVisible != test.bar || view.UploadEnabled != test.upload || view.OpenEnabled != test.open {
				t.Fatalf("特殊配额处理错误：%+v err=%v", view, err)
			}
			if test.quota == -1 && !strings.Contains(view.UsageText, "不限") {
				t.Fatal("不限文案缺失")
			}
			if test.quota == 0 && !strings.Contains(view.UsageText, "未开通") {
				t.Fatal("未开通文案缺失")
			}
			if test.state == "overquota" && !strings.Contains(view.Hint, "已超额 1.00 GB") {
				t.Fatal("超额大小文案缺失")
			}
		})
	}
}

func TestHoverCapacityRejectsInvalidNumbersAndShared(t *testing.T) {
	for _, space := range []account.Space{
		{SpaceType: account.SpacePersonal, UsedBytes: -1, QuotaBytes: 10},
		{SpaceType: account.SpacePersonal, UsedBytes: 0, QuotaBytes: -2},
		{SpaceType: account.SpaceShared, UsedBytes: 1, QuotaBytes: 10},
	} {
		if _, err := projectHoverCapacity(space, true); err == nil {
			t.Fatalf("无效数据被接受：%+v", space)
		}
	}
	view, err := projectHoverCapacity(account.Space{SpaceType: account.SpacePersonal, UsedBytes: math.MaxInt64, QuotaBytes: 1}, true)
	if err != nil || math.IsInf(view.Percent, 0) || math.IsNaN(view.Percent) {
		t.Fatalf("大容量百分比无效：%+v %v", view, err)
	}
}

func TestHoverCapacityOfflineRetainsSameUserAndRecoverClearsStale(t *testing.T) {
	var state hoverCapacityState
	key := hoverAccountIdentity("https://server/", "42")
	first, _ := state.begin(key, true, "旧昵称", "login")
	at := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	spaces := []account.Space{{SpaceType: account.SpacePersonal, OwnerID: "42", UsedBytes: hoverTestGB, QuotaBytes: 4 * hoverTestGB}}
	if !state.complete(first, spaces, true, nil, at) {
		t.Fatal("有效容量未写入")
	}
	version := state.view.AccountVersion
	next, changed := state.begin(key, true, "新昵称", "login")
	if changed || state.view.AccountVersion != version || state.view.Name != "新昵称" {
		t.Fatal("昵称更名改变了容量身份")
	}
	if !state.complete(next, nil, false, errors.New("offline"), at.Add(time.Minute)) || !state.view.Known || !state.view.Stale || state.view.UsedBytes != hoverTestGB || state.view.QuotaBytes != 4*hoverTestGB || state.view.Percent != 25 || state.view.UpdatedAt != at.Format(time.RFC3339) || state.view.UploadEnabled {
		t.Fatalf("断网清零或未标未更新：%+v", state.view)
	}
	if !strings.Contains(state.view.Hint, "未更新") {
		t.Fatal("未更新文案缺失")
	}
	next, _ = state.begin(key, true, "新昵称", "login")
	spaces[0].UsedBytes = 2 * hoverTestGB
	state.complete(next, spaces, true, nil, at.Add(2*time.Minute))
	if state.view.Stale || !state.view.Known || state.view.UsedBytes != 2*hoverTestGB || state.view.Percent != 50 || !state.view.UploadEnabled {
		t.Fatalf("恢复后未更新：%+v", state.view)
	}
}

func TestHoverCapacityAccountServerAndLogoutNeverExposeOldValues(t *testing.T) {
	for _, change := range []struct {
		server, id string
		loggedIn   bool
	}{
		{"https://one", "43", true}, {"https://two", "42", true}, {"", "", false}, {"https://one", "", true},
	} {
		var state hoverCapacityState
		old, _ := state.begin(hoverAccountIdentity("https://one", "42"), true, "同名用户", "login")
		spaces := []account.Space{{SpaceType: account.SpacePersonal, OwnerID: "42", UsedBytes: hoverTestGB, QuotaBytes: 4 * hoverTestGB}}
		state.complete(old, spaces, true, nil, time.Now())
		version := state.view.AccountVersion
		next, changed := state.begin(hoverAccountIdentity(change.server, change.id), change.loggedIn, "同名用户", "login")
		if !changed || state.view.Known || state.view.UsedBytes != 0 || state.view.QuotaBytes != 0 || state.view.BarVisible || state.view.AccountVersion == version {
			t.Fatalf("切换暴露旧容量：%+v", state.view)
		}
		if state.complete(old, spaces, true, nil, time.Now()) {
			t.Fatal("旧回包被应用到新账号")
		}
		state.complete(next, nil, false, errors.New("offline"), time.Now())
		if state.view.Known || state.view.Stale || state.view.UsedBytes != 0 {
			t.Fatal("新账号借用了旧账号离线缓存")
		}
	}
}

func TestHoverCapacityErrorWithoutKnownValueIsNotZeroQuota(t *testing.T) {
	var state hoverCapacityState
	revision, _ := state.begin(hoverAccountIdentity("https://server", "42"), true, "", "fallback-name")
	state.complete(revision, nil, false, errors.New("offline"), time.Now())
	if state.view.Known || state.view.BarVisible || state.view.State != "error" || state.view.Name != "fallback-name" || state.view.UsageText != "容量加载失败" {
		t.Fatalf("加载失败假冒零容量：%+v", state.view)
	}
}

func TestHoverCapacityMismatchedOwnerDuplicateOrMissingPersonalIsRejected(t *testing.T) {
	for _, spaces := range [][]account.Space{
		{{SpaceType: account.SpacePersonal, OwnerID: "other", QuotaBytes: 100}},
		{{SpaceType: account.SpaceShared, QuotaBytes: 100}},
		{{SpaceType: account.SpacePersonal, QuotaBytes: 100}, {SpaceType: account.SpacePersonal, QuotaBytes: 100}},
	} {
		var state hoverCapacityState
		revision, _ := state.begin(hoverAccountIdentity("https://server", "42"), true, "昵称", "")
		state.complete(revision, spaces, true, nil, time.Now())
		if state.view.Known || state.view.UploadEnabled || state.view.State != "error" {
			t.Fatalf("错误个人身份被投影：%+v", state.view)
		}
	}
}

func TestHoverCapacityPopupCachesWithoutWebViewAndNeverSelectsShared(t *testing.T) {
	popup := &hoverPopup{}
	revision := popup.BeginCapacityRefresh("https://server", "42", true, "昵称", "login")
	spaces := []account.Space{{SpaceType: account.SpaceShared, UsedBytes: 1, QuotaBytes: 100, Permission: account.PermWrite}, {SpaceType: account.SpacePersonal, OwnerID: "42", UsedBytes: hoverTestGB, QuotaBytes: 4 * hoverTestGB}}
	popup.CompleteCapacityRefresh(revision, spaces, true, nil)
	if popup.ActiveSpace() != account.SpacePersonal || !popup.capacitySnapshot().Known {
		t.Fatal("个人目标未建立")
	}
	before := popup.capacitySnapshot()
	popup.SetDropStatus("正在上传", "任务进度独立", "busy")
	if popup.capacitySnapshot() != before {
		t.Fatal("上传状态污染容量")
	}
	revision = popup.BeginCapacityRefresh("https://server", "42", true, "昵称", "login")
	popup.CompleteCapacityRefresh(revision, nil, false, errors.New("offline"))
	if !popup.capacitySnapshot().Stale || popup.ActiveSpace() != "" || popup.dropStatus[0] != "正在上传" {
		t.Fatal("离线容量清零/覆盖上传状态/仍开放目标")
	}
	popup.SetSpaces([]hoverSpace{{Kind: account.SpaceShared}})
	popup.handleWebMessage("space-select:shared", nil, nil)
	if popup.ActiveSpace() != "" {
		t.Fatal("共享上传被偷偷开放")
	}
}

func TestHoverCapacityZeroQuotaStaysVisibleOutsideLegacyTargetList(t *testing.T) {
	popup := &hoverPopup{}
	revision := popup.BeginCapacityRefresh("https://server", "42", true, "昵称", "login")
	popup.CompleteCapacityRefresh(revision, []account.Space{{SpaceType: account.SpacePersonal, OwnerID: "42", QuotaBytes: 0}}, true, nil)
	if view := popup.capacitySnapshot(); !view.Known || view.State != "unset" || view.UploadEnabled || popup.ActiveSpace() != "" {
		t.Fatalf("未开容量被丢失：%+v", view)
	}
}

func TestHoverRefreshHookCoalescesIsThreadSafeAndStopsAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	host := &trayHost{ctx: ctx, spaceRefreshCh: make(chan struct{}, 1)}
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); host.RefreshSpaces() }()
	}
	workers.Wait()
	if len(host.spaceRefreshCh) != 1 {
		t.Fatal("后台刷新请求未有界合并")
	}
	<-host.spaceRefreshCh
	cancel()
	host.RefreshSpaces()
	if len(host.spaceRefreshCh) != 0 {
		t.Fatal("退出后仍请求后台刷新")
	}
}

func TestHoverDroppedTerminalRequestsRefreshButBusyDoesNot(t *testing.T) {
	host := &trayHost{spaceRefreshCh: make(chan struct{}, 1)}
	popup := &hoverPopup{}
	host.reportDroppedResult(popup, "正在上传", "", "busy")
	if len(host.spaceRefreshCh) != 0 {
		t.Fatal("上传进度反复请求容量刷新")
	}
	for _, kind := range []string{"ok", "err"} {
		host.reportDroppedResult(popup, "结束", "", kind)
		if len(host.spaceRefreshCh) != 1 || popup.dropStatus[2] != kind {
			t.Fatal("终态未请求容量刷新或未保留原状态")
		}
		<-host.spaceRefreshCh
	}
	popup.onRefreshSpaces = host.RefreshSpaces
	popup.RequestSpacesRefresh()
	if len(host.spaceRefreshCh) != 1 {
		t.Fatal("展开刷新hook未唤醒后台")
	}
}

func TestHoverCapacityUIHasSinglePersonalMeterAndOriginalDropChannel(t *testing.T) {
	for _, forbidden := range []string{"seg-btn", "role=\"tablist\"", "space-select:", "fetch(", "XMLHttpRequest"} {
		if strings.Contains(hoverPopupHTML, forbidden) {
			t.Fatalf("容量页面遗留多空间/网络入口：%s", forbidden)
		}
	}
	for _, required := range []string{"window.applyCapacity", "capacityFill", "view.percent", "共享云盘暂不支持此入口", "postMessageWithAdditionalObjects('file:drop', files)", "window.applyDropStatus", "taskSummary"} {
		if !strings.Contains(hoverPopupHTML, required) {
			t.Fatalf("个人容量/原拖放入口缺失：%s", required)
		}
	}
}

func TestHoverCapacitySameAccountOlderRefreshCannotOverwriteNewer(t *testing.T) {
	popup := &hoverPopup{}
	old := popup.BeginCapacityRefresh("https://server", "42", true, "原昵称", "login")
	newer := popup.BeginCapacityRefresh("https://server", "42", true, "最新昵称", "login")
	popup.CompleteCapacityRefresh(newer, []account.Space{{SpaceType: account.SpacePersonal, OwnerID: "42", UsedBytes: 2 * hoverTestGB, QuotaBytes: 4 * hoverTestGB}}, true, nil)
	before := popup.capacitySnapshot()
	popup.CompleteCapacityRefresh(old, []account.Space{{SpaceType: account.SpacePersonal, OwnerID: "42", UsedBytes: hoverTestGB, QuotaBytes: 4 * hoverTestGB}}, true, nil)
	if popup.capacitySnapshot() != before || before.Name != "最新昵称" || before.Percent != 50 {
		t.Fatal("同账号迟到回包覆盖最新昵称/用量")
	}
}

func TestHoverCapacityLegacySetUserWithoutIDCannotReuseKnownValues(t *testing.T) {
	popup := &hoverPopup{}
	revision := popup.BeginCapacityRefresh("https://server", "42", true, "昵称", "login")
	popup.CompleteCapacityRefresh(revision, []account.Space{{SpaceType: account.SpacePersonal, OwnerID: "42", UsedBytes: hoverTestGB, QuotaBytes: 4 * hoverTestGB}}, true, nil)
	popup.SetUser(true, "另一个人", "other")
	if view := popup.capacitySnapshot(); view.Known || view.UsedBytes != 0 || view.Name != "另一个人" || popup.ActiveSpace() != "" {
		t.Fatalf("旧无身份接口把新昵称拼到旧容量：%+v", view)
	}
}

func TestHoverCapacityMainHookOnlyRefreshesMatchingApp(t *testing.T) {
	first, other := &App{}, &App{}
	matching := &trayHost{app: first, spaceRefreshCh: make(chan struct{}, 1)}
	nonMatching := &trayHost{app: other, spaceRefreshCh: make(chan struct{}, 1)}
	trayHostMu.Lock()
	oldHosts := trayHosts
	trayHosts = map[uintptr]*trayHost{1: matching, 2: nonMatching}
	trayHostMu.Unlock()
	defer func() { trayHostMu.Lock(); trayHosts = oldHosts; trayHostMu.Unlock() }()
	notifyTraySpacesChanged(first)
	if len(matching.spaceRefreshCh) != 1 || len(nonMatching.spaceRefreshCh) != 0 {
		t.Fatal("容量hook没有按App隔离")
	}
}

func TestHoverCapacityConcurrentSnapshotsDoNotMixAccountIdentity(t *testing.T) {
	popup := &hoverPopup{}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 50; j++ {
				id := "42"
				used := hoverTestGB
				if j%2 != 0 {
					id, used = "43", 2*hoverTestGB
				}
				revision := popup.BeginCapacityRefresh("https://server", id, true, id, "")
				popup.CompleteCapacityRefresh(revision, []account.Space{{SpaceType: account.SpacePersonal, OwnerID: id, UsedBytes: used, QuotaBytes: 4 * hoverTestGB}}, true, nil)
				view := popup.capacitySnapshot()
				if view.Known && ((view.UserID == "42" && view.UsedBytes != hoverTestGB) || (view.UserID == "43" && view.UsedBytes != 2*hoverTestGB) || view.Name != view.UserID) {
					t.Errorf("并发快照混用账号：%+v", view)
					return
				}
			}
		}()
	}
	workers.Wait()
}
