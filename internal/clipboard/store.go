// Package clipboard 实现剪切板历史记录：监听系统剪贴板变化（Windows 用
// AddClipboardFormatListener + message-only 窗口），记录文本/图片/文件列表到
// 本地 JSONL（环形截断），图片落盘为 PNG 文件，并向宿主推送变更事件。
// 该能力由内置剪切板插件消费，数据归宿主所有（不随插件卸载而消失）。
package clipboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 条目类型。
const (
	KindText  = "text"
	KindImage = "image"
	KindFiles = "files"
)

// 记录上限（可通过设置调整 MaxEntries / MaxFilesBytes）。
const (
	defaultMaxEntries    = 1000
	defaultMaxFilesBytes = 200 << 20 // 图片目录默认 200MB
	maxTextBytes         = 1 << 20   // 超过 1MB 的文本不记录（程序性大数据）
	maxFilePaths         = 100       // 文件列表最多记录 100 条路径
)

// Entry 是一条剪切板历史记录。
type Entry struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Text      string   `json:"text,omitempty"`   // kind=text
	File      string   `json:"file,omitempty"`   // kind=image 时图片文件名（files/ 目录下）
	Width     int      `json:"width,omitempty"`  // 图片像素宽
	Height    int      `json:"height,omitempty"` // 图片像素高
	Files     []string `json:"files,omitempty"`  // kind=files 的路径列表
	Size      int64    `json:"size"`             // 数据大小（字节或路径数）
	Source    string   `json:"source"`           // 来源进程名（exe 文件名）
	CreatedAt int64    `json:"createdAt"`        // Unix 毫秒
	Hash      string   `json:"hash"`             // 内容 SHA256 前 16 hex，去重用

	// imagePNG 是图片条目的原始 PNG 字节，仅在 record 落盘过程中传递，不序列化。
	imagePNG []byte `json:"-"`
}

// Settings 是剪切板记录的可调项（{root}/clipboard/settings.json）。
type Settings struct {
	MaxEntries    int   `json:"maxEntries"`    // 历史条数上限
	MaxFilesBytes int64 `json:"maxFilesBytes"` // 图片目录体积上限
	Paused        bool  `json:"paused"`        // 暂停记录（内置插件不可禁用，但用户可暂停）
}

func defaultSettings() Settings {
	return Settings{MaxEntries: defaultMaxEntries, MaxFilesBytes: defaultMaxFilesBytes}
}

// Store 管理历史记录的内存索引与 JSONL 持久化（{root}/clipboard/history.jsonl）。
// entries 按时间倒序（最新在前）；追加写文件，截断时整文件重写。
type Store struct {
	mu      sync.Mutex
	path    string
	entries []Entry
}

// LoadStore 读取历史文件（损坏行跳过，文件缺失则空库启动）。
func LoadStore(root string) (*Store, error) {
	s := &Store{path: filepath.Join(root, "clipboard", "history.jsonl")}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, fmt.Errorf("创建剪切板目录: %w", err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("读剪切板历史: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // 损坏行跳过，不让单行脏数据拖死整个历史
		}
		s.entries = append(s.entries, e)
	}
	// 文件是追加序（旧在前），内存索引倒序（新在前）。
	for i, j := 0, len(s.entries)-1; i < j; i, j = i+1, j-1 {
		s.entries[i], s.entries[j] = s.entries[j], s.entries[i]
	}
	return s, nil
}

// Append 追加一条记录（新在前），并按 maxEntries 截断后持久化。
func (s *Store) Append(e Entry, maxEntries int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append([]Entry{e}, s.entries...)
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	if len(s.entries) > maxEntries {
		s.entries = s.entries[:maxEntries]
	}
	return s.rewriteLocked()
}

// rewriteLocked 全量重写 JSONL（追加与截断共用；调用方须持锁）。
func (s *Store) rewriteLocked() error {
	var b strings.Builder
	for i := len(s.entries) - 1; i >= 0; i-- { // 文件里旧的在前
		data, err := json.Marshal(s.entries[i])
		if err != nil {
			continue
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("写剪切板历史: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换剪切板历史: %w", err)
	}
	return nil
}

// Query 按条件查询（倒序分页）。kind 空表示全部；query 为空不过滤，否则对文本/路径做包含匹配。
func (s *Store) Query(limit, offset int, kind, query string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, limit)
	q := strings.ToLower(query)
	for _, e := range s.entries {
		if kind != "" && e.Kind != kind {
			continue
		}
		if q != "" && !entryMatches(e, q) {
			continue
		}
		if offset > 0 {
			offset--
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func entryMatches(e Entry, lowerQuery string) bool {
	if strings.Contains(strings.ToLower(e.Text), lowerQuery) {
		return true
	}
	for _, p := range e.Files {
		if strings.Contains(strings.ToLower(p), lowerQuery) {
			return true
		}
	}
	return false
}

// Get 按 ID 取单条。
func (s *Store) Get(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Delete 删除单条，返回被删条目（调用方据此清理图片文件）。
func (s *Store) Delete(id string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.entries {
		if e.ID == id {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			if err := s.rewriteLocked(); err != nil {
				return e, err
			}
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("记录 %s 不存在", id)
}

// Clear 清空历史（返回被清掉的条目，供清理图片文件）。
func (s *Store) Clear() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := s.entries
	s.entries = nil
	if err := s.rewriteLocked(); err != nil {
		return removed, err
	}
	return removed, nil
}

// Entries 返回全部条目快照（LRU 清理图片时用）。
func (s *Store) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.entries...)
}

// NewID 生成条目 ID：毫秒时间戳-4字节随机。
func NewID() string {
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" + randomHex(4)
}
