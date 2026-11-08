//go:build windows

package main

import (
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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

// TestLiveNativeCFAPIIsolated 使用真实 Windows CFAPI、控制面和对象存储。
// 必须同时显式设置 EASYSHARE_LIVE_CFAPI=1、EASYSHARE_LIVE_DRIVE=1，以及
// EASYSHARE_LIVE_BASE / EASYSHARE_LIVE_USER_A / EASYSHARE_LIVE_PASS_A。
// go test . -run '^TestLiveNativeCFAPIIsolated$' -count=1 -v -timeout 10m
// 建议使用独立测试账号；也可显式注入 EASYSHARE_LIVE_TOKEN 复用已验证的测试会话。
// 不调用 App.Login、生产挂载生命周期或 namespace，也不终止任何用户进程。
func TestLiveNativeCFAPIIsolated(t *testing.T) {
	if os.Getenv("EASYSHARE_LIVE_CFAPI") != "1" || os.Getenv("EASYSHARE_LIVE_DRIVE") != "1" {
		t.Skip("真实 CFAPI/server 测试需 EASYSHARE_LIVE_CFAPI=1 且 EASYSHARE_LIVE_DRIVE=1")
	}
	workspace := nativeLiveWorkspace(t)
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("EASYSHARE_LIVE_BASE")), "/")
	if !cfapi.Available() {
		t.Fatal("显式启用真实测试，但本机不可用 CFAPI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	// 所有网络失败仅输出阶段，不打印 token、密码、原始网络错误或预签名 URL。
	session, err := nativeLiveSession(ctx, base)
	if err != nil || session == nil || session.Token == "" {
		t.Fatal("真实测试账号登录失败（网络错误已隐藏）")
	}
	token := session.Token
	client := drive.New(base)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("生成隔离随机身份失败")
	}
	identity := "easyshare-native-live-" + hex.EncodeToString(nonce[:])
	prefix := "live-native-cfapi-" + hex.EncodeToString(nonce[:])
	inScope := func(p string) bool { return p == prefix || strings.HasPrefix(p, prefix+"/") }
	// 清理白名单在首个写入前确定；永远不把远端枚举结果当作递归删除清单。
	known := []string{"", "docs", "docs/deep", "docs/empty", "root-seed.txt", "unread.txt", "docs/deep/seed.txt", "created.txt", "docs/created.txt", "docs/remote-added.txt"}
	rows, err := client.Objects(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal("隔离路径预检：objects 失败")
	}
	for _, row := range rows {
		if inScope(row.Path) {
			t.Fatal("随机测试前缀已有对象，拒绝接管")
		}
	}
	recycled, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatal("隔离路径预检：recycle 失败")
	}
	for _, row := range recycled {
		if inScope(row.Path) || inScope(row.OriginPath) {
			t.Fatal("随机测试前缀已有回收站项，拒绝接管")
		}
	}
	// 不使用 t.TempDir：卸载失败时自动 RemoveAll 会破坏仍注册的同步根。
	local, err := os.MkdirTemp(workspace, "easyshare-native-live-")
	if err != nil {
		t.Fatal("创建临时测试目录失败")
	}
	root, state := filepath.Join(local, "root"), filepath.Join(local, "state")
	var mount *cfapi.Mount
	mountAttempted := false
	t.Cleanup(func() {
		if mount != nil {
			if err := mount.Close(); err != nil {
				t.Errorf("Close 失败；保留同步根、状态与远端测试数据以便恢复：%s", local)
				return
			}
			if err := cfapi.UnregisterSyncRoot(root); err != nil {
				t.Errorf("隔离测试显式注销失败，保留数据：%v", err)
				return
			}
		} else if mountAttempted {
			// MountSpace 返回错误时不能证明所有注册步骤均已回滚，远端也暂不删除。
			t.Errorf("挂载失败，保守保留本地根、状态及远端测试数据：%s", local)
			return
		}
		cleanCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		paths := append([]string(nil), known...)
		sort.Slice(paths, func(i, j int) bool {
			depth := func(p string) int {
				if p == "" {
					return 0
				}
				return strings.Count(p, "/") + 1
			}
			di, dj := depth(paths[i]), depth(paths[j])
			if di != dj {
				return di > dj
			}
			// 同层文件先于目录；根最后，绝不递归删除。
			return len(paths[i]) > len(paths[j])
		})
		for _, rel := range paths {
			p := prefix
			if rel != "" {
				p += "/" + rel
			}
			current, listErr := client.Objects(cleanCtx, token, drive.SpacePersonal)
			if listErr != nil {
				t.Errorf("清理 %s：objects 失败，拒绝猜测身份", p)
				continue
			}
			for _, row := range current {
				if row.Path != p {
					continue
				}
				if row.FileId.Int64() <= 0 {
					t.Errorf("清理 %s：无有效 fileId", p)
					continue
				}
				if err := nativeLiveCleanupDelete(cleanCtx, base, token, p, row.FileId.Int64()); err != nil {
					t.Errorf("清理 %s fileId=%d：精确软删失败：%v", p, row.FileId.Int64(), err)
				}
			}
			entries, listErr := client.RecycleList(cleanCtx, token, drive.SpacePersonal)
			if listErr != nil {
				t.Errorf("清理 %s：recycle 失败", p)
				continue
			}
			for _, row := range entries {
				if row.Path != p && row.OriginPath != p {
					continue
				}
				if row.FileId.Int64() <= 0 || (row.Path != "" && row.Path != p) || (row.OriginPath != "" && row.OriginPath != p) {
					t.Errorf("清理 %s：回收站身份/路径不一致，拒绝 purge", p)
					continue
				}
				if client.RecyclePurge(cleanCtx, token, drive.SpacePersonal, row.FileId.Int64()) != nil {
					t.Errorf("清理 %s：精确 purge 失败", p)
				}
			}
		}
		remaining, listErr := client.Objects(cleanCtx, token, drive.SpacePersonal)
		if listErr != nil {
			t.Error("清理验收 objects 失败")
		}
		for _, row := range remaining {
			if inScope(row.Path) {
				t.Errorf("远端残留（不扩大删除范围）：%s", row.Path)
			}
		}
		entries, listErr := client.RecycleList(cleanCtx, token, drive.SpacePersonal)
		if listErr != nil {
			t.Error("清理验收 recycle 失败")
		}
		for _, row := range entries {
			if inScope(row.Path) || inScope(row.OriginPath) {
				t.Error("测试前缀回收站有残留（不扩大删除范围）")
			}
		}
		if !mountAttempted || mount != nil {
			if os.RemoveAll(local) != nil {
				t.Errorf("删除已安全卸载的临时目录失败：%s", local)
			}
		}
	})

	createRemoteDir := func(rel string) {
		t.Helper()
		p := prefix
		if rel != "" {
			p += "/" + rel
		}
		o, err := client.CreateDirectory(ctx, token, drive.SpacePersonal, p)
		if err != nil || o == nil || !o.Directory || o.Path != p || o.FileId.Int64() <= 0 {
			t.Fatalf("远端前置目录创建失败：%s（需部署持久目录协议）", p)
		}
	}
	for _, rel := range []string{"", "docs", "docs/deep"} {
		createRemoteDir(rel)
	}
	seed := map[string]string{
		"root-seed.txt":      "native root hydration seed\n",
		"docs/deep/seed.txt": "native deep hydration seed\n",
		"unread.txt":         "must remain unhydrated\n",
	}
	uploadRemote := func(rel, content string) {
		t.Helper()
		if client.Upload(ctx, token, drive.SpacePersonal, prefix+"/"+rel, strings.NewReader(content), int64(len(content))) != nil {
			t.Fatalf("独立远端准备上传失败：%s", rel)
		}
	}
	for rel, content := range seed {
		uploadRemote(rel, content)
	}
	scope := &nativeLiveScope{client: client, prefix: prefix, reads: make(map[string]int)}
	guid, err := windows.GenerateGUID()
	if err != nil || guid == cfapiPersonalProviderID {
		t.Fatal("生成独立非生产 provider GUID 失败")
	}
	cfg := cfmount.Config{RootPath: root, Space: drive.SpacePersonal, Client: scope,
		Token: func() string { return token }, ProviderName: "EasyShare isolated live test", ProviderID: guid, Writable: true}
	// 单独的能力探测超时不能成为挂载生命周期；显式覆盖新的可选 lifetime 参数。
	probeCtx, stopProbe := context.WithTimeout(ctx, 60*time.Second)
	list, reconcile, err := prepareNativeCFAPI(probeCtx, cfg, state, identity, ctx)
	stopProbe()
	if err != nil {
		t.Fatal("prepareNativeCFAPI 失败（能力探测或写回接口不可用，网络错误已隐藏）")
	}
	if os.MkdirAll(root, 0700) != nil {
		t.Fatal("创建隔离 root 失败")
	}
	mountAttempted = true
	mount, err = cfapi.MountSpace(cfapi.MountOptions{RootPath: root,
		Provider:         cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "live-test", ID: guid},
		SyncRootIdentity: []byte(identity), List: list, Open: cfmount.Open(cfg)})
	if err != nil {
		t.Fatal("真实 MountSpace 失败（错误隐藏；本地目录将保留）")
	}
	for _, rel := range []string{"root-seed.txt", "unread.txt", "docs/deep/seed.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("初始投影缺失：%s", rel)
		}
	}
	if scope.readCount("") != 0 {
		t.Fatal("初始枚举触发了内容下载")
	}
	tick := func() {
		t.Helper()
		if ctx.Err() != nil {
			t.Fatal("真实测试超时")
		}
		// 未读占位符被有意跳过，生产适配器会将其作为 skipped issue 返回。
		// 仅接受已知远端种子的精确 skipped 叶子；冲突/目录/网络/树同步等错误仍失败。
		if err := reconcile(mount); !nativeLiveExpectedUnhydrated(err) {
			t.Fatal("保护模式 reconcile 失败（非预期未水合跳过，原始错误已隐藏）")
		}
	}
	// 在保护树同步后验证根/深层按需水合，而不是只验证裸 MountSpace。
	tick()
	if scope.readCount("") != 0 {
		t.Fatal("保护模式初始扫描下载了未读内容")
	}
	localHash := func(rel, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || sha256.Sum256(got) != sha256.Sum256([]byte(want)) {
			t.Fatalf("本地水合 SHA-256 不符：%s", rel)
		}
	}
	localHash("root-seed.txt", seed["root-seed.txt"])
	localHash("docs/deep/seed.txt", seed["docs/deep/seed.txt"])
	if scope.readCount("root-seed.txt") == 0 || scope.readCount("docs/deep/seed.txt") == 0 {
		t.Fatal("读取未经过真实下载适配器")
	}
	// 默认稳定期为 2s；先观察并等待，再 reconcile 建立水合文件基线，之后才允许编辑。
	tick()
	time.Sleep(3 * time.Second)
	tick()

	// 独立新客户端验收，不复用 scope/写回缓存；不重新登录以免挤掉现有会话。
	verifier := drive.New(base)
	verify := func(rel, want string) int64 {
		t.Helper()
		objects, err := verifier.Objects(ctx, token, drive.SpacePersonal)
		if err != nil {
			t.Fatal("独立 objects 验收失败")
		}
		var found *drive.Object
		for i := range objects {
			if objects[i].Path == prefix+"/"+rel {
				if found != nil {
					t.Fatalf("远端路径重复：%s", rel)
				}
				found = &objects[i]
			}
		}
		if found == nil || found.Directory || found.FileId.Int64() <= 0 || found.VersionId.Int64() <= 0 || found.Size != int64(len(want)) {
			t.Fatalf("独立远端文件身份/大小/版本验收失败：%s", rel)
		}
		body, _, err := verifier.Open(ctx, token, drive.SpacePersonal, prefix+"/"+rel)
		if err != nil {
			t.Fatalf("独立下载失败：%s", rel)
		}
		got, readErr := io.ReadAll(io.LimitReader(body, int64(len(want))+1))
		closeErr := body.Close()
		if readErr != nil || closeErr != nil || sha256.Sum256(got) != sha256.Sum256([]byte(want)) {
			t.Fatalf("独立下载 SHA-256 验收失败：%s", rel)
		}
		t.Logf("独立下载验收 %s version=%d sha256=%x", rel, found.VersionId.Int64(), sha256.Sum256(got))
		return found.VersionId.Int64()
	}
	rootVersion := verify("root-seed.txt", seed["root-seed.txt"])
	deepVersion := verify("docs/deep/seed.txt", seed["docs/deep/seed.txt"])
	write := func(rel, content string) {
		t.Helper()
		if os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0600) != nil {
			t.Fatalf("普通本地文件写入失败：%s", rel)
		}
		tick()
		time.Sleep(3 * time.Second)
		tick()
		localHash(rel, content)
	}
	write("created.txt", "ordinary root new file\n")
	v1 := verify("created.txt", "ordinary root new file\n")
	write("created.txt", "ordinary root edited file v2\n")
	if verify("created.txt", "ordinary root edited file v2\n") <= v1 {
		t.Fatal("根新增文件编辑未推进版本")
	}
	write("docs/created.txt", "ordinary child inside cloud directory\n")
	v1 = verify("docs/created.txt", "ordinary child inside cloud directory\n")
	write("docs/created.txt", "edited child inside cloud directory v2\n")
	if verify("docs/created.txt", "edited child inside cloud directory v2\n") <= v1 {
		t.Fatal("Cloud 目录内文件编辑未推进版本")
	}
	write("root-seed.txt", "edited hydrated root\n")
	if verify("root-seed.txt", "edited hydrated root\n") <= rootVersion {
		t.Fatal("根水合文件编辑未推进版本")
	}
	write("docs/deep/seed.txt", "edited hydrated deep file\n")
	if verify("docs/deep/seed.txt", "edited hydrated deep file\n") <= deepVersion {
		t.Fatal("深层水合文件编辑未推进版本")
	}

	if os.Mkdir(filepath.Join(root, "docs", "empty"), 0700) != nil {
		t.Fatal("本地创建空目录失败")
	}
	tick()
	time.Sleep(3 * time.Second)
	tick()
	var emptyID int64
	for attempt := 0; attempt < 2; attempt++ {
		objects, err := drive.New(base).Objects(ctx, token, drive.SpacePersonal)
		if err != nil {
			t.Fatal("空目录独立持久化列表失败")
		}
		found := false
		for _, o := range objects {
			if strings.HasPrefix(o.Path, prefix+"/docs/empty/") {
				t.Fatal("待验收空目录意外出现子项")
			}
			if o.Path != prefix+"/docs/empty" {
				continue
			}
			if found || !o.Directory || o.FileId.Int64() <= 0 {
				t.Fatal("空目录真实持久身份无效或重复")
			}
			found = true
			if attempt == 0 {
				emptyID = o.FileId.Int64()
			} else if emptyID != o.FileId.Int64() {
				t.Fatal("空目录持久身份变化")
			}
		}
		if !found {
			t.Fatal("本地空目录未持久化到真实服务端")
		}
		entries, err := os.ReadDir(filepath.Join(root, "docs", "empty"))
		if err != nil || len(entries) != 0 {
			t.Fatal("本地空目录投影异常")
		}
		tick()
	}
	const remoteAdded = "remote docs addition projected through protected sync\n"
	uploadRemote("docs/remote-added.txt", remoteAdded)
	tick()
	if _, err := os.Stat(filepath.Join(root, "docs", "remote-added.txt")); err != nil {
		t.Fatal("远端 docs 新增未投影到受保护 Cloud 目录")
	}
	if scope.readCount("docs/remote-added.txt") != 0 {
		t.Fatal("远端新增投影提前下载内容")
	}
	localHash("docs/remote-added.txt", remoteAdded)
	verify("docs/remote-added.txt", remoteAdded)
	if scope.readCount("unread.txt") != 0 {
		t.Fatal("全流程意外下载未读文件")
	}
	t.Log("真实 CFAPI 隔离验收通过；卸载后按精确测试路径/fileId 清理")
}

// nativeLiveScope 不匿名嵌入真实客户端，避免泄露未受约束的操作能力。
// 对路径做严格校验而非清洗：绝对路径、反斜线、冒号、空段、点段一律拒绝。
// 所有挂载内容请求只能映射到随机前缀下；Objects 丢弃根对象和其他用户路径。
type nativeLiveScope struct {
	client *drive.Client
	prefix string
	mu     sync.Mutex
	reads  map[string]int
}

var (
	_ cfmount.Lister              = (*nativeLiveScope)(nil)
	_ cfmount.Presigner           = (*nativeLiveScope)(nil)
	_ cfmount.WritebackClient     = (*nativeLiveScope)(nil)
	_ cfmount.DirectoryCreator    = (*nativeLiveScope)(nil)
	_ nativeDirectoryCapabilities = (*nativeLiveScope)(nil)
)

func (s *nativeLiveScope) remote(space, rel string) (string, error) {
	if space != drive.SpacePersonal || rel == "" || strings.ContainsAny(rel, "\\:\x00") || path.IsAbs(rel) || path.Clean(rel) != rel {
		return "", errors.New("隔离适配器拒绝非法空间或路径")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part {
			return "", errors.New("隔离适配器拒绝非法路径段")
		}
	}
	return s.prefix + "/" + rel, nil
}

func (s *nativeLiveScope) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	if space != drive.SpacePersonal {
		return nil, errors.New("隔离适配器拒绝其他空间")
	}
	rows, err := s.client.Objects(ctx, token, space)
	if err != nil {
		return nil, errors.New("隔离远端 objects 失败")
	}
	var out []drive.Object
	for _, row := range rows {
		if !strings.HasPrefix(row.Path, s.prefix+"/") {
			continue
		}
		rel := strings.TrimPrefix(row.Path, s.prefix+"/")
		mapped, err := s.remote(space, rel)
		if err != nil || mapped != row.Path {
			return nil, errors.New("隔离远端返回非法路径")
		}
		row.Path = rel
		out = append(out, row)
	}
	return out, nil
}

func (s *nativeLiveScope) recordRead(rel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads[rel]++
}

func (s *nativeLiveScope) readCount(rel string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rel != "" {
		return s.reads[rel]
	}
	n := 0
	for _, count := range s.reads {
		n += count
	}
	return n
}

func (s *nativeLiveScope) Open(ctx context.Context, token, space, rel string) (io.ReadCloser, int64, error) {
	p, err := s.remote(space, rel)
	if err != nil {
		return nil, 0, err
	}
	s.recordRead(rel)
	body, size, err := s.client.Open(ctx, token, space, p)
	if err != nil {
		return nil, 0, errors.New("隔离远端 Open 失败")
	}
	return body, size, nil
}

func (s *nativeLiveScope) PresignGet(ctx context.Context, token, space, rel string) (string, error) {
	p, err := s.remote(space, rel)
	if err != nil {
		return "", err
	}
	s.recordRead(rel)
	url, err := s.client.PresignGet(ctx, token, space, p)
	if err != nil {
		return "", errors.New("隔离远端 PresignGet 失败")
	}
	return url, nil
}

func (s *nativeLiveScope) UploadFileIfVersion(ctx context.Context, token, space, rel, local string, expected *int64, progress drive.ProgressFunc) error {
	p, err := s.remote(space, rel)
	if err != nil {
		return err
	}
	if s.client.UploadFileIfVersion(ctx, token, space, p, local, expected, progress) != nil {
		return errors.New("隔离远端 CAS 上传失败")
	}
	return nil
}

func (s *nativeLiveScope) CreateDirectory(ctx context.Context, token, space, rel string) (*drive.Object, error) {
	p, err := s.remote(space, rel)
	if err != nil {
		return nil, err
	}
	o, err := s.client.CreateDirectory(ctx, token, space, p)
	if err != nil || o == nil {
		return nil, errors.New("隔离远端创建目录失败")
	}
	if o.Path != p || !o.Directory || o.FileId.Int64() <= 0 {
		return nil, errors.New("隔离远端目录身份无效")
	}
	copy := *o
	copy.Path = rel
	return &copy, nil
}

func (s *nativeLiveScope) SupportsPersistentDirectories(ctx context.Context, token, space string) (bool, error) {
	if space != drive.SpacePersonal {
		return false, errors.New("隔离适配器拒绝其他空间")
	}
	supported, err := s.client.SupportsPersistentDirectories(ctx, token, space)
	if err != nil {
		return false, errors.New("隔离远端目录能力探测失败")
	}
	return supported, nil
}

// nativeLiveExpectedUnhydrated 只忽略适配器对已知未读文件产生的精确 skipped issue。
// 必须逐个检查 errors.Join 的叶子，不能用 Contains("skipped") 吞掉同时存在的真实错误。
func nativeLiveExpectedUnhydrated(err error) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !nativeLiveExpectedUnhydrated(child) {
				return false
			}
		}
		return true
	}
	for _, rel := range []string{"root-seed.txt", "docs/deep/seed.txt", "unread.txt", "docs/remote-added.txt"} {
		if err.Error() == rel+" [skipped]: 占位符未完整水合、重解析点或非普通文件，未读取" {
			return true
		}
	}
	return false
}

func TestNativeLiveExpectedUnhydrated(t *testing.T) {
	skipped := errors.New("unread.txt [skipped]: 占位符未完整水合、重解析点或非普通文件，未读取")
	if !nativeLiveExpectedUnhydrated(errors.Join(nil, skipped)) {
		t.Fatal("必须允许已知未读文件的预期跳过")
	}
	for _, err := range []error{
		errors.Join(skipped, errors.New("真实网络失败")),
		errors.New("docs [skipped]: 占位符未完整水合、重解析点或非普通文件，未读取"),
		errors.New("unread.txt [conflict]: remote changed"),
	} {
		if nativeLiveExpectedUnhydrated(err) {
			t.Fatal("不能忽略目录跳过、冲突或混合真实失败")
		}
	}
}

// 测试专用诊断保留 HTTP/业务 code，不输出响应原文、令牌或 URL。
// 只重试明确的网关暂态错误；权限、目录非空等业务失败不掩盖、不猜测子项。
func nativeLiveCleanupDelete(ctx context.Context, base, token, p string, id int64) error {
	if id <= 0 {
		return errors.New("拒绝无效 fileId")
	}
	payload, _ := json.Marshal(map[string]any{"space": drive.SpacePersonal, "path": p, "fileId": id})
	client := &http.Client{Timeout: 15 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(base, "/")+"/easyshare/drive/object", strings.NewReader(string(payload)))
		if err != nil {
			return errors.New("cleanup request 构造失败")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("clientid", "e5cd7e4891bf95d1d19206ce24a7b32e")
		resp, err := client.Do(req)
		if err != nil {
			return errors.New("cleanup transport 失败（详情隐藏）")
		}
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
		resp.Body.Close()
		if resp.StatusCode/100 == 2 && decodeErr == nil && result.Code == 200 {
			return nil
		}
		category := "business-or-response-error"
		if result.Msg == "目录非空，请先处理子项" {
			category = "directory-not-empty"
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 || result.Code == 401 || result.Code == 403 {
			category = "authorization-denied"
		}
		transient := resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
		if transient && attempt < 2 {
			select {
			case <-time.After(time.Duration(attempt+1) * time.Second):
				continue
			case <-ctx.Done():
				return errors.New("cleanup context 已结束")
			}
		}
		return fmt.Errorf("HTTP=%d code=%d category=%s attempts=%d", resp.StatusCode, result.Code, category, attempt+1)
	}
	return errors.New("cleanup 有限重试耗尽")
}
