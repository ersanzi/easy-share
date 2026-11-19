package cfmount

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// DeleteRecord保留明确删除意图与原文件身份；正文继续由原写回快照及服务器回收站保管。
type DeleteRecord struct {
	OperationID      string            `json:"operationId"`
	Path             string            `json:"path"`
	RemoteID         int64             `json:"remoteId"`
	Version          int64             `json:"version"`
	Directory        bool              `json:"directory"`
	LocalIdentity    LocalFileIdentity `json:"localIdentity"`
	RemoteCommitted  bool              `json:"remoteCommitted"`
	BatchID          string            `json:"batchId,omitempty"`
	Status           string            `json:"status"`
	LocalRemoved     bool              `json:"localRemoved,omitempty"`
	LocalFailure     bool              `json:"localFailure,omitempty"`
	ClientProjection bool              `json:"clientProjection,omitempty"`
	Members          []DeleteMember    `json:"members,omitempty"`
}

type DeleteMember struct {
	Path                    string `json:"path"`
	RemoteID, Version, Size int64
	Directory               bool
	LocalIdentity           LocalFileIdentity
}

type deleteState struct {
	Format          int `json:"format"`
	Namespace, Root string
	Records         map[string]DeleteRecord `json:"records"`
}

type DeleteStateStore struct {
	mu    sync.Mutex
	path  string
	state deleteState
}

var deleteOperationToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func NewDeleteStateStore(root, stateDir, namespace string) (*DeleteStateStore, error) {
	if root == "" || stateDir == "" || namespace == "" {
		return nil, errors.New("删除状态缺少根、状态目录或账号身份")
	}
	r, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	d, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, err
	}
	if pathWithin(r, d) {
		return nil, errors.New("删除状态必须位于云根之外")
	}
	if err := os.MkdirAll(d, 0700); err != nil {
		return nil, err
	}
	s := &DeleteStateStore{path: filepath.Join(d, "delete-operations.json"), state: deleteState{Format: 1, Namespace: namespace, Root: r, Records: map[string]DeleteRecord{}}}
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.state); err != nil {
		return nil, fmt.Errorf("删除状态损坏，拒绝清空重建:%w", err)
	}
	if s.state.Format != 1 || s.state.Namespace != namespace || !strings.EqualFold(s.state.Root, r) || s.state.Records == nil {
		return nil, errors.New("删除状态账号或根身份不一致")
	}
	for p, record := range s.state.Records {
		if p != record.Path {
			return nil, errors.New("删除记录键与目标不一致")
		}
		if err := validDeleteRecord(record); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func validDeleteRecord(r DeleteRecord) error {
	if !validWritebackPath(r.Path) || !deleteOperationToken.MatchString(r.OperationID) || r.Version < 0 || r.RemoteID < 0 || r.RemoteCommitted && r.RemoteID <= 0 {
		return errors.New("删除记录缺少准确路径、操作或文件身份")
	}
	switch r.Status {
	case "preparing", "recycled", "restoring", "restored", "cancelled":
	default:
		return errors.New("未知删除操作状态")
	}
	if r.BatchID != "" && !deleteOperationToken.MatchString(r.BatchID) {
		return errors.New("删除批次无效")
	}
	for _, m := range r.Members {
		if !validWritebackPath(m.Path) || m.RemoteID <= 0 || m.Version < 0 || m.Size < 0 || m.Path != r.Path && !strings.HasPrefix(m.Path, r.Path+"/") {
			return errors.New("删除批次子项身份或范围无效")
		}
	}
	return nil
}

func cloneDeleteRecord(r DeleteRecord) DeleteRecord {
	r.Members = append([]DeleteMember(nil), r.Members...)
	return r
}
func (s *DeleteStateStore) Get(path string) (DeleteRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.state.Records[path]
	return cloneDeleteRecord(r), ok
}
func (s *DeleteStateStore) Records() []DeleteRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]DeleteRecord, 0, len(s.state.Records))
	for _, r := range s.state.Records {
		result = append(result, cloneDeleteRecord(r))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
func (s *DeleteStateStore) ActivePaths() []string {
	var paths []string
	for _, r := range s.Records() {
		if r.Status != "restored" && r.Status != "cancelled" {
			paths = append(paths, r.Path)
		}
	}
	return paths
}

func (s *DeleteStateStore) Put(record DeleteRecord) error {
	if err := validDeleteRecord(record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.state.Records[record.Path]
	s.state.Records[record.Path] = cloneDeleteRecord(record)
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err == nil {
		err = writeDeleteState(s.path, b)
	}
	if err != nil {
		if exists {
			s.state.Records[record.Path] = previous
		} else {
			delete(s.state.Records, record.Path)
		}
	}
	return err
}

func writeDeleteState(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "delete-state-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceWritebackState(name, path)
}
