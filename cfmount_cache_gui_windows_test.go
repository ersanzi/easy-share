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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"easyshare/internal/namespace"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 显式三闸门：独立服务器前缀、真实此电脑入口、真实根目录操作。不改正式入口。
func TestLiveCacheThisPCIsolated(t *testing.T) {
	if os.Getenv("EASYSHARE_LIVE_CFAPI") != "1" || os.Getenv("EASYSHARE_LIVE_DRIVE") != "1" || os.Getenv("EASYSHARE_LIVE_CACHE_GUI") != "1" {
		t.Skip("真实此电脑验收需显式启用三个 live 开关")
	}
	workspace := nativeLiveWorkspace(t)
	wd := filepath.Dir(workspace)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	base := os.Getenv("EASYSHARE_LIVE_BASE")
	session, err := nativeLiveSession(ctx, base)
	if err != nil || session == nil || session.Token == "" || session.User.UserID == "" {
		t.Fatal("真实测试账号登录失败（详情隐藏）")
	}
	token := session.Token
	client := drive.New(base)
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(random[:])
	prefix := "入口验收-" + strings.ToUpper(nonce[:6])
	rows, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal("GUI 远端基线查询失败")
	}
	for _, row := range rows {
		if row.Path == prefix || strings.HasPrefix(row.Path, prefix+"/") {
			t.Fatal("GUI 随机前缀冲突")
		}
	}
	recycled, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal("GUI 回收站基线查询失败")
	}
	for _, row := range recycled {
		if row.Path == prefix || row.OriginPath == prefix || strings.HasPrefix(row.Path, prefix+"/") || strings.HasPrefix(row.OriginPath, prefix+"/") {
			t.Fatal("GUI 随机前缀已在回收站")
		}
	}
	local, err := os.MkdirTemp(workspace, "live-cache-gui-")
	if err != nil {
		t.Fatal(err)
	}
	root, state := filepath.Join(local, "root"), filepath.Join(local, "state")
	source, export := filepath.Join(local, "source"), filepath.Join(local, "export")
	for _, dir := range []string{root, source, export} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	owned := map[string]drive.Object{}
	var mount *cfapi.Mount
	var entry namespace.Entry
	registered, completed := false, false
	commands := filepath.Join(wd, "tools", "gui", "cache-root-actions.ps1")
	gui := func(action string) error {
		parent := ctx
		if action == "Close" {
			parent = context.Background()
		}
		stageCtx, stop := context.WithTimeout(parent, 70*time.Second)
		defer stop()
		output, err := exec.CommandContext(stageCtx, "powershell.exe", "-STA", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", commands, "-RunDir", local, "-Action", action).CombinedOutput()
		_ = os.WriteFile(filepath.Join(local, "gui-"+action+".log"), output, 0600)
		if err != nil || !bytes.Contains(output, []byte("GUI_ROOT_PHASE_OK "+action)) {
			return fmt.Errorf("GUI %s 失败；详情见本轮记录", action)
		}
		return nil
	}
	t.Cleanup(func() {
		proof, _ := json.MarshalIndent(map[string]any{"prefix": prefix, "nonce": nonce, "local": local, "owned": owned, "guiPassed": completed}, "", "  ")
		_ = os.WriteFile(filepath.Join(local, "owned.json"), proof, 0600)
		if registered {
			if _, err := os.Stat(filepath.Join(local, "entry.json")); err == nil {
				if err = gui("Close"); err != nil {
					t.Error(err)
				}
			}
			if err := namespace.Unregister([]namespace.Entry{entry}); err != nil {
				t.Error("注销隔离入口失败")
			}
		}
		if mount != nil {
			if err := mount.Close(); err != nil {
				t.Error("断开隔离 GUI 根失败")
				return
			}
		}
		if !completed || t.Failed() {
			t.Logf("GUI_NOT_ACCEPTED_RETAINED: %s remote=%s", local, prefix)
			return
		}
		if err := cfapi.UnregisterSyncRoot(root); err != nil {
			t.Error("GUI 根安全注销失败")
			return
		}
		cleanCtx, stop := context.WithTimeout(context.Background(), 90*time.Second)
		defer stop()
		current, err := client.Objects(cleanCtx, token, drive.SpacePersonal)
		if err != nil {
			t.Error("GUI 清理前远端核对失败")
			return
		}
		count := 0
		for _, row := range current {
			if row.Path != prefix && !strings.HasPrefix(row.Path, prefix+"/") {
				continue
			}
			want, ok := owned[row.Path]
			if !ok || row.FileId != want.FileId || row.VersionId != want.VersionId {
				t.Error("GUI 清理前身份或版本变化，不清理")
				return
			}
			count++
		}
		if count != len(owned) {
			t.Error("GUI 清理前数量不匹配")
			return
		}
		for _, name := range []string{"新建.txt", "复制.txt", "拖入.txt"} {
			file := owned[prefix+"/"+name]
			if err := cacheGUICleanFile(cleanCtx, client, base, token, file); err != nil {
				t.Error(err)
				return
			}
		}
		folder := owned[prefix]
		if err := nativeLiveCleanupDelete(cleanCtx, base, token, prefix, folder.FileId.Int64()); err != nil {
			t.Errorf("GUI 目录清理失败: %v", err)
			return
		}
		if client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, folder.FileId.Int64()) != nil {
			t.Error("GUI 目录 purge 失败")
			return
		}
		current, err = client.Objects(cleanCtx, token, drive.SpacePersonal)
		if err != nil {
			t.Error("GUI 最终清单检查失败")
			return
		}
		for _, row := range current {
			if row.Path == prefix || strings.HasPrefix(row.Path, prefix+"/") {
				t.Error("GUI 存在远端残留")
				return
			}
		}
		final, _ := json.MarshalIndent(map[string]any{"prefix": prefix, "owned": owned, "guiPassed": true, "visibleRemoteClean": true, "isolatedEntryRemoved": true}, "", "  ")
		if err = os.WriteFile(filepath.Join(local, "result.json"), final, 0600); err != nil {
			t.Error(err)
		}
		t.Logf("THIS_PC_GUI_CLEAN: evidence retained at %s, isolated entry and exact visible test objects removed", local)
	})
	folder, err := client.CreateDirectory(ctx, token, drive.SpacePersonal, prefix)
	if err != nil || folder == nil || !folder.Directory || folder.FileId.Int64() <= 0 {
		t.Fatal("GUI 测试目录创建失败")
	}
	owned[prefix] = *folder
	scope := &nativeLiveScope{client: client, prefix: prefix, reads: map[string]int{}}
	guid, err := windows.GenerateGUID()
	if err != nil || guid == cfapiPersonalProviderID {
		t.Fatal("GUI provider生成失败")
	}
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte(base+"\x00"+session.User.UserID+"\x00"+nonce)))
	cfg := cfmount.Config{RootPath: root, Space: drive.SpacePersonal, Client: scope, Token: func() string { return token }, ProviderName: "EasyShare GUI acceptance", ProviderID: guid, Writable: true, CacheIdleFor: 4 * time.Second}
	var reconcile func(*cfapi.Mount) error
	connect := func() {
		t.Helper()
		list, cycle, err := prepareNativeCFAPI(ctx, cfg, state, identity, ctx)
		if err != nil {
			t.Fatal("GUI 写回配置失败")
		}
		mount, err = cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "gui-test", ID: guid}, SyncRootIdentity: []byte(identity), List: list, Open: cfmount.Open(cfg)})
		if err != nil {
			t.Fatal("GUI CFAPI 连接失败")
		}
		reconcile = cycle
	}
	connect()
	namespaceID, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	entry = namespace.SpaceEntries("", []namespace.SpaceMount{{Kind: "personal", TargetPath: root, UserID: session.User.UserID}})[0]
	entry.CLSID = namespaceID.String()
	entry.Name = "云盘验收"
	entry.Description = "隔离研发：真实服务器云盘根目录"
	for _, key := range []string{`Software\Classes\CLSID\` + entry.CLSID, `Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\` + entry.CLSID} {
		k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
		if err == nil {
			k.Close()
			t.Fatal("随机入口GUID已存在")
		}
		if err != registry.ErrNotExist {
			t.Fatal(err)
		}
	}
	if err = namespace.Register([]namespace.Entry{entry}); err != nil {
		t.Fatal(err)
	}
	registered = true
	names := map[string]string{"new": "新建.txt", "copy": "复制.txt", "drag": "拖入.txt"}
	contents := map[string]string{}
	hashes := map[string]string{}
	for _, kind := range []string{"new", "edit", "copy", "drag"} {
		contents[kind] = strings.Repeat("cloud-gui-"+kind+"-"+nonce+"\r\n", 32)
		hashes[kind] = fmt.Sprintf("%x", sha256.Sum256([]byte(contents[kind])))
		if kind == "copy" || kind == "drag" {
			if err = os.WriteFile(filepath.Join(source, names[kind]), []byte(contents[kind]), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest, _ := json.MarshalIndent(map[string]any{"root": root, "nonce": nonce, "clsid": entry.CLSID, "label": entry.Name, "names": names, "contents": contents, "sha256": hashes, "sourceDirectory": source, "exportDirectory": export}, "", "  ")
	if err = os.WriteFile(filepath.Join(local, "gui-manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	verify := func(rel string, want []byte) drive.Object {
		t.Helper()
		rows, err := client.Objects(ctx, token, drive.SpacePersonal)
		if err != nil {
			t.Fatal("GUI 独立远端清单失败")
		}
		var row drive.Object
		for _, o := range rows {
			if o.Path == prefix+"/"+rel {
				if row.FileId > 0 {
					t.Fatal("GUI 同名对象重复")
				}
				row = o
			}
		}
		if row.FileId <= 0 || row.Directory || row.Size != int64(len(want)) || row.VersionId <= 0 {
			t.Fatalf("GUI 独立远端对象属性不匹配: %s", rel)
		}
		body, _, err := drive.New(base).Open(ctx, token, drive.SpacePersonal, row.Path)
		if err != nil {
			t.Fatal("GUI 独立远端下载失败")
		}
		got, readErr := io.ReadAll(io.LimitReader(body, int64(len(want))+1))
		closeErr := body.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(got, want) {
			t.Fatal("GUI 独立远端哈希不匹配")
		}
		owned[row.Path] = row
		t.Logf("GUI_REMOTE_CONFIRMED: %s fileId=%d version=%d sha256=%x", rel, row.FileId.Int64(), row.VersionId.Int64(), sha256.Sum256(got))
		return row
	}
	reconcileOnce := func() {
		t.Helper()
		err := reconcile(mount)
		var acceptable func(error) bool
		acceptable = func(e error) bool {
			if e == nil {
				return true
			}
			if joined, ok := e.(interface{ Unwrap() []error }); ok {
				for _, part := range joined.Unwrap() {
					if !acceptable(part) {
						return false
					}
				}
				return true
			}
			for _, name := range names {
				if cacheLiveExpectedOnline(e, name) {
					return true
				}
			}
			return false
		}
		if !acceptable(err) {
			t.Fatal("GUI 自动调度异常（详情隐藏）")
		}
	}
	settle := func(rel, content string) {
		t.Helper()
		reconcileOnce()
		time.Sleep(2300 * time.Millisecond)
		reconcileOnce()
		verify(rel, []byte(content))
		time.Sleep(2200 * time.Millisecond)
		reconcileOnce()
		if len(content) > 1024 && cacheLiveAllocated(t, filepath.Join(root, rel)) != 0 {
			t.Fatalf("GUI %s 未自动回收", rel)
		}
	}
	for _, action := range []string{"Enter", "NewEmpty"} {
		if err = gui(action); err != nil {
			t.Fatal(err)
		}
	}
	settle(names["new"], "")
	first := owned[prefix+"/"+names["new"]]
	if err = gui("NewContent"); err != nil {
		t.Fatal(err)
	}
	settle(names["new"], contents["new"])
	if current := owned[prefix+"/"+names["new"]]; current.FileId != first.FileId || current.VersionId <= first.VersionId {
		t.Fatal("GUI 新建内容没有同ID递增版本")
	}
	if err = gui("Edit"); err != nil {
		t.Fatal(err)
	}
	settle(names["new"], contents["edit"])
	if err = gui("Copy"); err != nil {
		t.Fatal(err)
	}
	settle(names["copy"], contents["copy"])
	if err = gui("Drag"); err != nil {
		t.Fatal(err)
	}
	settle(names["drag"], contents["drag"])
	if err = gui("Export"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(export, names["new"])); err != nil || string(got) != contents["edit"] {
		t.Fatal("GUI 导出副本不符")
	}
	settle(names["new"], contents["edit"])
	if err = mount.Close(); err != nil {
		t.Fatal(err)
	}
	// 提供方连接完全关闭后，由两个先后退出的独立进程复用同一注册和状态。
	restart := cacheGUIRestart{Root: root, State: state, Identity: identity, Prefix: prefix, Provider: guid.String(), File: names["new"], SHA256: hashes["edit"], FileID: owned[prefix+"/"+names["new"]].FileId.Int64(), Version: owned[prefix+"/"+names["new"]].VersionId.Int64()}
	restartData, _ := json.Marshal(restart)
	restartPath := filepath.Join(local, "restart.json")
	if err = os.WriteFile(restartPath, restartData, 0600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		childCtx, stop := context.WithTimeout(ctx, 90*time.Second)
		child := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestCacheRestartProviderHelper$", "-test.v", "-test.timeout=80s")
		child.Env = append(os.Environ(), "EASYSHARE_CACHE_RESTART_MANIFEST="+restartPath, "EASYSHARE_CACHE_RESTART_TOKEN="+token)
		output, childErr := child.CombinedOutput()
		stop()
		if err = os.WriteFile(filepath.Join(local, fmt.Sprintf("restart-process-%d.log", attempt+1)), output, 0600); err != nil {
			t.Fatal(err)
		}
		if childErr != nil || !bytes.Contains(output, []byte("RESTART_PROVIDER_PASS")) {
			t.Fatal("新提供方进程的重启恢复验收失败；详情已记录")
		}
		t.Logf("RESTART_PROCESS_CONFIRMED: new provider pid=%d exited=0", child.ProcessState.Pid())
	}
	connect()
	reconcileOnce()
	if cacheLiveAllocated(t, filepath.Join(root, names["new"])) != 0 {
		t.Fatal("GUI 重连重新下载了正文")
	}
	if err = gui("Close"); err != nil {
		t.Fatal(err)
	}
	if err = gui("Enter"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, names["new"])); err != nil || string(got) != contents["edit"] {
		t.Fatal("GUI 重连后回读不符")
	}
	completed = true
	t.Log("THIS_PC_GUI_PASS: actual tile double-click, root New Text, fresh Notepad save, Explorer paste, OLE drag, automatic release, explicit export, reconnect and reenter")
}

func cacheGUICleanFile(ctx context.Context, client *drive.Client, base, token string, file drive.Object) error {
	if file.FileId.Int64() <= 0 {
		return errors.New("GUI 清理无测试身份")
	}
	signed, err := client.PresignGet(ctx, token, drive.SpacePersonal, file.Path)
	if err != nil {
		return errors.New("GUI 清理对象确认失败")
	}
	zero := int64(0)
	start, err := client.Changes(ctx, token, drive.SpacePersonal, &zero)
	if err != nil || start == nil {
		return errors.New("GUI 清理事件游标失败")
	}
	cursor := start.NewestSpaceVersion.Int64()
	if err = nativeLiveCleanupDelete(ctx, base, token, file.Path, file.FileId.Int64()); err != nil {
		return err
	}
	if client.RecyclePurge(ctx, token, drive.SpacePersonal, file.FileId.Int64()) != nil {
		return errors.New("GUI 精确purge失败")
	}
	time.Sleep(500 * time.Millisecond)
	delta, err := client.Changes(ctx, token, drive.SpacePersonal, &cursor)
	if err != nil || delta == nil {
		return errors.New("GUI 删除后事件核查失败")
	}
	ghost, err := cacheLiveCleanupGhost(delta.Changes, file)
	if err != nil {
		return err
	}
	if err = cacheLiveRequireGone(ctx, signed); err != nil {
		return err
	}
	if ghost > 0 {
		if err = nativeLiveCleanupDelete(ctx, base, token, file.Path, ghost); err != nil {
			return err
		}
		if client.RecyclePurge(ctx, token, drive.SpacePersonal, ghost) != nil {
			return errors.New("GUI 精确补账残留purge失败")
		}
	}
	return nil
}

type cacheGUIRestart struct {
	Root, State, Identity, Prefix, Provider, File, SHA256 string
	FileID, Version                                       int64
}

// 只作为父测试的独立进程运行；令牌仅进程环境传入，不落盘、不再登录。
func TestCacheRestartProviderHelper(t *testing.T) {
	manifest := os.Getenv("EASYSHARE_CACHE_RESTART_MANIFEST")
	if manifest == "" {
		t.Skip("仅父验收进程启动此恢复子进程")
	}
	workspace := nativeLiveWorkspace(t)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal("恢复清单不可读")
	}
	var input cacheGUIRestart
	if json.Unmarshal(data, &input) != nil || !nativeLiveGUIRoot(workspace, input.Root) || input.File != "新建.txt" || input.FileID <= 0 || input.Version <= 0 {
		t.Fatal("恢复清单未通过范围校验")
	}
	if filepath.Dir(input.State) != filepath.Dir(input.Root) || filepath.Dir(manifest) != filepath.Dir(input.Root) {
		t.Fatal("恢复状态与测试根不匹配")
	}
	token := os.Getenv("EASYSHARE_CACHE_RESTART_TOKEN")
	if token == "" {
		t.Fatal("恢复子进程缺少会话")
	}
	guid, err := windows.GUIDFromString(input.Provider)
	if err != nil || guid == cfapiPersonalProviderID {
		t.Fatal("恢复provider不安全")
	}
	local := filepath.Join(input.Root, input.File)
	id, err := cfapi.ReadDirectoryNTFSIdentity(local)
	if err != nil {
		t.Fatal("恢复前身份读取失败")
	}
	if cacheLiveAllocated(t, local) != 0 {
		t.Fatal("恢复前必须已仅云端")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := drive.New(os.Getenv("EASYSHARE_LIVE_BASE"))
	scope := &nativeLiveScope{client: client, prefix: input.Prefix, reads: map[string]int{}}
	cfg := cfmount.Config{RootPath: input.Root, Space: drive.SpacePersonal, Client: scope, Token: func() string { return token }, ProviderName: "EasyShare GUI acceptance", ProviderID: guid, Writable: true, CacheIdleFor: time.Millisecond}
	list, reconcile, err := prepareNativeCFAPI(ctx, cfg, input.State, input.Identity, ctx)
	if err != nil {
		t.Fatal("恢复配置失败")
	}
	mount, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: input.Root, Provider: cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "gui-test", ID: guid}, SyncRootIdentity: []byte(input.Identity), List: list, Open: cfmount.Open(cfg)})
	if err != nil {
		t.Fatal("恢复CFAPI连接失败")
	}
	defer mount.Close()
	if scope.readCount("") != 0 || cacheLiveAllocated(t, local) != 0 {
		t.Fatal("进程重启触发隐式下载")
	}
	got, err := os.ReadFile(local)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(got)) != input.SHA256 || scope.readCount(input.File) == 0 {
		t.Fatal("新提供方进程回读服务器失败")
	}
	// 刚刚水合后，必须经过两个稳定期观察，不能直接手动释放。
	_ = reconcile(mount)
	time.Sleep(2300 * time.Millisecond)
	_ = reconcile(mount)
	if cacheLiveAllocated(t, local) != 0 {
		t.Fatal("新进程自动回收没有完成")
	}
	if actual, err := cfapi.ReadDirectoryNTFSIdentity(local); err != nil || actual != id {
		t.Fatal("进程重启改变NTFS身份")
	}
	objects, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal("恢复后服务器身份复核失败")
	}
	matched := false
	for _, object := range objects {
		if object.Path == input.Prefix+"/"+input.File {
			matched = object.FileId.Int64() == input.FileID && object.VersionId.Int64() == input.Version
		}
	}
	if !matched {
		t.Fatal("新进程意外写入/改变服务器版本")
	}
	if err = mount.Close(); err != nil {
		t.Fatal("新进程断开未完成")
	}
	t.Logf("RESTART_PROVIDER_PASS pid=%d no-implicit-download exact-remote-bytes auto-release stable-fileId-version", os.Getpid())
}
