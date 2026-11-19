package cfmount

import (
	"context"
	"testing"

	"easyshare/internal/drive"
)

type progressWritebackClient struct {
	*wbFake
	progressReported bool
}

func (f *progressWritebackClient) UploadFileIfVersion(ctx context.Context, token, space, path, local string, expected *int64, progress drive.ProgressFunc) error {
	if progress != nil {
		progress(3, 3)
		f.progressReported = true
	}
	return f.wbFake.UploadFileIfVersion(ctx, token, space, path, local, expected, nil)
}

func captureWritebackStatus(e *WritebackEngine) *[]WritebackStatus {
	statuses := []WritebackStatus{}
	e.cfg.OnStatus = func(status WritebackStatus) { statuses = append(statuses, status) }
	return &statuses
}

func TestWritebackStatusBytesCompleteNeedsVerification(t *testing.T) {
	e, f, cfg := wbSetup(t)
	client := &progressWritebackClient{wbFake: f}
	e.cfg.Client = client
	statuses := captureWritebackStatus(e)
	wbWrite(t, cfg, "状态.txt", "one")
	wbStable(t, e)
	if !client.progressReported {
		t.Fatal("上传未接入真实进度回调")
	}
	fullUpload, verifying, synced := -1, -1, -1
	for i, status := range *statuses {
		if status.Phase == WritebackUploading && status.Completed == status.Total && status.Total > 0 {
			fullUpload = i
		}
		if status.Phase == WritebackVerifying {
			verifying = i
		}
		if status.Phase == WritebackSynced {
			synced = i
		}
	}
	if fullUpload < 0 || verifying <= fullUpload || synced <= verifying {
		t.Fatalf("字节完成必须先确认后同步: %+v", *statuses)
	}
}

func TestWritebackStatusFalseSuccessRetainsPending(t *testing.T) {
	e, f, cfg := wbSetup(t)
	f.lie = true
	statuses := captureWritebackStatus(e)
	wbWrite(t, cfg, "状态.txt", "one")
	wbStable(t, e)
	failed := false
	for _, status := range *statuses {
		if status.Phase == WritebackSynced {
			t.Fatalf("上传谎报成功被标为同步: %+v", *statuses)
		}
		failed = failed || status.Phase == WritebackFailed
	}
	baseline := e.SyncBaselines()["状态.txt"]
	if !failed || !baseline.Pending || baseline.LocalVerified {
		t.Fatalf("校验失败须保留pending: %+v %+v", baseline, *statuses)
	}
}

func TestWritebackStatusConflictCannotBeSynced(t *testing.T) {
	e, f, cfg := wbSetup(t)
	f.data["状态.txt"] = []byte("server")
	f.versions["状态.txt"] = 7
	statuses := captureWritebackStatus(e)
	wbWrite(t, cfg, "状态.txt", "local")
	wbStable(t, e)
	conflict := false
	for _, status := range *statuses {
		if status.Phase == WritebackSynced {
			t.Fatal("未知已有内容被刷为同步")
		}
		conflict = conflict || status.Phase == WritebackConflict
	}
	if !conflict || f.uploads != 0 {
		t.Fatalf("冲突状态或写回保护不符: %+v", *statuses)
	}
}

func TestWritebackSyncBaselinesAreSnapshots(t *testing.T) {
	e, _, cfg := wbSetup(t)
	wbWrite(t, cfg, "状态.txt", "one")
	wbStable(t, e)
	baseline := e.SyncBaselines()["状态.txt"]
	if baseline.FileID <= 0 || baseline.Version <= 0 || baseline.Pending || !baseline.LocalVerified {
		t.Fatalf("已确认基线不符: %+v", baseline)
	}
	copy := e.SyncBaselines()
	delete(copy, "状态.txt")
	if _, ok := e.SyncBaselines()["状态.txt"]; !ok {
		t.Fatal("外部投影改变了引擎持久状态")
	}
}
