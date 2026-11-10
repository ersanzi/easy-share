package cfmount

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easyshare/internal/drive"
)

type cacheFake struct {
	*wbFake
	id        int64
	swap      bool
	duplicate bool
	reads     int
}

func (f *cacheFake) Objects(ctx context.Context, a, b string) ([]drive.Object, error) {
	out, err := f.wbFake.Objects(ctx, a, b)
	for i := range out {
		out[i].FileId = drive.FlexInt64(f.id)
	}
	if f.duplicate && len(out) > 0 {
		out = append(out, out[0])
	}
	return out, err
}
func (f *cacheFake) Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error) {
	f.reads++
	body, size, err := f.wbFake.Open(ctx, token, space, path)
	if f.swap {
		f.id++
	}
	return body, size, err
}

func cacheSetup(t *testing.T) (*WritebackEngine, *cacheFake) {
	e, f, c := wbSetup(t)
	cf := &cacheFake{wbFake: f, id: 123}
	e.cfg.Client = cf
	e.cfg.CacheIdleFor = time.Nanosecond
	wbWrite(t, c, "a", "payload")
	wbStable(t, e)
	return e, cf
}
func TestCacheScheduleAfterProtectedTree(t *testing.T) {
	e, _ := cacheSetup(t)
	treeDone := false
	calls := 0
	out, err := e.Reconcile(context.Background(), func(context.Context, []string) error { treeDone = true; return nil }, func(ctx context.Context, c CacheCandidate) (bool, error) {
		calls++
		if !treeDone || c.LocalInfo == nil || c.Version != 1 || c.RemoteFileID != 123 {
			t.Fatal("wrong order/baseline")
		}
		return true, c.Confirm(ctx)
	})
	if err != nil || calls != 1 || len(out.Released) != 1 {
		t.Fatalf("%+v %v calls=%d", out, err, calls)
	}
}
func TestCacheScheduleRetainsUnsafeCandidates(t *testing.T) {
	for _, kind := range []string{"pending", "conflict", "idle", "tree-failure", "ambiguous", "remote-version", "unuploaded", "remote-id", "same-metadata-edit", "pending-newer-local"} {
		t.Run(kind, func(t *testing.T) {
			e, f := cacheSetup(t)
			tree := func(context.Context, []string) error { return nil }
			switch kind {
			case "pending":
				wbWrite(t, e.cfg, "a", "changed")
				f.fail = true
				wbStable(t, e)
			case "conflict":
				f.versions["a"]++
				wbWrite(t, e.cfg, "a", "changed")
				wbStable(t, e)
			case "idle":
				e.cfg.CacheIdleFor = time.Hour
			case "tree-failure":
				tree = func(context.Context, []string) error { return errors.New("tree") }
			case "ambiguous":
				f.duplicate = true
			case "remote-version":
				f.versions["a"]++
			case "unuploaded":
				wbWrite(t, e.cfg, "a", "new content")
			case "remote-id":
				f.id++
			case "same-metadata-edit":
				path := filepath.Join(e.cfg.RootPath, "a")
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				wbWrite(t, e.cfg, "a", "PAYLOAD")
				if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				f.fail = true
			case "pending-newer-local":
				wbWrite(t, e.cfg, "a", "pending")
				f.fail = true
				wbStable(t, e)
				path := filepath.Join(e.cfg.RootPath, "a")
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				wbWrite(t, e.cfg, "a", "newer!!")
				if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				f.fail = false
			}
			calls := 0
			e.Reconcile(context.Background(), tree, func(context.Context, CacheCandidate) (bool, error) { calls++; return true, nil })
			if calls != 0 {
				t.Fatal("unsafe candidate released")
			}
		})
	}
}
func TestCacheConfirmRequiresFullReadbackAndStableIdentity(t *testing.T) {
	for _, kind := range []string{"hash", "version", "identity", "during-read", "identity-during-read"} {
		t.Run(kind, func(t *testing.T) {
			e, f := cacheSetup(t)
			calls := 0
			out, err := e.Reconcile(context.Background(), func(context.Context, []string) error { return nil }, func(ctx context.Context, c CacheCandidate) (bool, error) {
				calls++
				switch kind {
				case "hash":
					f.data["a"] = []byte("PAYLOAD")
				case "version":
					f.versions["a"]++
				case "identity":
					f.id++
				case "during-read":
					f.changeDuringRead = true
				case "identity-during-read":
					f.swap = true
				}
				x := c.Confirm(ctx)
				if x == nil {
					t.Fatal("remote mutation accepted")
				}
				return false, x
			})
			if err != nil || calls != 1 || len(out.Released) != 0 || len(out.Issues) != 1 {
				t.Fatalf("%+v %v calls=%d", out, err, calls)
			}
		})
	}
}
func TestCacheTickDoesNotReleaseWithoutTree(t *testing.T) {
	e, _ := cacheSetup(t)
	calls := 0
	e.Reconcile(context.Background(), nil, func(context.Context, CacheCandidate) (bool, error) { calls++; return true, nil })
	if calls != 0 {
		t.Fatal("release before tree")
	}
}

func TestCacheLegacyIdentityRequiresReadback(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same", true: "remote-changed"}[changed], func(t *testing.T) {
			e, f := cacheSetup(t)
			e.state.Records["a"].FileID = 0
			if err := e.save(); err != nil {
				t.Fatal(err)
			}
			e, err := NewWritebackEngine(e.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				f.data["a"] = []byte("PAYLOAD")
			}
			before := f.reads
			calls := 0
			for i := 0; i < 2; i++ {
				e.Reconcile(context.Background(), func(context.Context, []string) error { return nil }, func(ctx context.Context, c CacheCandidate) (bool, error) {
					calls++
					return true, c.Confirm(ctx)
				})
				time.Sleep(time.Millisecond)
			}
			if f.reads <= before {
				t.Fatal("legacy identity was inferred without remote readback")
			}
			if changed && (calls != 0 || e.state.Records["a"].FileID != 0) {
				t.Fatal("changed remote content authorized legacy cache")
			}
			if !changed && (calls != 1 || e.state.Records["a"].FileID != f.id) {
				t.Fatal("verified legacy identity not committed")
			}
		})
	}
}

func TestCacheCommittedIdentitySurvivesRestart(t *testing.T) {
	e, f := cacheSetup(t)
	if e.state.Records["a"].FileID != 123 {
		t.Fatal("fileId not stored with upload confirmation")
	}
	restarted, err := NewWritebackEngine(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.id++
	wbStable(t, restarted)
	calls := 0
	out, err := restarted.Reconcile(context.Background(), func(context.Context, []string) error { return nil }, func(context.Context, CacheCandidate) (bool, error) { calls++; return true, nil })
	if err != nil || calls != 0 || len(out.Issues) == 0 || restarted.state.Records["a"].FileID != 123 {
		t.Fatalf("restarted baseline adopted new identity: %+v %v", out, err)
	}
}

func TestCommittedCacheManualOperationKeepsPending(t *testing.T) {
	e, f := cacheSetup(t)
	called := 0
	if err := e.WithCommittedCache(context.Background(), "a", func(ctx context.Context, c CacheCandidate) error { called++; return c.Confirm(ctx) }); err != nil || called != 1 {
		t.Fatalf("confirmed baseline: %v", err)
	}
	wbWrite(t, e.cfg, "a", "changed")
	f.fail = true
	wbStable(t, e)
	if err := e.WithCommittedCache(context.Background(), "a", func(context.Context, CacheCandidate) error { called++; return nil }); err == nil {
		t.Fatal("manual cache accepted pending upload")
	}
	if called != 1 || e.state.Records["a"].Pending == nil {
		t.Fatal("manual request changed pending")
	}
}
