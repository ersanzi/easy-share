package clipboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrUnsupportedPlatform 平台尚无剪切板监听实现（macOS 轮询在批次 3）。
var ErrUnsupportedPlatform = fmt.Errorf("当前平台暂不支持剪切板监听")

// Service 是剪切板历史的宿主侧服务：监听（平台实现）→ 记录（Store）→
// 事件回调（onChange 由 app 注入，转 Wails 事件推给前端插件）。
type Service struct {
	root string

	store    *Store
	settings Settings

	mu        sync.Mutex
	lastHash  string    // 最近一次记录的内容 hash（去重）
	selfWrite selfWrite // 自身回写标记（避免把自己的写入再记录一遍）

	// 生命周期：剪切板插件可卸载/禁用，录制随插件在场状态反复启停。
	lifeMu  sync.Mutex
	running bool
	stopCh  chan struct{}

	onChangeMu sync.RWMutex
	onChange   func(Entry)     // 兼容保留的单槽回调（app 注入，转 Wails 事件）
	onChangeFn []func(Entry)   // 多订阅回调（面板等附加观察者，AddOnChange 注册）
}

// WriteRequest 是回写剪贴板的请求（clipboard.write 能力入参）。
type WriteRequest struct {
	Kind      string   // text / image / files
	Text      string   // kind=text
	ImageFile string   // kind=image：clipboard/files/ 下的文件名
	Files     []string // kind=files
}

// selfWrite 记录最近一次宿主主动写入剪贴板的内容与时间（1 秒内同 hash 的更新事件忽略）。
type selfWrite struct {
	hash string
	at   time.Time
}

// NewService 创建服务并加载历史与设置。
func NewService(root string) (*Service, error) {
	store, err := LoadStore(root)
	if err != nil {
		return nil, err
	}
	s := &Service{root: root, store: store, settings: defaultSettings(), stopCh: make(chan struct{})}
	s.loadSettings()
	return s, nil
}

// SetOnChange 注册变更回调（新记录产生时调用；在监听线程上触发，实现方应尽快返回）。
func (s *Service) SetOnChange(fn func(Entry)) {
	s.onChangeMu.Lock()
	s.onChange = fn
	s.onChangeMu.Unlock()
}

// AddOnChange 追加一个变更订阅者（面板窗口等附加观察者；与 SetOnChange 互不影响）。
func (s *Service) AddOnChange(fn func(Entry)) {
	s.onChangeMu.Lock()
	s.onChangeFn = append(s.onChangeFn, fn)
	s.onChangeMu.Unlock()
}

func (s *Service) notifyChange(e Entry) {
	s.onChangeMu.RLock()
	fn := s.onChange
	fns := append([]func(Entry){}, s.onChangeFn...)
	s.onChangeMu.RUnlock()
	if fn != nil {
		fn(e)
	}
	for _, f := range fns {
		f(e)
	}
}

// Start 启动录制（平台监听实现在 listener_windows.go / listener_darwin.go /
// listener_other.go）。可重入：剪切板插件可卸载/禁用，安装/卸载/启停会反复调用。
func (s *Service) Start() error {
	s.lifeMu.Lock()
	if s.running {
		s.lifeMu.Unlock()
		return nil
	}
	// 上次 Stop 留下的已关闭 stopCh 复位，否则轮询协程会立即退出。
	select {
	case <-s.stopCh:
		s.stopCh = make(chan struct{})
	default:
	}
	s.lifeMu.Unlock()

	if err := s.startListener(); err != nil {
		return err
	}
	s.lifeMu.Lock()
	s.running = true
	s.lifeMu.Unlock()
	return nil
}

// Stop 停止录制；未在运行时为 no-op。
func (s *Service) Stop() {
	s.lifeMu.Lock()
	if !s.running {
		s.lifeMu.Unlock()
		return
	}
	s.running = false
	s.lifeMu.Unlock()
	s.stopListener()
}

// Settings 返回当前设置。
func (s *Service) Settings() Settings { return s.settings }

// SetPaused 切换暂停状态并持久化。
func (s *Service) SetPaused(paused bool) error {
	s.mu.Lock()
	s.settings.Paused = paused
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	return err
}

// SaveSettings 更新并持久化设置。
func (s *Service) SaveSettings(next Settings) error {
	s.mu.Lock()
	if next.MaxEntries <= 0 {
		next.MaxEntries = defaultMaxEntries
	}
	if next.MaxFilesBytes <= 0 {
		next.MaxFilesBytes = defaultMaxFilesBytes
	}
	s.settings = next
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	return err
}

func (s *Service) loadSettings() {
	data, err := os.ReadFile(filepath.Join(s.root, "clipboard", "settings.json"))
	if err != nil {
		return
	}
	var st Settings
	if json.Unmarshal(data, &st) == nil {
		if st.MaxEntries > 0 {
			s.settings.MaxEntries = st.MaxEntries
		}
		if st.MaxFilesBytes > 0 {
			s.settings.MaxFilesBytes = st.MaxFilesBytes
		}
		s.settings.Paused = st.Paused
	}
}

func (s *Service) saveSettingsLocked() error {
	dir := filepath.Join(s.root, "clipboard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建剪切板目录: %w", err)
	}
	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "settings.json"))
}

// filesDir 图片文件目录。
func (s *Service) filesDir() string { return filepath.Join(s.root, "clipboard", "files") }

// ImagePath 图片绝对路径（file 为 Store 里的文件名）。
func (s *Service) ImagePath(file string) string {
	name := filepath.Base(file) // 防穿越：只取文件名
	return filepath.Join(s.filesDir(), name)
}

// FilesHandler 返回剪切板图片的静态服务（挂 /clipboard-files/ 前缀），
// 供插件 iframe <img> 直接加载，免去 base64 传输。
func (s *Service) FilesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/clipboard-files/")
		// 只放行 hex.png 命名的图片文件。
		base := strings.TrimSuffix(name, ".png")
		if name == base || len(base) != 16 || !isHex(base) {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(s.ImagePath(name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// List 查询历史（倒序分页）。
func (s *Service) List(limit, offset int, kind, query string) []Entry {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.store.Query(limit, offset, kind, query)
}

// urlPattern 判定文本条目是否为纯链接（Stats 的链接分类，与插件端规则一致）。
var urlPattern = regexp.MustCompile(`^https?://\S+$`)

// Stats 返回各分类总数（侧栏统计用）。链接是从文本里再分出的类，text 计数含 url。
func (s *Service) Stats() map[string]int {
	entries := s.store.Entries()
	out := map[string]int{"total": len(entries)}
	for _, e := range entries {
		out[e.Kind]++
		if e.Kind == KindText && urlPattern.MatchString(strings.TrimSpace(e.Text)) {
			out["url"]++
		}
	}
	return out
}

// Delete 删除单条（连同图片文件）。
func (s *Service) Delete(id string) error {
	e, err := s.store.Delete(id)
	if err != nil {
		return err
	}
	if e.Kind == KindImage && e.File != "" {
		_ = os.Remove(s.ImagePath(e.File))
	}
	return nil
}

// Clear 清空历史（连同全部图片文件）。
func (s *Service) Clear() error {
	removed, err := s.store.Clear()
	for _, e := range removed {
		if e.Kind == KindImage && e.File != "" {
			_ = os.Remove(s.ImagePath(e.File))
		}
	}
	return err
}

// record 由平台监听实现调用：去重、落盘、LRU 截断、通知。
func (s *Service) record(e Entry) {
	s.mu.Lock()
	if s.settings.Paused {
		s.mu.Unlock()
		return
	}
	if e.Hash == s.lastHash {
		s.mu.Unlock()
		return
	}
	// 1 秒内与自身回写同 hash 的更新忽略（回写触发的事件不再入库）。
	if e.Hash == s.selfWrite.hash && time.Since(s.selfWrite.at) < time.Second {
		s.mu.Unlock()
		return
	}
	maxFiles := s.settings.MaxFilesBytes
	s.lastHash = e.Hash
	s.mu.Unlock()

	// 图片先落盘。
	if e.Kind == KindImage && len(e.imagePNG) > 0 {
		if err := os.MkdirAll(s.filesDir(), 0o700); err == nil {
			name := e.Hash + ".png"
			if err := os.WriteFile(filepath.Join(s.filesDir(), name), e.imagePNG, 0o600); err == nil {
				e.File = name
			}
		}
	}
	e.imagePNG = nil // 不入库

	if err := s.store.Append(e, s.settings.MaxEntries); err != nil {
		return
	}
	s.trimFilesLRU(maxFiles)
	s.notifyChange(e)
}

// trimFilesLRU 图片目录超限时按最老优先删除。
func (s *Service) trimFilesLRU(maxBytes int64) {
	if maxBytes <= 0 {
		return
	}
	entries := s.store.Entries() // 倒序（新→老）
	var total int64
	kept := map[string]bool{}
	for _, e := range entries {
		if e.Kind != KindImage || e.File == "" || kept[e.File] {
			continue
		}
		path := s.ImagePath(e.File)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		kept[e.File] = true
		total += info.Size()
	}
	if total <= maxBytes {
		return
	}
	// 从最老开始删，直到回到限额内。
	for i := len(entries) - 1; i >= 0 && total > maxBytes; i-- {
		e := entries[i]
		if e.Kind != KindImage || e.File == "" {
			continue
		}
		path := s.ImagePath(e.File)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err == nil {
			total -= info.Size()
		}
	}
}

// markSelfWrite 记录宿主主动写入（平台 Write 实现调用）。
func (s *Service) markSelfWrite(hash string) {
	s.mu.Lock()
	s.selfWrite = selfWrite{hash: hash, at: time.Now()}
	s.mu.Unlock()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "00000000"[:n*2]
	}
	return hex.EncodeToString(b)
}
