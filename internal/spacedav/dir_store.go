package spacedav

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 空目录持久化（路线图切片 4 第 3 项的第一半）。
//
// 为什么需要它：对象存储没有真目录，控制面接口又不接受以 / 结尾的键，
// 所以「资源管理器里新建的空文件夹」在服务端**没有落点**。
// 原先它只活在进程内存（pendingDirs）——桌面端一重启就消失，
// 用户看到的症状是「我建的文件夹自己没了」。
//
// 本文件把它落到数据根下的一张小 JSON 表里，重启后仍在。
//
// 边界（诚实交代）：
//   - 这是**客户端侧持久化**，不是服务端目录对象模型。同一账号在另一台机器上
//     看不到你建的空目录——真正的目录对象（钉盘的 local_id/parent_id 树）
//     是控制面的活，归后续切片。
//   - 空目录里一旦有了真实文件，就从这张表里摘掉（它已经由对象路径表达）。
//   - 同名目录在另一端被建出来也不会冲突：表里只是「这条路径也算目录」的提示。

// dirStore 持久化「本机建过、当前仍为空」的目录。
//
// 表结构按空间分桶：个人盘与共享盘的空目录互不可见。
type dirStore struct {
	path string
	mu   sync.Mutex
	// dirs space → (dir key → 创建时间)
	dirs map[string]map[string]time.Time
}

type dirStoreFile struct {
	// Spaces space → {dir: createdAtUnix}
	Spaces map[string]map[string]int64 `json:"spaces"`
}

// NewDirStore 打开（或新建）空目录持久化表。path 为数据根下的 JSON 文件。
func NewDirStore(path string) *dirStore {
	store := &dirStore{path: path, dirs: map[string]map[string]time.Time{}}
	store.load()
	return store
}

func (d *dirStore) load() {
	raw, err := os.ReadFile(d.path)
	if err != nil {
		return
	}
	var file dirStoreFile
	if json.Unmarshal(raw, &file) != nil {
		return
	}
	for space, entries := range file.Spaces {
		bucket := make(map[string]time.Time, len(entries))
		for dir, created := range entries {
			bucket[dir] = time.Unix(created, 0)
		}
		d.dirs[space] = bucket
	}
}

// save 落盘（临时文件 + rename，与 SessionStore/diskVersionStore 同一惯例）。
func (d *dirStore) save() error {
	file := dirStoreFile{Spaces: map[string]map[string]int64{}}
	for space, bucket := range d.dirs {
		entries := make(map[string]int64, len(bucket))
		for dir, created := range bucket {
			entries[dir] = created.Unix()
		}
		file.Spaces[space] = entries
	}
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

// Load 返回某空间全部持久化的空目录（调用方只读）。
func (d *dirStore) Load(space string) map[string]time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	src := d.dirs[space]
	out := make(map[string]time.Time, len(src))
	for dir, created := range src {
		out[dir] = created
	}
	return out
}

// Add 记录一个空目录并落盘。
func (d *dirStore) Add(space, dir string, created time.Time) error {
	if dir == "" {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	bucket := d.dirs[space]
	if bucket == nil {
		bucket = map[string]time.Time{}
		d.dirs[space] = bucket
	}
	bucket[dir] = created
	return d.save()
}

// Remove 摘掉一个目录记录（其下已有真实对象）并落盘。
func (d *dirStore) Remove(space, dir string) error {
	if dir == "" {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	bucket := d.dirs[space]
	if bucket == nil {
		return nil
	}
	if _, ok := bucket[dir]; !ok {
		return nil
	}
	delete(bucket, dir)
	return d.save()
}

// RemoveWithParents 摘掉某路径及其全部祖先目录记录（文件落在深层目录时一次清理）。
//
// 例：文件传到 a/b/c.txt 时，a、a/b 若在表里都应摘掉——它们不再为空。
func (d *dirStore) RemoveWithParents(space, dir string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	bucket := d.dirs[space]
	if bucket == nil || dir == "" || dir == "." {
		return nil
	}
	changed := false
	for {
		if _, ok := bucket[dir]; ok {
			delete(bucket, dir)
			changed = true
		}
		parent := parentDirOf(dir)
		if parent == dir || parent == "" {
			break
		}
		dir = parent
	}
	if !changed {
		return nil
	}
	return d.save()
}

// parentDirOf 返回父目录路径；顶层目录返回空串。
func parentDirOf(dir string) string {
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' {
			return dir[:i]
		}
	}
	return ""
}
