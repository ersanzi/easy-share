package spacedav

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// diskVersionStore 把各空间的 last_version 持久化到一个 JSON 文件。
//
// 为什么不是每个空间一个文件：这里只有 personal/shared 两个键，
// 一个文件既原子也好管理。写法沿用 SessionStore：临时文件 + rename，
// 避免进程中断留下半份 JSON。
type diskVersionStore struct {
	path string
}

type diskVersionState struct {
	Versions map[string]int64 `json:"versions"`
}

// NewDiskVersionStore 创建磁盘版本存储。
func NewDiskVersionStore(path string) VersionStore {
	return &diskVersionStore{path: path}
}

func (d *diskVersionStore) Load(space string) int64 {
	raw, err := os.ReadFile(d.path)
	if err != nil {
		return 0
	}
	var state diskVersionState
	if json.Unmarshal(raw, &state) != nil || state.Versions == nil {
		return 0
	}
	return state.Versions[space]
}

func (d *diskVersionStore) Save(space string, version int64) error {
	_ = os.MkdirAll(filepath.Dir(d.path), 0o700)
	state := diskVersionState{Versions: map[string]int64{}}
	if raw, err := os.ReadFile(d.path); err == nil {
		_ = json.Unmarshal(raw, &state)
		if state.Versions == nil {
			state.Versions = map[string]int64{}
		}
	}
	state.Versions[space] = version
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}
