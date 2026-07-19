package task

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// EnablePersistence configures the store to load history from and save terminal
// tasks to the given file path. Only completed, rejected, and failed tasks are
// persisted; in-flight tasks are ephemeral by nature.
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
		if value.ID != "" && terminal(value.Status) {
			if _, exists := s.tasks[value.ID]; !exists {
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
	var history []Task
	for _, value := range s.tasks {
		if terminal(value.Status) {
			history = append(history, value)
		}
	}
	s.mutex.RUnlock()

	if history == nil {
		history = []Task{}
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')

	directory := filepath.Dir(path)
	_ = os.MkdirAll(directory, 0o700)
	tmp, err := os.CreateTemp(directory, "history-*.tmp")
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
