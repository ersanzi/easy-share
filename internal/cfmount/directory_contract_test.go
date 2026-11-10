package cfmount

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"easyshare/internal/drive"
)

// 编译期保证生产客户端能直接接入，无需包装返回值。
var _ DirectoryCreator = (*drive.Client)(nil)
var _ WritebackClient = (*drive.Client)(nil)

type directoryFake struct {
	*wbFake
	objects  []drive.Object
	response *drive.Object
	err      error
}

func (f *directoryFake) Objects(context.Context, string, string) ([]drive.Object, error) {
	return f.objects, nil
}
func (f *directoryFake) CreateDirectory(_ context.Context, _, _, p string) (*drive.Object, error) {
	f.dirs = append(f.dirs, p)
	return f.response, f.err
}
func TestWritebackExplicitRemoteDirectoryNeedsNoCreate(t *testing.T) {
	e, base, c := wbSetup(t)
	if err := os.Mkdir(filepath.Join(c.RootPath, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	f := &directoryFake{wbFake: base, objects: []drive.Object{{Path: "empty", Directory: true, FileId: 7}}}
	e.cfg.Client = f
	r := wbTick(t, e)
	if len(r.Issues) != 0 || len(f.dirs) != 0 || !e.state.Records["empty"].Directory {
		t.Fatalf("未识别远端空目录: %+v", r)
	}
}
func TestWritebackDirectoryRejectsInvalidAck(t *testing.T) {
	for _, response := range []*drive.Object{nil, {Path: "empty", FileId: 7}, {Path: "wrong", Directory: true, FileId: 7}, {Path: "empty", Directory: true}} {
		e, base, c := wbSetup(t)
		if err := os.Mkdir(filepath.Join(c.RootPath, "empty"), 0700); err != nil {
			t.Fatal(err)
		}
		e.cfg.Client = &directoryFake{wbFake: base, response: response}
		r := wbTick(t, e)
		if len(r.Issues) == 0 || e.state.Records["empty"] != nil {
			t.Fatalf("错误 ACK 被采纳: %+v", r)
		}
	}
}
func TestWritebackRejectsRemoteTypeCollisions(t *testing.T) {
	for _, directory := range []bool{false, true} {
		e, base, c := wbSetup(t)
		if directory {
			wbWrite(t, c, "same", "local")
		} else if err := os.Mkdir(filepath.Join(c.RootPath, "same"), 0700); err != nil {
			t.Fatal(err)
		}
		f := &directoryFake{wbFake: base, objects: []drive.Object{{Path: "same", Directory: directory, FileId: 7}}}
		e.cfg.Client = f
		r := wbTick(t, e)
		if len(r.Issues) == 0 || r.Issues[0].Kind != "conflict" || len(f.dirs) != 0 || f.uploads != 0 {
			t.Fatalf("类型冲突未拒绝: %+v", r)
		}
	}
}
