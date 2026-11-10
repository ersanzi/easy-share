//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

// 仅允许新建隔离前缀，真实服务端、真实 CFAPI、生产调度适配器一起验收。
// 不使用 App.Login、正式根或 namespace；不终止用户程序。失败时保留测试数据。
func TestLiveNativeCFAPICacheIsolated(t *testing.T) {
	if os.Getenv("EASYSHARE_LIVE_CFAPI") != "1" || os.Getenv("EASYSHARE_LIVE_DRIVE") != "1" || os.Getenv("EASYSHARE_LIVE_CACHE") != "1" {
		t.Skip("真实缓存闭环需显式启用三个 live 开关")
	}
	workspace := nativeLiveWorkspace(t)
	base := strings.TrimRight(os.Getenv("EASYSHARE_LIVE_BASE"), "/")
	if !cfapi.Available() {
		t.Fatal("真实缓存闭环缺少 CFAPI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	session, err := nativeLiveSession(ctx, base)
	if err != nil || session == nil || session.Token == "" || session.User.UserID == "" {
		t.Fatal("真实服务器登录或用户身份查询失败（详情隐藏）")
	}
	token := session.Token
	client := drive.New(base)
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal("生成测试身份失败")
	}
	prefix := "缓存验收-" + strings.ToUpper(hex.EncodeToString(nonce[:3]))
	inScope := func(p string) bool { return p == prefix || strings.HasPrefix(p, prefix+"/") }
	before, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal("服务器清单预检失败")
	}
	for _, row := range before {
		if inScope(row.Path) {
			t.Fatal("测试短名称已存在，不接管")
		}
	}
	recycle, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal("服务器回收站预检失败")
	}
	for _, row := range recycle {
		if inScope(row.Path) || inScope(row.OriginPath) {
			t.Fatal("测试短名称已有回收站身份，不接管")
		}
	}
	local, err := os.MkdirTemp(workspace, "live-cache-")
	if err != nil {
		t.Fatal("建立隔离测试落点失败")
	}
	root, state := filepath.Join(local, "root"), filepath.Join(local, "state")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal("创建隔离同步根失败")
	}
	const rel = "文档.txt"
	localPath := filepath.Join(root, rel)
	var mount *cfapi.Mount
	owned := make(map[string]drive.Object)
	completed := false
	t.Cleanup(func() {
		// 全随机身份及已验证的精确 fileId 持久到研发证据，不靠目录名猜归属。
		proof, proofErr := json.MarshalIndent(map[string]any{"prefix": prefix, "nonce": hex.EncodeToString(nonce[:]), "local": local, "owned": owned, "coreChecksPassed": completed, "cleanupCompleted": false}, "", "  ")
		if proofErr != nil || os.WriteFile(filepath.Join(local, "manifest.json"), proof, 0600) != nil {
			t.Error("测试身份记录失败，停止清理")
			return
		}
		if mount != nil {
			if err := mount.Close(); err != nil {
				t.Errorf("断开测试根失败，保留 %s", local)
				return
			}
		}
		if !completed || t.Failed() {
			t.Logf("CACHE_LIVE_RETAINED: local=%s remote=%s; 未扩大清理范围", local, prefix)
			return
		}
		cleanCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		rows, err := client.Objects(cleanCtx, token, drive.SpacePersonal)
		if err != nil {
			t.Error("清理前身份复核失败，保留测试数据")
			return
		}
		count := 0
		for _, row := range rows {
			if !inScope(row.Path) {
				continue
			}
			want, ok := owned[row.Path]
			if !ok || row.FileId != want.FileId || row.VersionId != want.VersionId || row.Directory != want.Directory {
				t.Error("测试数据身份或版本已变化，拒绝清理")
				return
			}
			count++
		}
		if count != len(owned) {
			t.Error("测试对象数量变化，拒绝清理")
			return
		}
		// 先保存确切对象键的短期只读凭据和删除前事件游标；不输出 URL。
		file := owned[prefix+"/"+rel]
		signed, err := client.PresignGet(cleanCtx, token, drive.SpacePersonal, file.Path)
		if err != nil {
			t.Error("清理前测试对象下载身份确认失败")
			return
		}
		zero := int64(0)
		start, err := client.Changes(cleanCtx, token, drive.SpacePersonal, &zero)
		if err != nil || start == nil {
			t.Error("清理前事件游标读取失败")
			return
		}
		cursor := start.NewestSpaceVersion.Int64()
		if err = cfapi.UnregisterSyncRoot(root); err != nil {
			t.Error("测试根显式注销失败，保留测试数据")
			return
		}
		if err = nativeLiveCleanupDelete(cleanCtx, base, token, file.Path, file.FileId.Int64()); err != nil {
			t.Errorf("精确测试文件软删失败: %v", err)
			return
		}
		if err = client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, file.FileId.Int64()); err != nil {
			t.Error("精确测试文件回收站清理失败")
			return
		}
		// 服务器删除行与对象之间可能被并发列表惰性补账。只确认本轮删除后
		// 同一个精确路径的一次 created、原 ID 的 purge 事件以及实际对象键已 404。
		time.Sleep(500 * time.Millisecond)
		delta, err := client.Changes(cleanCtx, token, drive.SpacePersonal, &cursor)
		if err != nil || delta == nil {
			t.Error("清理后事件核验失败")
			return
		}
		ghost, err := cacheLiveCleanupGhost(delta.Changes, file)
		if err != nil {
			t.Errorf("清理后发现不明变更，不扩大删除: %v", err)
			return
		}
		if err = cacheLiveRequireGone(cleanCtx, signed); err != nil {
			t.Error("对象键未确认 404，禁止清理新身份")
			return
		}
		if ghost > 0 {
			if err = nativeLiveCleanupDelete(cleanCtx, base, token, file.Path, ghost); err != nil {
				t.Errorf("精确补账残留软删失败: %v", err)
				return
			}
			if err = client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, ghost); err != nil {
				t.Error("精确补账残留 purge 失败")
				return
			}
			t.Logf("CACHE_CLEANUP_GHOST: proved absent object and one post-purge reconciliation ID=%d", ghost)
		}
		folder := owned[prefix]
		if err = nativeLiveCleanupDelete(cleanCtx, base, token, prefix, folder.FileId.Int64()); err != nil {
			t.Errorf("精确测试目录清理失败: path=%s id=%d %v; 保留 %s", prefix, folder.FileId.Int64(), err, local)
			return
		}
		if err = client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, folder.FileId.Int64()); err != nil {
			t.Error("精确测试目录回收站清理失败")
			return
		}
		rows, err = client.Objects(cleanCtx, token, drive.SpacePersonal)
		if err != nil {
			t.Error("清理后服务器清单查询失败")
			return
		}
		for _, row := range rows {
			if inScope(row.Path) {
				t.Error("测试服务器对象有残留")
				return
			}
		}
		remaining, err := client.RecycleList(cleanCtx, token, drive.SpacePersonal)
		if err != nil {
			t.Error("清理后回收站查询失败")
			return
		}
		for _, row := range remaining {
			if inScope(row.Path) || inScope(row.OriginPath) {
				t.Error("测试回收站有残留")
				return
			}
		}
		if os.RemoveAll(local) != nil {
			t.Error("已注销的测试本地根清理失败")
			return
		}
		t.Log("CACHE_LIVE_CLEAN: exact owned IDs purged, no remote/recycle/local test residue")
	})
	folder, err := client.CreateDirectory(ctx, token, drive.SpacePersonal, prefix)
	if err != nil || folder == nil || !folder.Directory || folder.FileId.Int64() <= 0 || folder.Path != prefix {
		t.Fatal("创建隔离远端目录失败")
	}
	owned[prefix] = *folder
	scope := &cacheLiveScope{nativeLiveScope: &nativeLiveScope{client: client, prefix: prefix, reads: map[string]int{}}}
	guid, err := windows.GenerateGUID()
	if err != nil || guid == cfapiPersonalProviderID {
		t.Fatal("隔离 provider 身份生成失败")
	}
	digest := sha256.Sum256([]byte(base + "\x00" + session.User.UserID + "\x00" + prefix + "\x00" + hex.EncodeToString(nonce[:])))
	identity := hex.EncodeToString(digest[:])
	cfg := cfmount.Config{RootPath: root, Space: drive.SpacePersonal, Client: scope, Token: func() string { return token }, ProviderName: "EasyShare cache acceptance", ProviderID: guid, Writable: true, CacheIdleFor: 4 * time.Second}
	var reconcile func(*cfapi.Mount) error
	connect := func() {
		t.Helper()
		list, cycle, err := prepareNativeCFAPI(ctx, cfg, state, identity, ctx)
		if err != nil {
			t.Fatal("隔离写回与缓存调度初始化失败")
		}
		mount, err = cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "cache-live", ID: guid}, SyncRootIdentity: []byte(identity), List: list, Open: cfmount.Open(cfg), Policies: &cfapi.SyncPolicies{Hydration: cfapi.HydrationPolicy{Primary: cfapi.HydrationPolicyProgressive}, Population: cfapi.PopulationPolicy{Primary: cfapi.PopulationPolicyAlwaysFull}}})
		if err != nil {
			t.Fatal("隔离真实 CFAPI 连接失败")
		}
		reconcile = cycle
	}
	connect()
	content := bytes.Repeat([]byte("server-primary-"+hex.EncodeToString(nonce[:])+"\r\n"), 1024)
	if err = os.WriteFile(localPath, content, 0600); err != nil {
		t.Fatal("写入隔离根的测试正文失败")
	}
	fileIdentity, err := cfapi.ReadDirectoryNTFSIdentity(localPath)
	if err != nil {
		t.Fatal("测试文件 NTFS 身份采集失败")
	}
	tick := func() {
		t.Helper()
		if err := reconcile(mount); !cacheLiveExpectedOnline(err, rel) {
			t.Fatal("缓存自动调度返回非预期错误（详情隐藏）")
		}
	}
	waitReleased := func(stage string) {
		t.Helper()
		deadline := time.Now().Add(150 * time.Second)
		for time.Now().Before(deadline) && ctx.Err() == nil {
			tick()
			if cacheLiveAllocated(t, localPath) == 0 {
				if got, err := cfapi.ReadDirectoryNTFSIdentity(localPath); err != nil || got != fileIdentity {
					t.Fatal("自动回收改变 NTFS 文件身份")
				}
				t.Logf("AUTO_RELEASE_CONFIRMED: stage=%s allocation=0", stage)
				return
			}
			time.Sleep(1500 * time.Millisecond)
		}
		t.Fatalf("阶段 %s 未在上限内自动释放，不能按手动回收冒充", stage)
	}
	verify := func(want []byte) drive.Object {
		t.Helper()
		verifier := drive.New(base)
		find := func() drive.Object {
			t.Helper()
			rows, err := verifier.Objects(ctx, token, drive.SpacePersonal)
			if err != nil {
				t.Fatal("独立服务器清单验证失败")
			}
			var found drive.Object
			for _, row := range rows {
				if row.Path == prefix+"/"+rel {
					if found.FileId.Int64() != 0 {
						t.Fatal("独立清单出现同名重复项")
					}
					found = row
				}
			}
			if found.FileId.Int64() <= 0 || found.VersionId.Int64() <= 0 || found.Directory || found.Size != int64(len(want)) {
				t.Fatal("独立服务器身份/版本/大小验证失败")
			}
			return found
		}
		a := find()
		body, _, err := verifier.Open(ctx, token, drive.SpacePersonal, prefix+"/"+rel)
		if err != nil {
			t.Fatal("独立服务器正文下载失败")
		}
		got, readErr := io.ReadAll(io.LimitReader(body, int64(len(want))+1))
		closeErr := body.Close()
		if readErr != nil || closeErr != nil || sha256.Sum256(got) != sha256.Sum256(want) {
			t.Fatal("独立服务器完整哈希校验失败")
		}
		b := find()
		if a.FileId != b.FileId || a.VersionId != b.VersionId || a.Size != b.Size {
			t.Fatal("独立服务器核验期间身份或版本变化")
		}
		owned[b.Path] = b
		t.Logf("REMOTE_BYTES_CONFIRMED: fileId=%d version=%d bytes=%d sha256=%x", b.FileId.Int64(), b.VersionId.Int64(), b.Size, sha256.Sum256(got))
		return b
	}
	waitReleased("uploaded-v1")
	v1 := verify(content)
	reads := scope.readCount("")
	tick()
	tick()
	if scope.readCount("") != reads || cacheLiveAllocated(t, localPath) != 0 {
		t.Fatal("仅云端定时扫描重新下载了正文")
	}
	// 独立 PowerShell 从已释放占位符读取真实服务器字节，再直接编辑同一文件。
	quoted := "'" + strings.ReplaceAll(localPath, "'", "''") + "'"
	wantHash := fmt.Sprintf("%x", sha256.Sum256(content))
	script := "$ErrorActionPreference='Stop';$f=[IO.File]::Open(" + quoted + ",[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]7);try{$h=[Security.Cryptography.SHA256]::Create();$hash=[BitConverter]::ToString($h.ComputeHash($f)).Replace('-','').ToLowerInvariant();if($hash -ne '" + wantHash + "'){throw 'hash mismatch'}}finally{$f.Dispose()};$f=[IO.File]::Open(" + quoted + ",[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]7);try{$f.WriteByte(69);$f.Flush($true)}finally{$f.Dispose()};Write-Output 'SERVER_REOPEN_EDIT_OK'"
	readCtx, stopRead := context.WithTimeout(ctx, 30*time.Second)
	output, err := exec.CommandContext(readCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	stopRead()
	if err != nil || !bytes.Contains(output, []byte("SERVER_REOPEN_EDIT_OK")) {
		t.Fatal("真实服务器回读后的独立程序打开/编辑失败（详情隐藏）")
	}
	if scope.readCount("") <= reads {
		t.Fatal("独立程序没有通过下载适配器回读服务器")
	}
	edited := append([]byte(nil), content...)
	edited[0] = 'E'
	// 故意在条件上传入口拒绝本次提交，确认调度不回收尚未上传修改。
	scope.setFailUpload(true)
	tick()
	time.Sleep(3 * time.Second)
	if err = reconcile(mount); err == nil {
		t.Fatal("注入条件上传失败未被报告")
	}
	if scope.failedUploads() == 0 || cacheLiveAllocated(t, localPath) == 0 {
		t.Fatal("未提交修改被误回收，或注入故障没有实际执行")
	}
	if got, err := os.ReadFile(localPath); err != nil || !bytes.Equal(got, edited) {
		t.Fatal("条件上传失败后本机编辑内容丢失")
	}
	if still := verify(content); still.FileId != v1.FileId || still.VersionId != v1.VersionId {
		t.Fatal("注入失败时服务器版本被意外改变")
	}
	t.Log("UNUPLOADED_EDIT_RETAINED: failed conditional upload, local bytes exact, server v1 unchanged")
	scope.setFailUpload(false)
	waitReleased("edited-v2")
	v2 := verify(edited)
	if v2.FileId != v1.FileId || v2.VersionId.Int64() <= v1.VersionId.Int64() {
		t.Fatal("编辑提交未保留 fileId 或版本未推进")
	}
	reads = scope.readCount("")
	if err = mount.Close(); err != nil {
		t.Fatal("重连前隔离挂载断开失败")
	}
	connect()
	tick()
	if cacheLiveAllocated(t, localPath) != 0 || scope.readCount("") != reads {
		t.Fatal("重建写回引擎与挂载时下载了仅云端正文")
	}
	if got, err := cfapi.ReadDirectoryNTFSIdentity(localPath); err != nil || got != fileIdentity {
		t.Fatal("重建连接改变原文件身份")
	}
	export := filepath.Join(local, "导出.txt")
	source, err := os.Open(localPath)
	if err != nil {
		t.Fatal("重建连接后无法回读")
	}
	dest, err := os.Create(export)
	if err != nil {
		source.Close()
		t.Fatal("创建显式导出副本失败")
	}
	_, copyErr := io.Copy(dest, source)
	if err = errors.Join(copyErr, source.Close(), dest.Close()); err != nil {
		t.Fatal("重连后的显式复制导出失败")
	}
	if got, err := os.ReadFile(export); err != nil || !bytes.Equal(got, edited) {
		t.Fatal("重连后的导出副本哈希不符")
	}
	waitReleased("post-reconnect-export")
	verify(edited)
	if err = mount.Close(); err != nil {
		t.Fatal("最终断开失败")
	}
	if got, err := os.ReadFile(export); err != nil || !bytes.Equal(got, edited) {
		t.Fatal("导出副本依赖云盘连接")
	}
	completed = true
	t.Log("CACHE_LIVE_PASS: real server upload, automatic release, external reopen/edit, failed-upload retention, v2 release, engine reconnect, independent export")
}

// 只注入上传失败；成功路径、能力、下载与清单均走真实客户端。
type cacheLiveScope struct {
	*nativeLiveScope
	cacheMu    sync.Mutex
	failUpload bool
	failures   int
}

func (s *cacheLiveScope) setFailUpload(fail bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.failUpload = fail
}

func (s *cacheLiveScope) failedUploads() int {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return s.failures
}

func (s *cacheLiveScope) UploadFileIfVersion(ctx context.Context, token, space, rel, local string, expected *int64, progress drive.ProgressFunc) error {
	s.cacheMu.Lock()
	fail := s.failUpload
	if fail {
		s.failures++
	}
	s.cacheMu.Unlock()
	if fail {
		return errors.New("isolated cache test: injected upload failure")
	}
	return s.nativeLiveScope.UploadFileIfVersion(ctx, token, space, rel, local, expected, progress)
}

func cacheLiveExpectedOnline(err error, rel string) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !cacheLiveExpectedOnline(child, rel) {
				return false
			}
		}
		return true
	}
	return err.Error() == rel+" [skipped]: 占位符未完整水合、重解析点或非普通文件，未读取"
}

func cacheLiveAllocated(t *testing.T, path string) int64 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal("测试文件路径无效")
	}
	get := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")
	var high uint32
	low, _, callErr := get.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xffffffff && callErr != windows.ERROR_SUCCESS {
		t.Fatal("测试文件实际分配量读取失败")
	}
	return int64(uint64(high)<<32 | uint64(uint32(low)))
}

// 只接受本轮已知原 ID 的 purge 之后，对同一文件路径的一次新建补账事件。
// 出现更新、改名、目录、不同大小、多 ID 或事件顺序不符，全部保留供人工核查。
func cacheLiveCleanupGhost(changes []drive.Change, original drive.Object) (int64, error) {
	var purged, ghost, createdVersion int64
	for _, change := range changes {
		if change.FileId == original.FileId && change.Event == drive.ChangePurged {
			purged = change.SpaceVersion.Int64()
		}
		if change.Path != original.Path && change.OldPath != original.Path {
			continue
		}
		if change.FileId == original.FileId {
			if change.Event != drive.ChangeDeleted {
				return 0, errors.New("原文件发生了清理之外的事件")
			}
			continue
		}
		if change.Event != drive.ChangeCreated || change.Directory || change.Size != original.Size || ghost != 0 || change.FileId.Int64() <= 0 || change.OldPath != "" {
			return 0, errors.New("同名身份变化不能证明为单次补账")
		}
		ghost, createdVersion = change.FileId.Int64(), change.SpaceVersion.Int64()
	}
	if purged <= 0 || (ghost != 0 && createdVersion <= purged) {
		return 0, errors.New("清理事件顺序未确认")
	}
	return ghost, nil
}

func cacheLiveRequireGone(ctx context.Context, signed string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	if err != nil {
		return errors.New("对象缺失检查构造失败")
	}
	req.Header.Set("Range", "bytes=0-0")
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("对象缺失检查失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		return errors.New("对象仍存在或请求未获明确 404")
	}
	return nil
}

func TestCacheLiveCleanupGhostScope(t *testing.T) {
	original := drive.Object{FileId: 71, Path: "owned/file", Size: 9}
	baseline := []drive.Change{{SpaceVersion: 11, Event: drive.ChangeDeleted, FileId: 71, Path: original.Path}, {SpaceVersion: 12, Event: drive.ChangePurged, FileId: 71}, {SpaceVersion: 13, Event: drive.ChangeCreated, FileId: 72, Path: original.Path, Size: 9}}
	if got, err := cacheLiveCleanupGhost(baseline, original); err != nil || got != 72 {
		t.Fatalf("valid exact ghost: %d %v", got, err)
	}
	if got, err := cacheLiveCleanupGhost(baseline[:2], original); err != nil || got != 0 {
		t.Fatalf("no ghost: %d %v", got, err)
	}
	for _, test := range []struct {
		name   string
		mutate func([]drive.Change) []drive.Change
	}{
		{"update", func(v []drive.Change) []drive.Change { v[2].Event = drive.ChangeUpdated; return v }},
		{"directory", func(v []drive.Change) []drive.Change { v[2].Directory = true; return v }},
		{"size", func(v []drive.Change) []drive.Change { v[2].Size++; return v }},
		{"before-purge", func(v []drive.Change) []drive.Change { v[2].SpaceVersion = 10; return v }},
		{"another-change", func(v []drive.Change) []drive.Change {
			return append(v, drive.Change{FileId: 72, Path: original.Path, Event: drive.ChangeUpdated})
		}},
		{"second-id", func(v []drive.Change) []drive.Change {
			return append(v, drive.Change{FileId: 73, Path: original.Path, Event: drive.ChangeCreated, Size: 9})
		}},
		{"missing-purge", func(v []drive.Change) []drive.Change { return append(v[:1], v[2]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changes := test.mutate(append([]drive.Change(nil), baseline...))
			if _, err := cacheLiveCleanupGhost(changes, original); err == nil {
				t.Fatal("unsafe identity accepted for cleanup")
			}
		})
	}
}
