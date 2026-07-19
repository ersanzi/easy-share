package task

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Store struct {
	mutex sync.RWMutex
	tasks map[string]Task
}

func NewStore() *Store {
	return &Store{tasks: make(map[string]Task)}
}

func (s *Store) Create(value Task) (Task, error) {
	if !validStatus(value.Status) || !validDirection(value.Direction) {
		return Task{}, ErrInvalidTask
	}
	if !validProgress(value) {
		return Task{}, ErrInvalidProgress
	}
	if value.ID == "" {
		id, err := newID()
		if err != nil {
			return Task{}, err
		}
		value.ID = id
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = now
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, exists := s.tasks[value.ID]; exists {
		return Task{}, fmt.Errorf("task %q already exists", value.ID)
	}
	s.tasks[value.ID] = value
	return value, nil
}

func (s *Store) Get(id string) (Task, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	value, ok := s.tasks[id]
	return value, ok
}

func (s *Store) List() []Task {
	s.mutex.RLock()
	values := make([]Task, 0, len(s.tasks))
	for _, value := range s.tasks {
		values = append(values, value)
	}
	s.mutex.RUnlock()
	sort.Slice(values, func(left, right int) bool {
		if values[left].CreatedAt.Equal(values[right].CreatedAt) {
			return values[left].ID < values[right].ID
		}
		return values[left].CreatedAt.Before(values[right].CreatedAt)
	})
	return values
}

func (s *Store) Update(id string, update func(*Task) error) (Task, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	current, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrTaskNotFound
	}
	next := current
	if err := update(&next); err != nil {
		return Task{}, err
	}
	if next.ID != current.ID {
		return Task{}, ErrImmutableID
	}
	if next.CreatedAt != current.CreatedAt || next.TotalBytes != current.TotalBytes || next.Direction != current.Direction || next.FileName != current.FileName || next.LocalPath != current.LocalPath || next.Peer != current.Peer {
		return Task{}, ErrImmutableField
	}
	if !validTransition(current.Status, next.Status) {
		return Task{}, ErrInvalidTransition
	}
	if !validProgress(next) {
		return Task{}, ErrInvalidProgress
	}
	if next.TransferredBytes < current.TransferredBytes {
		return Task{}, ErrInvalidProgress
	}
	next.UpdatedAt = time.Now().UTC()
	s.tasks[id] = next
	return next, nil
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate task ID: %w", err)
	}
	return hex.EncodeToString(value), nil
}
