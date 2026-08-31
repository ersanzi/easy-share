// Package spacedav 把一个「空间」（个人或共享）暴露为 WebDAV 文件系统，供 Windows
// 资源管理器直接挂载。
//
// 与 internal/cloud/webdavfs 的区别，也是本包存在的理由：webdavfs 建在
// objectstore.Store 之上，用编译期静态凭据直连 RustFS（KI-2）；本包建在
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

	mu       sync.Mutex
	cache    []drive.Object
	cachedAt time.Time
	// pendingDirs 保存本次会话里新建的空目录。
	//
	// 对象存储没有真目录，控制面的接口又拒绝以 / 结尾的键，所以空目录无处存放。
	// 不记住它，用户在资源管理器里「新建文件夹」后刷新就会消失。这里只在内存里维持到
	// 目录内有了第一个文件为止。
	pendingDirs map[string]time.Time
}

// Options 构造 FS 所需的参数。
type Options struct {
	Client   Client
	Space    string
	Token    TokenFunc
	ReadOnly bool
	Label    string
}

// New 创建某个空间的 WebDAV 文件系统。
func New(opts Options) *FS {
	label := opts.Label
	if label == "" {
		label = "EasyShare"
	}
	return &FS{
		client:      opts.Client,
		space:       opts.Space,
		token:       opts.Token,
		readOnly:    opts.ReadOnly,
		label:       label,
		pendingDirs: map[string]time.Time{},
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

	list, err := f.client.Objects(ctx, token, f.space)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.cache, f.cachedAt = list, time.Now()
	f.mu.Unlock()
	return list, nil
}

// invalidate 作废清单缓存。任何写操作后调用。
func (f *FS) invalidate() {
	f.mu.Lock()
	f.cache, f.cachedAt = nil, time.Time{}
	f.mu.Unlock()
}

// clearPendingDir 清掉某个空目录记录（其下已经有真实对象了）。
func (f *FS) clearPendingDir(dir string) {
	if dir == "" || dir == "." {
		return
	}
	f.mu.Lock()
	delete(f.pendingDirs, dir)
	f.mu.Unlock()
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

// Mkdir 记住一个空目录。对象存储无真目录，故只在内存里维持。
func (f *FS) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	if f.readOnly {
		return errReadOnly
	}
	key := toKey(name)
	if key == "" {
		return os.ErrExist
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingDirs[key] = time.Now()
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
		return newWriteFile(f, key), nil
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
	return newReadFile(key, body, size), nil
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
	f.mu.Unlock()

	if len(targets) == 0 {
		// 只是个空目录：内存记录已清，无对象可删
		return nil
	}
	for _, target := range targets {
		if err := f.client.Delete(ctx, token, f.space, target); err != nil {
			f.invalidate()
			return err
		}
	}
	f.invalidate()
	return nil
}

// Rename 通过「下载 + 上传 + 删除」实现——控制面没有 rename 接口。
//
// 刻意不做目录改名：那会放大成 N 组下载上传删除，中途失败会留下半改状态。
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
