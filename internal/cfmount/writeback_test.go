package cfmount

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"easyshare/internal/drive"
)

type wbFake struct {
	data                        map[string][]byte
	versions                    map[string]int64
	uploads                     int
	fail, lie, changeDuringRead bool
	expected                    []int64
	dirs                        []string
}

func (f *wbFake) Objects(context.Context, string, string) ([]drive.Object, error) {
	out := []drive.Object{}
	for p, b := range f.data {
		out = append(out, drive.Object{FileId: 123, Path: p, Size: int64(len(b)), VersionId: drive.FlexInt64(f.versions[p])})
	}
	return out, nil
}
func (f *wbFake) Open(_ context.Context, _, _, p string) (io.ReadCloser, int64, error) {
	b, ok := f.data[p]
	if !ok {
		return nil, 0, os.ErrNotExist
	}
	if f.changeDuringRead {
		f.versions[p]++
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}
func (f *wbFake) UploadFileIfVersion(_ context.Context, _, _, p, local string, v *int64, _ drive.ProgressFunc) error {
	f.uploads++
	if v == nil {
		panic("禁止无条件上传")
	}
	f.expected = append(f.expected, *v)
	if f.fail {
		return errors.New("offline")
	}
	if f.versions[p] != *v {
		return errors.New("conflict")
	}
	b, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	f.versions[p]++
	if f.lie {
		f.data[p] = bytes.Repeat([]byte("x"), len(b))
	} else {
		f.data[p] = b
	}
	return nil
}
func (f *wbFake) CreateDirectory(_ context.Context, _, _, p string) (*drive.Object, error) {
	f.dirs = append(f.dirs, p)
	return &drive.Object{Path: p, Directory: true, FileId: 123}, nil
}
func wbSetup(t *testing.T) (*WritebackEngine, *wbFake, WritebackConfig) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	f := &wbFake{data: map[string][]byte{}, versions: map[string]int64{}}
	cfg := WritebackConfig{RootPath: root, StateDir: filepath.Join(base, "state"), Namespace: "server/account", Space: "personal", Client: f, Token: func() string { return "test" }, StableFor: time.Nanosecond}
	e, err := NewWritebackEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e, f, cfg
}
func wbWrite(t *testing.T, c WritebackConfig, p, s string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(c.RootPath, p), []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}
func wbTick(t *testing.T, e *WritebackEngine) WritebackResult {
	t.Helper()
	r, err := e.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func wbStable(t *testing.T, e *WritebackEngine) WritebackResult {
	t.Helper()
	wbTick(t, e)
	time.Sleep(2 * time.Millisecond)
	return wbTick(t, e)
}

func TestWritebackCreateEditCAS(t *testing.T) {
	e, f, c := wbSetup(t)
	wbWrite(t, c, "a", "one")
	r := wbStable(t, e)
	if len(r.Verified) != 1 || string(f.data["a"]) != "one" || f.expected[0] != 0 {
		t.Fatalf("新增失败: %+v %+v", r, f)
	}
	wbWrite(t, c, "a", "edited content")
	r = wbStable(t, e)
	if len(r.Verified) != 1 || f.expected[1] != 1 || string(f.data["a"]) != "edited content" {
		t.Fatalf("编辑失败: %+v", r)
	}
}
func TestWritebackInitialConflictNotBaseline(t *testing.T) {
	e, f, c := wbSetup(t)
	f.data["a"] = []byte("server")
	f.versions["a"] = 7
	wbWrite(t, c, "a", "local")
	r := wbStable(t, e)
	if f.uploads != 0 || len(r.Issues) == 0 {
		t.Fatalf("初始文件被覆盖: %+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(c.RootPath, "a"))
	if string(b) != "local" {
		t.Fatal("丢失本地字节")
	}
}
func TestWritebackAdoptsOnlyVerifiedContent(t *testing.T) {
	e, f, c := wbSetup(t)
	f.data["a"] = []byte("same")
	f.versions["a"] = 7
	wbWrite(t, c, "a", "same")
	r := wbStable(t, e)
	if len(r.Verified) != 1 || f.uploads != 0 || e.state.Records["a"].Version != 7 {
		t.Fatalf("未验证基线: %+v", r)
	}
	wbWrite(t, c, "a", "new")
	wbStable(t, e)
	if f.expected[0] != 7 {
		t.Fatal("未使用基线 CAS")
	}
}
func TestWritebackReadbackRejectsFalseSuccess(t *testing.T) {
	e, f, c := wbSetup(t)
	f.lie = true
	wbWrite(t, c, "a", "payload")
	r := wbStable(t, e)
	if len(r.Verified) != 0 || e.state.Records["a"].Pending == nil {
		t.Fatalf("伪成功: %+v", r)
	}
	q := e.state.Records["a"].Pending
	b, err := os.ReadFile(filepath.Join(c.StateDir, q.Snapshot))
	if err != nil || string(b) != "payload" {
		t.Fatal("失败后快照丢失")
	}
	before := f.uploads
	wbTick(t, e)
	if f.uploads != before {
		t.Fatal("冲突版本被再次覆盖")
	}
}
func TestWritebackPendingSurvivesRestart(t *testing.T) {
	e, f, c := wbSetup(t)
	f.fail = true
	wbWrite(t, c, "a", "payload")
	wbStable(t, e)
	e2, err := NewWritebackEngine(c)
	if err != nil {
		t.Fatal(err)
	}
	if e2.state.Records["a"].Pending == nil {
		t.Fatal("意图没有持久化")
	}
	f.fail = false
	r := wbStable(t, e2)
	if len(r.Verified) != 1 || string(f.data["a"]) != "payload" {
		t.Fatalf("恢复失败: %+v", r)
	}
}
func TestWritebackMissingRemoteNeverDeletesOrRecreates(t *testing.T) {
	e, f, c := wbSetup(t)
	wbWrite(t, c, "a", "payload")
	wbStable(t, e)
	delete(f.data, "a")
	delete(f.versions, "a")
	wbWrite(t, c, "a", "edited")
	r := wbStable(t, e)
	if len(r.Issues) == 0 || f.uploads != 1 {
		t.Fatalf("远端缺失被当成新增: %+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(c.RootPath, "a"))
	if string(b) != "edited" {
		t.Fatal("删除本地文件")
	}
}
func TestWritebackLocalMissingNeverDeletesRemote(t *testing.T) {
	e, f, c := wbSetup(t)
	wbWrite(t, c, "a", "payload")
	wbStable(t, e)
	os.Remove(filepath.Join(c.RootPath, "a"))
	r := wbTick(t, e)
	if string(f.data["a"]) != "payload" || len(r.Issues) == 0 {
		t.Fatalf("不安全删除: %+v", r)
	}
}
func TestWritebackDirectoriesAndProtectedPaths(t *testing.T) {
	e, f, c := wbSetup(t)
	os.Mkdir(filepath.Join(c.RootPath, "empty"), 0700)
	wbWrite(t, c, "unstable", "data")
	r, err := e.Reconcile(context.Background(), func(_ context.Context, protected []string) error {
		if len(protected) != 2 {
			t.Fatalf("没有保护全部本地路径: %v", protected)
		}
		return nil
	})
	if err != nil || len(f.dirs) != 1 || len(r.Verified) != 0 {
		t.Fatalf("目录失败 %+v %v", r, err)
	}
}
func TestWritebackTreeSchedulingSerialized(t *testing.T) {
	e, _, _ := wbSetup(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		e.Reconcile(context.Background(), func(context.Context, []string) error { close(entered); <-release; return nil })
	}()
	<-entered
	go func() { defer wg.Done(); e.Tick(context.Background()); close(done) }()
	select {
	case <-done:
		t.Fatal("写回绕过树同步互斥")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	wg.Wait()
}
func TestWritebackStateCorruptionFailsClosed(t *testing.T) {
	_, _, c := wbSetup(t)
	os.WriteFile(filepath.Join(c.StateDir, "pending.json"), []byte("broken"), 0600)
	if _, err := NewWritebackEngine(c); err == nil {
		t.Fatal("损坏状态被当成首次同步")
	}
}
func TestWritebackRemoteChangesDuringVerify(t *testing.T) {
	e, f, c := wbSetup(t)
	f.changeDuringRead = true
	wbWrite(t, c, "a", "payload")
	r := wbStable(t, e)
	if len(r.Verified) != 0 || e.state.Records["a"].Pending == nil {
		t.Fatal("版本变化仍标记验证成功")
	}
}

func TestWritebackConcurrentRemoteEditPreservesSnapshot(t *testing.T) {
	e, f, c := wbSetup(t)
	wbWrite(t, c, "a", "original")
	wbStable(t, e)
	f.data["a"] = []byte("remote edit")
	f.versions["a"] = 2
	wbWrite(t, c, "a", "local edit")
	r := wbStable(t, e)
	if f.uploads != 1 || string(f.data["a"]) != "remote edit" || len(r.Issues) == 0 {
		t.Fatalf("冲突没有被阻止: %+v", r)
	}
	q := e.state.Records["a"].Pending
	if q == nil || q.Expected != 1 {
		t.Fatal("丢失原始 CAS 意图")
	}
	b, err := os.ReadFile(filepath.Join(c.StateDir, q.Snapshot))
	if err != nil || string(b) != "local edit" {
		t.Fatal("丢失冲突字节")
	}
}

func TestWritebackUncertainUploadAdoptedAfterRestart(t *testing.T) {
	e, f, c := wbSetup(t)
	f.fail = true
	wbWrite(t, c, "a", "payload")
	wbStable(t, e)
	// 模拟上传已成功、但进程没有收到响应/保存结果。
	f.data["a"] = []byte("payload")
	f.versions["a"] = 1
	e2, err := NewWritebackEngine(c)
	if err != nil {
		t.Fatal(err)
	}
	r := wbStable(t, e2)
	if len(r.Verified) != 1 || f.uploads != 1 || e2.state.Records["a"].Pending != nil {
		t.Fatalf("不确定结果没有读回恢复: %+v", r)
	}
}

func TestWritebackRejectsStateWithinRoot(t *testing.T) {
	_, _, c := wbSetup(t)
	c.StateDir = filepath.Join(c.RootPath, "state")
	if _, err := NewWritebackEngine(c); err == nil {
		t.Fatal("状态目录被允许放入同步根")
	}
}
