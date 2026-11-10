package cfmount

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/drive"
)

// Explicit opt-in, using the same environment as internal/drive live tests:
// EASYSHARE_LIVE_DRIVE=1 EASYSHARE_LIVE_BASE=http://47.96.128.231:11054
// EASYSHARE_LIVE_USER_A=... EASYSHARE_LIVE_PASS_A=...
// go test ./internal/cfmount -run '^TestLiveWritebackCreateEdit$' -count=1 -v
// No real sync root is opened. The scoped adapter maps a disposable local root
// onto a unique remote prefix; it does NOT implement DirectoryCreator. Uploads
// create their implicit parent, so no undeployed empty-directory API is called.
func TestLiveWritebackCreateEdit(t *testing.T) {
	if os.Getenv("EASYSHARE_LIVE_DRIVE") == "" {
		t.Skip("set EASYSHARE_LIVE_DRIVE to opt into real-server writes")
	}
	base := strings.TrimRight(os.Getenv("EASYSHARE_LIVE_BASE"), "/")
	user, pass := os.Getenv("EASYSHARE_LIVE_USER_A"), os.Getenv("EASYSHARE_LIVE_PASS_A")
	if base == "" || user == "" || pass == "" {
		t.Skip("requires EASYSHARE_LIVE_BASE, EASYSHARE_LIVE_USER_A and EASYSHARE_LIVE_PASS_A")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	session, err := account.New(base).Login(ctx, user, pass)
	if err != nil {
		t.Fatalf("account login failed: %v", err)
	}
	if session.Token == "" {
		t.Fatal("account login returned no token")
	}
	// Never log the session, token, credentials or signed transfer URLs.
	redact := func(err error) string {
		return strings.ReplaceAll(fmt.Sprint(err), session.Token, "[REDACTED]")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "live-writeback-" + hex.EncodeToString(nonce[:]) + "/"
	const name = "ordinary.txt"
	remotePath := prefix + name
	client := drive.New(base)
	// Before registering destructive cleanup, establish that this exact random
	// namespace is unused. Never delete pre-existing data, even on a collision.
	objects, err := client.Objects(ctx, session.Token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("preflight listing: %s", redact(err))
	}
	for _, o := range objects {
		if o.Path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(o.Path, prefix) {
			t.Fatal("random remote namespace already exists; refusing to touch it")
		}
	}
	entries, err := client.RecycleList(ctx, session.Token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("preflight recycle listing: %s", redact(err))
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Path, prefix) || strings.HasPrefix(e.OriginPath, prefix) {
			t.Fatal("random remote namespace exists in recycle bin; refusing to touch it")
		}
	}
	// Register before the first Tick: a partially successful upload must also be
	// removed. Only this exact file is deleted/purged, never an entire recycle bin.
	t.Cleanup(func() {
		cleanCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		rows, listErr := client.Objects(cleanCtx, session.Token, drive.SpacePersonal)
		if listErr != nil {
			t.Errorf("cleanup listing: %s", redact(listErr))
			// Still try the exact owned path after an ambiguous upload/list failure.
		}
		exists := listErr != nil
		for _, o := range rows {
			exists = exists || o.Path == remotePath
		}
		if exists {
			if err := client.Delete(cleanCtx, session.Token, drive.SpacePersonal, remotePath); err != nil {
				t.Errorf("cleanup delete %s: %s", remotePath, redact(err))
			}
		}
		recycled, err := client.RecycleList(cleanCtx, session.Token, drive.SpacePersonal)
		if err != nil {
			t.Errorf("cleanup recycle listing: %s", redact(err))
			return
		}
		for _, e := range recycled {
			if e.Path != remotePath && e.OriginPath != remotePath {
				continue
			}
			if err := client.RecyclePurge(cleanCtx, session.Token, drive.SpacePersonal, e.FileId.Int64()); err != nil {
				t.Errorf("cleanup purge %s: %s", remotePath, redact(err))
			}
		}
		rows, err = client.Objects(cleanCtx, session.Token, drive.SpacePersonal)
		if err != nil {
			t.Errorf("cleanup verification listing: %s", redact(err))
		}
		for _, o := range rows {
			if strings.HasPrefix(o.Path, prefix) {
				t.Errorf("remote residue: %s", o.Path)
			}
		}
		recycled, err = client.RecycleList(cleanCtx, session.Token, drive.SpacePersonal)
		if err != nil {
			t.Errorf("cleanup recycle verification: %s", redact(err))
		}
		for _, e := range recycled {
			if strings.HasPrefix(e.Path, prefix) || strings.HasPrefix(e.OriginPath, prefix) {
				t.Errorf("test recycle residue remains under %s", prefix)
			}
		}
		if !t.Failed() {
			t.Log("cleanup verified: no test objects or recycle entries remain")
		}
	})

	local := t.TempDir()
	root := filepath.Join(local, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	const stableFor = 30 * time.Millisecond
	engine, err := NewWritebackEngine(WritebackConfig{
		RootPath: root, StateDir: filepath.Join(local, "state"),
		Space: drive.SpacePersonal, Namespace: base + "/" + user + "/" + prefix,
		Client: &liveWritebackScope{client: client, prefix: prefix},
		Token:  func() string { return session.Token }, StableFor: stableFor,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeAndTick := func(content []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			t.Fatal(err)
		}
		first, err := engine.Tick(ctx)
		if err != nil || len(first.Issues) != 0 || len(first.Verified) != 0 {
			t.Fatalf("observation Tick: err=%s issues=%v verified=%v", redact(err), first.Issues, first.Verified)
		}
		time.Sleep(2 * stableFor)
		second, err := engine.Tick(ctx)
		if err != nil || len(second.Issues) != 0 || len(second.Verified) != 1 || second.Verified[0] != name {
			t.Fatalf("stable Tick: err=%s issues=%v verified=%v", redact(err), second.Issues, second.Verified)
		}
	}
	verify := func(want []byte) int64 {
		t.Helper()
		rows, err := client.Objects(ctx, session.Token, drive.SpacePersonal)
		if err != nil {
			t.Fatalf("real listing: %s", redact(err))
		}
		var found *drive.Object
		for i := range rows {
			if rows[i].Path == remotePath {
				found = &rows[i]
			}
		}
		if found == nil || found.Directory || found.Size != int64(len(want)) || found.VersionId.Int64() <= 0 {
			t.Fatalf("real listing lacks ordinary file with valid size/version: %s", remotePath)
		}
		body, _, err := client.Open(ctx, session.Token, drive.SpacePersonal, remotePath)
		if err != nil {
			t.Fatalf("real download: %s", redact(err))
		}
		got, readErr := io.ReadAll(io.LimitReader(body, int64(len(want))+1))
		closeErr := body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("download read/close: %s / %s", redact(readErr), redact(closeErr))
		}
		if len(got) != len(want) || sha256.Sum256(got) != sha256.Sum256(want) {
			t.Fatal("real download size/SHA-256 mismatch")
		}
		t.Logf("verified %s: version=%d bytes=%d sha256=%x", remotePath, found.VersionId.Int64(), len(got), sha256.Sum256(got))
		return found.VersionId.Int64()
	}
	first := bytes.Repeat([]byte("ordinary local file, first version\n"), 257)
	writeAndTick(first)
	v1 := verify(first)
	second := bytes.Repeat([]byte("edited ordinary local file, second version\n"), 331)
	writeAndTick(second)
	v2 := verify(second)
	if v2 <= v1 {
		t.Fatalf("editing must advance real remote version: %d -> %d", v1, v2)
	}
}

// Pure path scoping; all listing, CAS upload and readback use the real client.
// No anonymous embedding: do not accidentally expose DirectoryCreator.
type liveWritebackScope struct {
	client *drive.Client
	prefix string
}

func (s *liveWritebackScope) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	rows, err := s.client.Objects(ctx, token, space)
	if err != nil {
		return nil, err
	}
	var scoped []drive.Object
	for _, o := range rows {
		if strings.HasPrefix(o.Path, s.prefix) {
			o.Path = strings.TrimPrefix(o.Path, s.prefix)
			scoped = append(scoped, o)
		}
	}
	return scoped, nil
}

func (s *liveWritebackScope) Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error) {
	return s.client.Open(ctx, token, space, s.prefix+path)
}

func (s *liveWritebackScope) UploadFileIfVersion(ctx context.Context, token, space, path, local string, expected *int64, progress drive.ProgressFunc) error {
	return s.client.UploadFileIfVersion(ctx, token, space, s.prefix+path, local, expected, progress)
}
