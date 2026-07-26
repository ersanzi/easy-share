package task

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// EnablePersistence configures the store to load history from and save all
// tasks to the given file path. All tasks (including non-terminal) are
// persisted so that queued/running/paused tasks survive restarts.
func (s *Store) EnablePersistence(path string) {
	s.mutex.Lock()
	s.persistPath = path
	s.mutex.Unlock()
	s.loadHistory()
}

// ClearHistory removes all terminal tasks from the store and persists the result.
func (s *Store) ClearHistory() {
	s.mutex.Lock()
	for id, value := range s.tasks {
		if terminal(value.Status) {
			delete(s.tasks, id)
		}
	}
	s.mutex.Unlock()
	s.persist()
}

func (s *Store) loadHistory() {
	s.mutex.RLock()
	path := s.persistPath
	s.mutex.RUnlock()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var history []Task
	if json.Unmarshal(data, &history) != nil {
		return
	}
	s.mutex.Lock()
	for _, value := range history {
		if value.ID != "" {
			if _, exists := s.tasks[value.ID]; !exists {
				// 非终态任务重启后标记为 waiting_network（网络状态未知）
				if !terminal(value.Status) && value.Status != StatusPending {
					value.Status = StatusWaitingNetwork
				}
				s.tasks[value.ID] = value
			}
		}
	}
	s.mutex.Unlock()
}

func (s *Store) persist() {
	s.mutex.RLock()
	path := s.persistPath
	if path == "" {
		s.mutex.RUnlock()
		return
	}
	all := make([]Task, 0, len(s.tasks))
	for _, value := range s.tasks {
		all = append(all, value)
	}
	s.mutex.RUnlock()

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')

	directory := filepath.Dir(path)
	_ = os.MkdirAll(directory, 0o700)
	tmp, err := os.CreateTemp(directory, "activities-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	_ = os.Rename(tmpPath, path)
}
