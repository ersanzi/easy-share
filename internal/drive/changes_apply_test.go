package drive

import (
	"testing"
	"time"
)

func obj(path string, size int64, id int64) Object {
	return Object{Path: path, Size: size, FileId: FlexInt64(id), LastModified: time.Unix(1767225600, 0)}
}

func ch(event, path string, id int64) Change {
	return Change{Event: event, Path: path, FileId: FlexInt64(id), Size: 99}
}

func TestApplyChangesCreatedUpdatedRestored(t *testing.T) {
	base := []Object{obj("a.txt", 10, 1)}

	// created：新路径带事件里的 size/fileId 进视图
	out := ApplyChanges(base, []Change{ch(ChangeCreated, "b.txt", 2)})
	if len(out) != 2 {
		t.Fatalf("created 后应有 2 项，实得 %d", len(out))
	}
	if out[1].Size != 99 || out[1].FileId.Int64() != 2 {
		t.Errorf("新增项元数据没带进来：size=%d fileId=%d", out[1].Size, out[1].FileId.Int64())
	}

	// updated/restored：路径已存在时**保留既有元数据**，不用零值覆盖
	// （真实缺陷：Stat 会返回 0 字节，用户看到文件「变小」）
	out = ApplyChanges(base, []Change{ch(ChangeUpdated, "a.txt", 1)})
	if out[0].Size != 10 {
		t.Errorf("updated 不该把已有对象的 size 改成事件里的值或零值，实得 %d", out[0].Size)
	}
	out = ApplyChanges(base, []Change{ch(ChangeRestored, "a.txt", 1)})
	if len(out) != 1 || out[0].Size != 10 {
		t.Errorf("restored 应等价于「路径存在」，实得 %+v", out)
	}
}

func TestApplyChangesDeletedAndPurged(t *testing.T) {
	base := []Object{obj("a.txt", 10, 1), obj("b.txt", 20, 2)}

	out := ApplyChanges(base, []Change{ch(ChangeDeleted, "a.txt", 1)})
	if len(out) != 1 || out[0].Path != "b.txt" {
		t.Errorf("deleted 应移除该路径，实得 %+v", out)
	}
	out = ApplyChanges(base, []Change{ch(ChangePurged, "b.txt", 2)})
	if len(out) != 1 || out[0].Path != "a.txt" {
		t.Errorf("purged 应移除该路径，实得 %+v", out)
	}
}

func TestApplyChangesMovedKeepsMetadata(t *testing.T) {
	base := []Object{obj("old/name.txt", 10, 7), obj("keep.txt", 5, 8)}

	moved := ch(ChangeMoved, "new/name.txt", 7)
	moved.OldPath = "old/name.txt"
	out := ApplyChanges(base, []Change{moved})

	if len(out) != 2 {
		t.Fatalf("moved 不改变项数，实得 %d：%+v", len(out), out)
	}
	var got *Object
	for i := range out {
		if out[i].Path == "new/name.txt" {
			got = &out[i]
		}
	}
	if got == nil {
		t.Fatalf("moved 后新路径没出现在视图里：%+v", out)
	}
	// 身份与元数据跟着走：fileId 用事件值，size 保留原值
	if got.FileId.Int64() != 7 || got.Size != 10 {
		t.Errorf("moved 应保留 size 并带上事件 fileId，实得 size=%d fileId=%d", got.Size, got.FileId.Int64())
	}
	for _, o := range out {
		if o.Path == "old/name.txt" {
			t.Error("moved 后旧路径仍在视图里")
		}
	}
}

func TestApplyChangesDoesNotMutateInput(t *testing.T) {
	// 调用方可能把旧视图同时交给别的 goroutine 在读（挂载缓存就是这样），
	// 合并必须产出新切片
	base := []Object{obj("a.txt", 10, 1), obj("b.txt", 20, 2)}
	out := ApplyChanges(base, []Change{ch(ChangeDeleted, "a.txt", 1)})

	if len(base) != 2 || base[0].Path != "a.txt" {
		t.Errorf("输入被改写了：%+v", base)
	}
	if len(out) != 1 {
		t.Errorf("输出应只剩一项，实得 %+v", out)
	}
}

func TestApplyChangesIgnoresUnknownEvent(t *testing.T) {
	base := []Object{obj("a.txt", 10, 1)}
	out := ApplyChanges(base, []Change{ch("some_future_event", "a.txt", 1)})
	if len(out) != 1 || out[0].Path != "a.txt" {
		t.Errorf("未知事件应原样返回，实得 %+v", out)
	}
}
