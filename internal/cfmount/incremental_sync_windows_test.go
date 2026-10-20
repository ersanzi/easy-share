//go:build windows

package cfmount

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
)

// 真实 CfApi 挂载 + 增量列举：证明「占位符树」能被差异事件正确更新，
// 而不是只把游标往前推。单测只看请求形态，看不出「新文件有没有真的变成占位符」——
// 而本包的教训恰恰是「绿不等于对」（增量游标用错时症状是文件静默消失）。

// changeCapableClient 在集成测试的 fakeClient 上补出 Changes 能力。
//
// 事件表由用例追加，模拟「云端新建了一个文件」这一最常见的变更形态。
type changeCapableClient struct {
	*fakeClient
	version  int64
	pending  []drive.Change
	cursors  []*int64
	changeNs int
}

func (c *changeCapableClient) Changes(_ context.Context, _, _ string, since *int64) (*drive.Changes, error) {
	c.changeNs++
	c.cursors = append(c.cursors, since)
	if since == nil {
		// 全量重建：给完整视图 + 当前版本
		c.version = 100
		return &drive.Changes{Files: c.objects, NewestSpaceVersion: drive.FlexInt64(c.version)}, nil
	}
	// 增量：只回攒下的差异，并把版本推进
	out := &drive.Changes{Changes: c.pending, NewestSpaceVersion: drive.FlexInt64(c.version + 1)}
	c.version++
	c.pending = nil
	return out, nil
}

// addCreated 记下一条云端新建事件，同时补进 objects——
// 全量重建时（since==nil）要能在 Files 里看到它。
func (c *changeCapableClient) addCreated(path string, size int64, id int64) {
	mod := time.Unix(1767225600, 0)
	obj := drive.Object{Path: path, Size: size, FileId: drive.FlexInt64(id), LastModified: mod}
	c.objects = append(c.objects, obj)
	c.pending = append(c.pending, drive.Change{
		Event: drive.ChangeCreated, Path: path, Size: size, FileId: drive.FlexInt64(id),
	})
}

func TestIncrementalSyncAddsPlaceholder(t *testing.T) {
	if !cfapi.Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	mod := time.Unix(1767225600, 0)

	fc := &changeCapableClient{fakeClient: &fakeClient{
		files: map[string][]byte{
			"readme.txt":    []byte("top level"),
			"docs/guide.md": []byte("guide body"),
		},
		objects: []drive.Object{
			{Path: "readme.txt", Size: 9, LastModified: mod, FileId: 301},
			{Path: "docs/guide.md", Size: 10, LastModified: mod, FileId: 302},
		},
	}}

	cfg := Config{
		RootPath:     root,
		Space:        drive.SpacePersonal,
		Client:       fc,
		Token:        func() string { return "fake-token" },
		ProviderName: "EasyShareTest",
		ProviderID:   testGUID,
		State:        NewListState(),
	}

	m, err := cfapi.MountSpace(cfapi.MountOptions{
		RootPath:         root,
		Provider:         cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "0.1.0", ID: cfg.ProviderID},
		SyncRootIdentity: []byte("easyshare-cfmount-incremental"),
		List:             List(cfg),
		Open:             Open(cfg),
	})
	if err != nil {
		t.Fatalf("MountSpace 失败: %v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close 失败: %v", err)
		}
	}()

	// ① 挂载那一次走的是 /changes 不带游标（全量重建），旧的 Objects 接口一次都不该用
	if fc.listCalls != 0 {
		t.Errorf("支持增量后不该再调旧的全量列举接口，listCalls=%d", fc.listCalls)
	}
	if fc.changeNs != 1 || fc.cursors[0] != nil {
		t.Errorf("首轮应问一次增量且不带游标，calls=%d cursors=%v", fc.changeNs, fc.cursors)
	}

	// ② 云端新建一个文件，再同步一次：必须带上一轮的版本游标，且不能再拉全量
	fc.addCreated("docs/new.txt", 11, 303)
	fc.files["docs/new.txt"] = []byte("brand new!!")   // 11 字节，与事件里的 Size 对齐
	if err := m.Sync(List(cfg)); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}
	if fc.changeNs != 2 || fc.cursors[1] == nil || *fc.cursors[1] != 100 {
		t.Fatalf("次轮应带游标 100，calls=%d cursors=%v", fc.changeNs, fc.cursors)
	}
	if fc.listCalls != 0 {
		t.Errorf("增量轮不该拉全量，listCalls=%d", fc.listCalls)
	}

	// ③ 差异真的落成了占位符：本地可见、逻辑大小是云端值、且**没被下载**
	path := filepath.Join(root, "docs", "new.txt")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("增量事件没建出占位符：%v", err)
	}
	if fi.Size() != 11 {
		t.Errorf("占位符逻辑大小 = %d，期望云端的 11（事件里的元数据必须带上）", fi.Size())
	}
	if len(fc.openCalls) != 0 {
		t.Errorf("没人读它却拉取了内容：%v", fc.openCalls)
	}

	// ④ 读它才能拿到正确内容——证明合并出来的视图是真能用的，不只是游标动了
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读新占位符失败: %v", err)
	}
	if string(got) != "brand new!!" {
		t.Errorf("按需取数内容不符：%q", got)
	}
	if fmt.Sprint(fc.openCalls) != "[docs/new.txt]" {
		t.Errorf("应只拉被读的那个文件，openCalls=%v", fc.openCalls)
	}
}
