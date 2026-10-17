// Package spacedav 把一个「空间」（个人或共享）暴露为 WebDAV 文件系统，供 Windows
// 资源管理器直接挂载。
//
// 前身是建在 objectstore.Store 之上、用编译期静态凭据直连 RustFS 的
// internal/cloud/webdavfs（KI-2，已随 KI-5 清理删除）。本包改为建在
// internal/drive 客户端之上，**每个操作都经控制面**——因此
//
//   - 客户端不持任何对象存储凭据（ADR-0007 不变量 1）；
//   - 配额与共享空间的读写授权对资源管理器同样生效，不再只管客户端内的「文件」页。
//
// 这解决了一处结构性错位：在此之前，管理页里把某账号的共享权限收掉，那个人照样能从
// 「此电脑」的共享盘读写，因为那条路径根本不经过控制面。
package spacedav

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"easyshare/internal/drive"

	"golang.org/x/net/webdav"
)

// TokenFunc 返回当前登录态的令牌。
//
// 用函数而不是直接存字符串：会话会过期、会换账号，挂载点却是长期存在的。每次操作现取，
// 保证退出登录后立刻失去访问能力。返回空串表示未登录。
type TokenFunc func() string

// Client 是本包需要的控制面能力，与 *drive.Client 的对应方法一致。
// 收窄成接口是为了可测：单测里塞假实现，不必起 HTTP 服务。
type Client interface {
	Objects(ctx context.Context, token, space string) ([]drive.Object, error)
	Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error)
	Upload(ctx context.Context, token, space, path string, body io.Reader, size int64) error
	Delete(ctx context.Context, token, space, path string) error
}

// changeClient 是可选能力：支持增量列举的客户端。
//
// 生产 *drive.Client 已实现；fake/旧实现没有时自动退回全量 Objects——
// 这是刻意的向后兼容：单测不需要为了编译补一堆空方法。
type changeClient interface {
	Changes(ctx context.Context, token, space string, sinceVersion *int64) (*drive.Changes, error)
}

// listTTL 是对象清单的缓存时长。
//
// 资源管理器打开一个目录会连发多次 PROPFIND/Stat，逐次回控制面既慢又吵。缓存很短，
// 只为把「一次浏览动作」压成一次请求；任何写操作都会立刻作废它。
const listTTL = 3 * time.Second

// FS 是某个空间的 WebDAV 文件系统。
type FS struct {
	client Client
	space  string
	token  TokenFunc
	// readOnly 为 true 时拒绝一切写操作。共享空间只给了 read 权限时用。
	readOnly bool
	// label 是根目录显示名。
	label string
	// spoolDir 是上传暂存目录。WebDAV 的 PUT 是「流式写入、写完才 Close」，
	// 而配额判定需要**确切字节数**（签发时就要），所以内容必须先落到某处。
	// 落内存会让「拖一个 5 GB 文件」变成 5 GB 分配——桌面端与用户其他程序共享内存，
	// 这不只是慢，是会 OOM 崩掉且给不出可读错误。故落这里。
	// 空表示用系统临时目录（测试与未显式配置时的兜底）。
	spoolDir string

	mu       sync.Mutex
	cache    []drive.Object
	cachedAt time.Time
	// lastVersion 是上次全量/增量列举拿到的 spaceVersion。
	//
	// 有了它，同一挂载进程内的第二次列举不需要重列整空间：
	// 只拉 lastVersion 之后的变更，并把它们应用到 cache。
	// 任何写操作仍会 invalidate（写侧需要最新版本做 CAS）。
	lastVersion int64
	// versions 是跨进程持久化的版本存储；nil 时退回进程内。
	versions VersionStore
	// pendingDirs 保存**当前仍为空**的目录。
	//
	// 对象存储没有真目录，控制面的接口又拒绝以 / 结尾的键，所以空目录无处存放。
	// 不记住它，用户在资源管理器里「新建文件夹」后刷新就会消失。
	// 有 DirStore 时这张表跨进程持久化（见 dir_store.go）；没有时退回进程内存。
	pendingDirs map[string]time.Time
	// dirStore 是空目录的持久化表；nil 表示只做进程内（测试/旧装配）。
	dirStore *dirStore
}

// Options 构造 FS 所需的参数。
type Options struct {
	Client   Client
	Space    string
	Token    TokenFunc
	ReadOnly bool
	Label    string
	// SpoolDir 上传暂存目录；空则用系统临时目录。
	// 生产装配应指向数据根（与 config.json 同级），避免占用系统盘
	// ——拖进来的可能是几十 GB，系统盘通常最小。
	SpoolDir string
	// Versions 持久化上次增量版本；nil 表示只做进程内缓存。
	//
	// 生产传 NewVersionStore(dir)。有了它，桌面端重启后不会退回全量——
	// 只需带 last_version 再拉差异。旧控制面/单测不传也能工作。
	Versions VersionStore
	// DirStore 持久化「仍为空的目录」；nil 表示只做进程内存。
	//
	// 生产传 NewDirStore(dataRoot/dirs.json)：资源管理器里新建的空文件夹
	// 在桌面端重启后仍在（验收口径：新建空目录重启后仍在）。
	// 注意这是**客户端侧**持久化，不是服务端目录对象模型——跨机器不可见。
	DirStore *dirStore
}

// VersionStore 持久化某个空间的 last_version。
type VersionStore interface {
	// Load 返回上次列举拿到的空间版本；0 表示无记录（应全量重建）。
	Load(space string) int64
	// Save 保存空间版本。
	Save(space string, version int64) error
}

// memoryVersionStore 是进程内实现（测试/旧控制面回退用）。
type memoryVersionStore struct {
	versions map[string]int64
}

func (m *memoryVersionStore) Load(space string) int64 {
	return m.versions[space]
}

func (m *memoryVersionStore) Save(space string, version int64) error {
	if m.versions == nil {
		m.versions = map[string]int64{}
	}
	m.versions[space] = version
	return nil
}

// NewMemoryVersionStore 创建进程内版本存储。
func NewMemoryVersionStore() VersionStore {
	return &memoryVersionStore{versions: map[string]int64{}}
}

// New 创建某个空间的 WebDAV 文件系统。
func New(opts Options) *FS {
	label := opts.Label
	if label == "" {
		label = "EasyShare"
	}
	if opts.Versions == nil {
		opts.Versions = NewMemoryVersionStore()
	}
	// 空目录：有持久化表就从盘上恢复（重启后新建的空文件夹仍在）
	pending := map[string]time.Time{}
	if opts.DirStore != nil {
		for dir, created := range opts.DirStore.Load(opts.Space) {
			pending[dir] = created
		}
	}
	return &FS{
		client:      opts.Client,
		space:       opts.Space,
		token:       opts.Token,
		readOnly:    opts.ReadOnly,
		label:       label,
		spoolDir:    opts.SpoolDir,
		versions:    opts.Versions,
		lastVersion: opts.Versions.Load(opts.Space),
		pendingDirs: pending,
		dirStore:    opts.DirStore,
	}
}

// errReadOnly 是只读空间下写操作的统一错误。
// 用 os.ErrPermission 让 webdav 层翻成 403，资源管理器会给出「拒绝访问」而不是无声失败。
var errReadOnly = fmt.Errorf("空间为只读：%w", os.ErrPermission)

// tokenOrErr 取当前令牌，未登录时报错。
func (f *FS) tokenOrErr() (string, error) {
	if f.token == nil {
		return "", fmt.Errorf("未配置登录态：%w", os.ErrPermission)
	}
	token := f.token()
	if token == "" {
		return "", fmt.Errorf("请先登录：%w", os.ErrPermission)
	}
	return token, nil
}

// objects 取对象清单，带短缓存。
func (f *FS) objects(ctx context.Context) ([]drive.Object, error) {
	token, err := f.tokenOrErr()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	if f.cache != nil && time.Since(f.cachedAt) < listTTL {
		cached := f.cache
		f.mu.Unlock()
		return cached, nil
	}
	f.mu.Unlock()

	// 优先走增量：同进程内第二次及以后的列举只拉差异，不再重列整空间。
	//
	// 这正是路线图切片 2 的生产落点——挂载盘是目录列表的最高频调用方
	// （资源管理器一次浏览会连发多次 PROPFIND/Stat），它吃到增量，
	// 增量列举才不只是“接口可用”，而是“生产在用”。
	// 旧控制面 / 单测假客户端没有 Changes 能力时自动退回全量 Objects。
	if cc, ok := f.client.(changeClient); ok {
		f.mu.Lock()
		base := f.cache
		since := f.lastVersion
		f.mu.Unlock()

		var sincePtr *int64
		// 只要磁盘上有版本就可以尝试增量，即使 base 为空——
		// 变更流本身足够重建当前视图（created 会补出缺失项）。
		// base == nil 只说明进程刚重启、内存缓存丢了，不代表服务端没有差异。
		if since > 0 {
			sincePtr = &since
		}
		result, err := cc.Changes(ctx, token, f.space, sincePtr)
		if err != nil {
			// 增量失败不等于全量失败：可能只是旧控制面尚未提供 /changes。
			// 退回全量，行为与引入增量前一致；错误不掩盖网络层本身的故障。
			list, listErr := f.client.Objects(ctx, token, f.space)
			if listErr != nil {
				return nil, listErr
			}
			f.mu.Lock()
			f.cache, f.cachedAt = list, time.Now()
			f.mu.Unlock()
			return list, nil
		}
		if sincePtr == nil {
			f.mu.Lock()
			f.cache, f.cachedAt = result.Files, time.Now()
			f.lastVersion = result.NewestSpaceVersion.Int64()
			f.mu.Unlock()
			_ = f.versions.Save(f.space, f.lastVersion)
			return result.Files, nil
		}

		// 把差异应用到上次缓存。这里必须**复制后修改**，
		// 不能直接改 base：其他 goroutine 可能正持有旧 slice 在读。
		merged := make([]drive.Object, len(base))
		copy(merged, base)
		_ = merged // base 可能为空（进程重启）；applyChange 会从 created 事件补出对象
		for _, change := range result.Changes {
			merged = applyChange(merged, change)
		}
		f.mu.Lock()
		f.cache, f.cachedAt = merged, time.Now()
		f.lastVersion = result.NewestSpaceVersion.Int64()
		f.mu.Unlock()
		_ = f.versions.Save(f.space, f.lastVersion)
		return merged, nil
	}

	list, err := f.client.Objects(ctx, token, f.space)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.cache, f.cachedAt = list, time.Now()
	f.mu.Unlock()
	return list, nil
}

// applyChange 把一条生命周期事件应用到对象清单。
//
// 语义与客户端本地视图一致：created/updated/restored 保证路径存在；
// deleted/moved/purged 从旧路径移除。事件流按版本升序到达，
// 因此重复应用同一条流会得到相同结果。
func applyChange(objects []drive.Object, change drive.Change) []drive.Object {
	switch change.Event {
	case drive.ChangeCreated, drive.ChangeUpdated, drive.ChangeRestored:
		// 事件流只带身份不带大小/时间；这里不能用零值对象充当完整视图——
		// 否则 Stat 会返回 0 字节（真实缺陷：用户看到文件“变小”）。
		// 正确做法：先保留既有对象的元数据；没有元数据时**从全量视图查询**。
		for i := range objects {
			if objects[i].Path == change.Path {
				return objects
			}
		}
		return append(objects, drive.Object{
			Path:         change.Path,
			Size:         change.Size,
			LastModified: change.LastModified(),
			FileId:       change.FileId,
		})
	case drive.ChangeDeleted, drive.ChangePurged:
		next := objects[:0]
		for _, obj := range objects {
			if obj.Path != change.Path {
				next = append(next, obj)
			}
		}
		return next
	case drive.ChangeMoved:
		next := make([]drive.Object, 0, len(objects))
		for _, obj := range objects {
			if obj.Path == change.OldPath {
				continue
			}
			if obj.Path == change.Path {
				continue
			}
			next = append(next, obj)
		}
		// moved 只改路径，size/lastModified 保留
		for _, obj := range objects {
			if obj.Path == change.OldPath {
				moved := obj
				moved.Path = change.Path
				moved.FileId = change.FileId
				next = append(next, moved)
				break
			}
		}
		return next
	default:
		return objects
	}
}

// versionOf 返回上传前看到的版本号。
//
// 读缓存而不是再打一次控制面：OpenFile 发生在 PUT 流开始时，
// 那一刻的清单就是“上传方看到的世界”（CAS 语义）。写成功后 invalidate 会让它失效。
func (f *FS) versionOf(key string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, obj := range f.cache {
		if obj.Path == key {
			return obj.VersionId.Int64(), true
		}
	}
	return 0, false
}

// invalidate 作废清单缓存。任何写操作后调用。
func (f *FS) invalidate() {
	f.mu.Lock()
	f.cache, f.cachedAt = nil, time.Time{}
	f.mu.Unlock()
}

// clearPendingDir 清掉某个空目录记录（其下已经有真实对象了）。
//
// 连同祖先一起清：文件落到 a/b/c.txt 时，a 与 a/b 都已不再是空目录。
func (f *FS) clearPendingDir(dir string) {
	if dir == "" || dir == "." {
		return
	}
	f.mu.Lock()
	delete(f.pendingDirs, dir)
	store := f.dirStore
	f.mu.Unlock()
	if store != nil {
		_ = store.RemoveWithParents(f.space, dir)
	}
}

// uploadContext 给上传用的 context，调用方必须 defer cancel。
//
// 刻意不继承 WebDAV 请求的 context：资源管理器常在 Close 之前就断开连接，
// 继承会让一个已经收全的文件在最后一步被取消。
func (f *FS) uploadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), uploadTimeout)
}

// uploadTimeout 是单次上传的上限，够大文件走完，又不至于永久挂住。
const uploadTimeout = 30 * time.Minute

// --- webdav.FileSystem ---

// Mkdir 记住一个空目录。对象存储无真目录，故只在本地维护（有 DirStore 时落盘）。
func (f *FS) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	if f.readOnly {
		return errReadOnly
	}
	key := toKey(name)
	if key == "" {
		return os.ErrExist
	}
	created := time.Now()
	f.mu.Lock()
	f.pendingDirs[key] = created
	store := f.dirStore
	f.mu.Unlock()
	if store != nil {
		// 落盘失败不拦创建：本进程内仍然可见，只是重启后丢——
		// 这点取舍与 pendingDirs 的历史行为一致，不该因为写盘失败就报错
		_ = store.Add(f.space, key, created)
	}
	return nil
}

// OpenFile 打开文件读，或返回一个写缓冲。
func (f *FS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	key := toKey(name)
	writing := flag&(os.O_CREATE|os.O_WRONLY|os.O_RDWR|os.O_TRUNC|os.O_APPEND) != 0

	if writing {
		if f.readOnly {
			return nil, errReadOnly
		}
		if key == "" {
			return nil, os.ErrInvalid
		}
		// 先确认能登录，避免用户在资源管理器里写完一大个文件才发现没登录
		if _, err := f.tokenOrErr(); err != nil {
			return nil, err
		}
		// 写前刷新一次对象清单：这是 CAS 的“我看到的世界”。
		//
		// 不能依赖缓存“碰巧有”：资源管理器可能直接 PUT 而没有先 PROPFIND，
		// 缓存为空时会把已有文件当成“不存在”（expectedVersion=0），
		// 于是每次保存都落冲突副本。这里主动列一次，保证版本快照来自打开写句柄这一刻。
		if _, err := f.objects(ctx); err != nil {
			return nil, err
		}
		return newWriteFile(f, key)
	}

	if key == "" {
		return f.openDir(ctx, "", f.label)
	}
	// 目录优先判定：目录名可能与文件同名前缀，先看它是不是目录
	isDir, err := f.isDir(ctx, key)
	if err != nil {
		return nil, err
	}
	if isDir {
		return f.openDir(ctx, key, path.Base(key))
	}

	token, err := f.tokenOrErr()
	if err != nil {
		return nil, err
	}
	body, size, err := f.client.Open(ctx, token, f.space, key)
	if err != nil {
		return nil, translateNotFound(err)
	}
	return newReadFile(f, key, body, size), nil
}

// RemoveAll 删除文件，或递归删除某前缀下的全部对象。
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if f.readOnly {
		return errReadOnly
	}
	key := toKey(name)
	if key == "" {
		return os.ErrInvalid
	}
	token, err := f.tokenOrErr()
	if err != nil {
		return err
	}
	list, err := f.objects(ctx)
	if err != nil {
		return err
	}

	prefix := key + "/"
	var targets []string
	for _, obj := range list {
		if obj.Path == key || strings.HasPrefix(obj.Path, prefix) {
			targets = append(targets, obj.Path)
		}
	}

	f.mu.Lock()
	delete(f.pendingDirs, key)
	for dir := range f.pendingDirs {
		if strings.HasPrefix(dir, prefix) {
			delete(f.pendingDirs, dir)
		}
	}
	store := f.dirStore
	f.mu.Unlock()
	// 持久化表同步清理：删掉的目录不该在重启后复活
	if store != nil {
		_ = store.RemoveWithParents(f.space, key)
		// RemoveWithParents 只清祖先链；子目录要按前缀另清
		for dir := range store.Load(f.space) {
			if strings.HasPrefix(dir, prefix) {
				_ = store.Remove(f.space, dir)
			}
		}
	}

	if len(targets) == 0 {
		// 只是个空目录：内存记录已清，无对象可删
		return nil
	}
	// 删除优先走 fileId（切片 1：写路径按身份而非路径）。
	// 路径在「列出 → 删除」之间可能已被别人改名占用；按身份删不会误伤。
	// fileId 从本次列举结果里取；取不到（目录层未登记/脏数据）退回按路径删。
	fileIDs := make(map[string]int64, len(list))
	for _, obj := range list {
		fileIDs[obj.Path] = obj.FileId.Int64()
	}
	for _, target := range targets {
		if err := f.deleteObject(ctx, token, target, fileIDs[target]); err != nil {
			f.invalidate()
			return err
		}
	}
	f.invalidate()
	return nil
}

// deleteObject 删一个对象：有 fileId 且客户端支持时按身份删，否则按路径删。
func (f *FS) deleteObject(ctx context.Context, token, path string, fileID int64) error {
	if byID, ok := f.client.(fileIDDeleter); ok && fileID > 0 {
		return byID.DeleteByFileID(ctx, token, f.space, fileID, path)
	}
	return f.client.Delete(ctx, token, f.space, path)
}

// fileIDDeleter 是可选能力：能按稳定 fileId 删除的客户端（*drive.Client 满足）。
//
// 与 mover 同一模式：可选接口而非加进 Client，假实现与旧控制面不必跟着改。
type fileIDDeleter interface {
	DeleteByFileID(ctx context.Context, token, space string, fileID int64, path string) error
}

// mover 是可选能力：控制面支持服务端改名/移动的客户端。
//
// *drive.Client 满足（Move 是导出方法）。有它时 Rename 走服务端复制——字节不出对象存储。
// 没有它时退回「下载 + 上传 + 删除」（旧行为），保持假客户端与旧控制面可用。
type mover interface {
	Move(ctx context.Context, token, space, from, to string) error
}

// Rename 改名/移动。
//
// **优先走服务端 move**（{@link mover}）：控制面做 CopyObject + 删源，
// 字节不出对象存储，且 **fileId 不变**——分享链接、任务引用、知识索引都绑 fileId，
// 换了它就等于换了一个文件。
//
// 旧实现是客户端「下载 + 上传 + 删除」：一个 2 GB 文件改名要先拉回本地再传上去，
// 等几分钟、流量翻倍，而且**中途失败会留下两份**；更糟的是上传到新键会登记出
// **新的 fileId**，旧 fileId 的所有引用全部失效。仅在客户端不支持服务端 move
// （假实现/旧控制面）时才退回这条路。
//
// 刻意不做目录改名：那会放大成 N 组复制删除，中途失败会留下半改状态。
// 资源管理器会得到 EPERM，提示无法重命名，好过悄悄搞坏一半。
func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	if f.readOnly {
		return errReadOnly
	}
	oldKey, newKey := toKey(oldName), toKey(newName)
	if oldKey == "" || newKey == "" {
		return os.ErrInvalid
	}
	token, err := f.tokenOrErr()
	if err != nil {
		return err
	}
	isDir, err := f.isDir(ctx, oldKey)
	if err != nil {
		return err
	}
	if isDir {
		return fmt.Errorf("暂不支持重命名目录：%w", os.ErrPermission)
	}

	// 服务端 move：一次往返，字节不动，fileId 不变
	if m, ok := f.client.(mover); ok {
		err := m.Move(ctx, token, f.space, oldKey, newKey)
		if isTargetOccupied(err) {
			// 切片 3 的固定规则：目标已存在 → 冲突副本，不覆盖。
			// 重新列举以避开所有现有路径；旧缓存可能不含刚被别人创建的文件。
			list, listErr := f.objects(ctx)
			if listErr != nil {
				return fmt.Errorf("移动冲突前刷新列表失败：%w", listErr)
			}
			conflictKey := uniqueConflictKey(list, newKey)
			if err := m.Move(ctx, token, f.space, oldKey, conflictKey); err != nil {
				return err
			}
			f.invalidate()
			return nil
		}
		if err != nil {
			return err
		}
		f.invalidate()
		return nil
	}

	body, size, err := f.client.Open(ctx, token, f.space, oldKey)
	if err != nil {
		return translateNotFound(err)
	}
	defer body.Close()
	if err := f.client.Upload(ctx, token, f.space, newKey, body, size); err != nil {
		return err
	}
	// 先传成功再删源：反过来一旦上传失败，文件就没了
	if err := f.client.Delete(ctx, token, f.space, oldKey); err != nil {
		f.invalidate()
		return err
	}
	f.invalidate()
	return nil
}

// Stat 返回文件或目录信息。
func (f *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	key := toKey(name)
	if key == "" {
		return &fileInfo{name: f.label, isDir: true, modTime: time.Now()}, nil
	}
	list, err := f.objects(ctx)
	if err != nil {
		return nil, err
	}
	for _, obj := range list {
		if obj.Path == key {
			return &fileInfo{
				name:    path.Base(key),
				size:    obj.Size,
				modTime: obj.LastModified,
			}, nil
		}
	}
	isDir, err := f.isDirFrom(list, key)
	if err != nil {
		return nil, err
	}
	if isDir {
		return &fileInfo{name: path.Base(key), isDir: true, modTime: time.Now()}, nil
	}
	return nil, os.ErrNotExist
}

// isDir 判断某键是否为目录（其下有对象，或是本次会话新建的空目录）。
func (f *FS) isDir(ctx context.Context, key string) (bool, error) {
	list, err := f.objects(ctx)
	if err != nil {
		return false, err
	}
	return f.isDirFrom(list, key)
}

func (f *FS) isDirFrom(list []drive.Object, key string) (bool, error) {
	prefix := key + "/"
	for _, obj := range list {
		if strings.HasPrefix(obj.Path, prefix) {
			return true, nil
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.pendingDirs[key]; ok {
		return true, nil
	}
	for dir := range f.pendingDirs {
		if strings.HasPrefix(dir, prefix) {
			return true, nil
		}
	}
	return false, nil
}

// openDir 列出某前缀的直接子项。
func (f *FS) openDir(ctx context.Context, key, label string) (webdav.File, error) {
	list, err := f.objects(ctx)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if key != "" {
		prefix = key + "/"
	}

	files := map[string]*fileInfo{}
	dirs := map[string]bool{}
	for _, obj := range list {
		if !strings.HasPrefix(obj.Path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(obj.Path, prefix)
		if remainder == "" {
			continue
		}
		if idx := strings.Index(remainder, "/"); idx >= 0 {
			dirs[remainder[:idx]] = true
			continue
		}
		files[remainder] = &fileInfo{name: remainder, size: obj.Size, modTime: obj.LastModified}
	}

	// 叠上本次会话新建、还没有文件的空目录
	f.mu.Lock()
	for dir, created := range f.pendingDirs {
		if !strings.HasPrefix(dir, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(dir, prefix)
		if remainder == "" {
			continue
		}
		if idx := strings.Index(remainder, "/"); idx >= 0 {
			dirs[remainder[:idx]] = true
			continue
		}
		if !dirs[remainder] {
			dirs[remainder] = true
			files["\x00dir\x00"+remainder] = &fileInfo{name: remainder, isDir: true, modTime: created}
		}
	}
	f.mu.Unlock()

	entries := make([]os.FileInfo, 0, len(files)+len(dirs))
	for name := range dirs {
		entries = append(entries, &fileInfo{name: name, isDir: true, modTime: time.Now()})
	}
	for name, info := range files {
		if strings.HasPrefix(name, "\x00dir\x00") {
			continue
		}
		entries = append(entries, info)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	return &dirFile{info: &fileInfo{name: label, isDir: true, modTime: time.Now()}, entries: entries}, nil
}

// toKey 把 WebDAV 路径规范成控制面认的相对路径。
//
// 控制面只接受相对路径（不带前导 /、不带 .. 片段），空间前缀由它按登录态自己拼——
// 客户端无法指定前缀，因此跨不到别的空间。
func toKey(name string) string {
	// 必须先统一分隔符再去首尾斜杠：反过来的话 `\dir\b.txt` 会剩下前导 /，
	// 而控制面明确拒绝以 / 开头的相对路径，资源管理器传来的 Windows 风格路径就全废了。
	key := strings.ReplaceAll(name, "\\", "/")
	for strings.Contains(key, "//") {
		key = strings.ReplaceAll(key, "//", "/")
	}
	key = strings.Trim(key, "/")
	if key == "." {
		return ""
	}
	return key
}

// translateNotFound 把控制面的错误尽量映射为 os.ErrNotExist，让资源管理器显示
// 「找不到文件」而不是一个原始的服务端错误串。
func translateNotFound(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if strings.Contains(text, "404") || strings.Contains(text, "NoSuchKey") ||
		strings.Contains(text, "不存在") {
		return os.ErrNotExist
	}
	return err
}

// isTargetOccupied 判断 move 是否因目标已存在被拒。
func isTargetOccupied(err error) bool {
	return err != nil && strings.Contains(err.Error(), "目标位置已有同名文件")
}

// uniqueConflictKey 生成不与现有对象冲突的副本路径。
func uniqueConflictKey(objects []drive.Object, key string) string {
	ext := path.Ext(key)
	base := strings.TrimSuffix(key, ext)
	if !objectExists(objects, base+" (冲突副本)"+ext) {
		return base + " (冲突副本)" + ext
	}
	for i := 2; ; i++ {
		candidate := base + " (冲突副本" + strconv.Itoa(i) + ")" + ext
		if !objectExists(objects, candidate) {
			return candidate
		}
	}
}

func objectExists(objects []drive.Object, key string) bool {
	for _, obj := range objects {
		if obj.Path == key {
			return true
		}
	}
	return false
}
