//go:build windows

package cfmount

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
)

// 这组用例钉的是「占位符挂载吃没吃到增量列举」（钉盘 §4 那套空间版本 + 变更流水）。
// 判据落在**请求形态**上而不是返回内容：只有第一次允许全量，之后必须带游标问差异；
// 而控制面不支持增量时，必须完整退回全量、不丢文件。

type stubLister struct {
	objects []drive.Object
	// results 依次作为 Changes 的返回；用尽后重复最后一项
	results []*drive.Changes
	// changesErr 非 nil 时 Changes 一律失败（模拟旧控制面没有 /changes）
	changesErr error

	objectsCalls int
	sinceSeen    []*int64
	changesCalls int
}

func (s *stubLister) Objects(context.Context, string, string) ([]drive.Object, error) {
	s.objectsCalls++
	return s.objects, nil
}

func (s *stubLister) Open(context.Context, string, string, string) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("本用例不读内容")
}

func (s *stubLister) Changes(_ context.Context, _, _ string, since *int64) (*drive.Changes, error) {
	s.changesCalls++
	s.sinceSeen = append(s.sinceSeen, since)
	if s.changesErr != nil {
		return nil, s.changesErr
	}
	if len(s.results) == 0 {
		return &drive.Changes{}, nil
	}
	out := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}
	return out, nil
}

func fileObject(path string, size int64, id int64) drive.Object {
	return drive.Object{Path: path, Size: size, FileId: drive.FlexInt64(id), LastModified: time.Unix(1767225600, 0)}
}

func TestListFirstFullThenIncremental(t *testing.T) {
	stub := &stubLister{
		objects: []drive.Object{fileObject("a.txt", 3, 1), fileObject("b.txt", 4, 2)},
		results: []*drive.Changes{
			// 第一次：全量重建（游标 nil）
			{Files: []drive.Object{fileObject("a.txt", 3, 1), fileObject("b.txt", 4, 2)}, NewestSpaceVersion: drive.FlexInt64(7)},
			// 第二次：只回差异——删掉 a、新增 c
			{
				NewestSpaceVersion: drive.FlexInt64(9),
				Changes: []drive.Change{
					{Event: drive.ChangeDeleted, Path: "a.txt", FileId: drive.FlexInt64(1)},
					{Event: drive.ChangeCreated, Path: "sub/c.txt", FileId: drive.FlexInt64(3), Size: 5},
				},
			},
		},
	}
	cfg := Config{Space: drive.SpacePersonal, Client: stub, Token: func() string { return "tok" }, State: NewListState()}
	list := List(cfg)

	first, err := list(context.Background())
	if err != nil {
		t.Fatalf("首轮列举失败：%v", err)
	}
	if stub.objectsCalls != 0 {
		t.Errorf("首轮应走 /changes 不带游标（那即全量重建），不该再用旧的全量接口，objectsCalls=%d", stub.objectsCalls)
	}
	if len(namesOf(first)) < 2 {
		t.Errorf("首轮视图至少要有 a.txt/b.txt 与推导出的目录，实得 %v", namesOf(first))
	}

	second, err := list(context.Background())
	if err != nil {
		t.Fatalf("次轮列举失败：%v", err)
	}
	if stub.changesCalls != 2 {
		t.Fatalf("两轮都该问一次 /changes，实得 %d", stub.changesCalls)
	}
	if stub.sinceSeen[0] != nil {
		t.Errorf("首轮不该带游标（没底），实得 %v", *stub.sinceSeen[0])
	}
	if stub.sinceSeen[1] == nil || *stub.sinceSeen[1] != 7 {
		t.Fatalf("次轮应带上一轮的版本 7，实得 %v", stub.sinceSeen[1])
	}
	if stub.objectsCalls != 0 {
		t.Errorf("走增量后不该再发全量列举，objectsCalls=%d", stub.objectsCalls)
	}
	names := namesOf(second)
	if containsName(names, "a.txt") {
		t.Errorf("deleted 事件没生效，a.txt 仍在视图里：%v", names)
	}
	if !containsName(names, "sub/c.txt") {
		t.Errorf("created 事件没生效，sub/c.txt 不在视图里：%v", names)
	}
	if !containsName(names, "sub") {
		t.Errorf("新文件所在目录应由路径推导出来：%v", names)
	}
}

func TestListFallsBackToFullWhenChangesUnsupported(t *testing.T) {
	stub := &stubLister{
		objects:    []drive.Object{fileObject("a.txt", 3, 1), fileObject("b.txt", 4, 2)},
		changesErr: errors.New("404 未找到 /changes"),
	}
	cfg := Config{Space: drive.SpacePersonal, Client: stub, Token: func() string { return "tok" }, State: NewListState()}
	list := List(cfg)

	for i := 0; i < 3; i++ {
		out, err := list(context.Background())
		if err != nil {
			t.Fatalf("第 %d 轮失败：%v", i+1, err)
		}
		names := namesOf(out)
		if !containsName(names, "a.txt") || !containsName(names, "b.txt") {
			t.Fatalf("旧控制面必须完整退回全量，实得 %v", names)
		}
	}
	if stub.objectsCalls != 3 {
		t.Errorf("增量不可用时应每轮全量，objectsCalls=%d", stub.objectsCalls)
	}
	// 关键：退路不能「半路用游标」——每次都得是从零开始的全量
	for i, since := range stub.sinceSeen {
		if since != nil {
			t.Errorf("第 %d 轮带着游标 %d 去要增量，退回全量后应清零", i+1, *since)
		}
	}
}

func TestListWithoutStateStaysFullScan(t *testing.T) {
	stub := &stubLister{
		objects: []drive.Object{fileObject("a.txt", 3, 1)},
		results: []*drive.Changes{{Files: []drive.Object{fileObject("a.txt", 3, 1)}, NewestSpaceVersion: drive.FlexInt64(1)}},
	}
	// 不传 State（旧调用方）：行为与引入增量之前完全一致
	cfg := Config{Space: drive.SpacePersonal, Client: stub, Token: func() string { return "tok" }}
	list := List(cfg)

	for i := 0; i < 2; i++ {
		if _, err := list(context.Background()); err != nil {
			t.Fatalf("第 %d 轮失败：%v", i+1, err)
		}
	}
	if stub.objectsCalls != 2 || stub.changesCalls != 0 {
		t.Errorf("无 State 时应只用全量接口：objectsCalls=%d changesCalls=%d", stub.objectsCalls, stub.changesCalls)
	}
}

func TestListWithoutTokenErrors(t *testing.T) {
	stub := &stubLister{objects: []drive.Object{fileObject("a.txt", 3, 1)}}
	cfg := Config{Space: drive.SpacePersonal, Client: stub, Token: func() string { return "" }}
	if _, err := List(cfg)(context.Background()); err == nil {
		t.Error("未登录应报错，而不是返回空清单——空清单会让挂载点看起来像空目录")
	}
}

func namesOf(entries []cfapi.CloudEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
