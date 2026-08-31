package main

import (
	"strings"
	"testing"

	"easyshare/internal/account"
	"easyshare/internal/namespace"
)

func TestUsageSubtitleShowsQuota(t *testing.T) {
	cases := []struct {
		name  string
		space account.Space
		want  string
	}{
		{
			name:  "有配额时显示已用与上限",
			space: account.Space{UsedBytes: 2 * 1024 * 1024 * 1024, QuotaBytes: 10 * 1024 * 1024 * 1024},
			want:  "已用 2.00 GB / 10.00 GB",
		},
		{
			name:  "不限容量",
			space: account.Space{UsedBytes: 1536, QuotaBytes: account.QuotaUnlimited},
			want:  "已用 1.50 KB / 不限",
		},
		{
			name:  "空空间",
			space: account.Space{UsedBytes: 0, QuotaBytes: 1024},
			want:  "已用 0 B / 1.00 KB",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usageSubtitle(tc.space); got != tc.want {
				t.Errorf("usageSubtitle() = %q，应为 %q", got, tc.want)
			}
		})
	}
}

func TestHumanBytesUnits(t *testing.T) {
	cases := map[int64]string{
		0:             "0 B",
		512:           "512 B",
		1024:          "1.00 KB",
		1024 * 1024:   "1.00 MB",
		1536 * 1024:   "1.50 MB",
		1 << 30:       "1.00 GB",
		1 << 40:       "1.00 TB",
		2 * (1 << 40): "2.00 TB",
	}
	for input, want := range cases {
		if got := humanBytes(input); got != want {
			t.Errorf("humanBytes(%d) = %q，应为 %q", input, got, want)
		}
	}
}

func TestSpaceEntriesOnlyBuildsRequestedMounts(t *testing.T) {
	// 空列表必须产出空结果：没有配额、没有授权的空间不该出现在「此电脑」里
	if entries := namespace.SpaceEntries("icon.exe", nil); len(entries) != 0 {
		t.Fatalf("无空间时不应产出条目，实得 %d", len(entries))
	}

	entries := namespace.SpaceEntries("icon.exe", []namespace.SpaceMount{
		{Kind: "personal", Port: 19081, UserID: "42", Subtitle: "已用 1.00 GB / 10.00 GB"},
	})
	if len(entries) != 1 {
		t.Fatalf("应只产出个人空间一条，实得 %d", len(entries))
	}
	entry := entries[0]
	if entry.CLSID != namespace.PersonalCLSID() {
		t.Errorf("个人空间用错了 CLSID：%s", entry.CLSID)
	}
	// 账号绑定：条目要标明属于哪个账号，换账号时随之更新
	if entry.UserID != "42" {
		t.Errorf("UserID 未透传：%q", entry.UserID)
	}
	if entry.Subtitle == "" {
		t.Error("副标题为空——配额信息只能显示在这里，资源管理器不给用容量条")
	}
	if !strings.Contains(entry.TargetPath, "19081") {
		t.Errorf("目标路径未指向个人空间端口：%s", entry.TargetPath)
	}
}

func TestSharedAndPersonalUseDistinctCLSIDs(t *testing.T) {
	// 三个条目指的是三件不同的事，任意两个共用 CLSID 都会让其中一个被覆盖掉
	personal, shared, lan := namespace.PersonalCLSID(), namespace.SharedCLSID(), namespace.LANCLSID()
	if personal == shared || personal == lan || shared == lan {
		t.Fatalf("CLSID 必须互不相同：personal=%s shared=%s lan=%s", personal, shared, lan)
	}
}

func TestSharedEntryMarksReadOnly(t *testing.T) {
	entries := namespace.SpaceEntries("icon.exe", []namespace.SpaceMount{
		{Kind: "shared", Port: 19082, UserID: "42", ReadOnly: true},
	})
	if len(entries) != 1 {
		t.Fatalf("应产出共享空间一条，实得 %d", len(entries))
	}
	if entries[0].CLSID != namespace.SharedCLSID() {
		t.Errorf("共享空间用错了 CLSID：%s", entries[0].CLSID)
	}
	// 只读要在说明里讲明白，否则用户拖入文件被拒时不知道为什么
	if !strings.Contains(entries[0].Description, "只读") {
		t.Errorf("只读共享空间的说明未标注只读：%q", entries[0].Description)
	}
}

func TestLANEntryIsIndependentOfSpaces(t *testing.T) {
	lan := namespace.LANEntry("icon.exe", 19080)
	if lan.CLSID != namespace.LANCLSID() {
		t.Errorf("局域网条目 CLSID 不对：%s", lan.CLSID)
	}
	// 局域网收件箱不属于任何账号：它是本机目录，不需登录也不受配额约束
	if lan.UserID != "" {
		t.Errorf("局域网条目不该绑账号，实得 %q", lan.UserID)
	}
	// 名字要和云端「共享」区分开——两者曾共用一个名字，是混淆的来源
	if strings.Contains(lan.Name, "共享") {
		t.Errorf("局域网条目名不应含「共享」，避免与云端共享空间混淆：%q", lan.Name)
	}
	if !strings.Contains(lan.Name, "局域网") {
		t.Errorf("局域网条目名应点明局域网：%q", lan.Name)
	}
}
