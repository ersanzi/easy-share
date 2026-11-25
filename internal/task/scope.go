package task

// VisibleTo 将设备级局域网任务与账号级任务分开；旧云任务缺少归属时拒绝展示。
func (value Task) VisibleTo(owner string) bool {
	if value.Hidden {
		return false
	}
	if value.OwnerID != "" {
		return owner != "" && value.OwnerID == owner
	}
	return value.Kind == KindLANSend || value.Kind == KindLANReceive || value.Kind == ""
}

func (s *Store) ListFor(owner string) []Task {
	result := make([]Task, 0)
	for _, value := range s.List() {
		if value.VisibleTo(owner) {
			result = append(result, value)
		}
	}
	return result
}

func (s *Store) DeleteFor(id, owner string) error {
	s.mutex.Lock()
	value, ok := s.tasks[id]
	if !ok || !value.VisibleTo(owner) {
		s.mutex.Unlock()
		return ErrTaskNotFound
	}
	if value.Kind == KindCloudDownload && value.LocalPath != "" {
		value.Hidden = true
		s.tasks[id] = value
	} else {
		delete(s.tasks, id)
	}
	s.mutex.Unlock()
	s.persist()
	return nil
}

func (s *Store) ClearHistoryFor(owner string) {
	s.mutex.Lock()
	for id, value := range s.tasks {
		if terminal(value.Status) && value.VisibleTo(owner) {
			if value.Kind == KindCloudDownload && value.LocalPath != "" {
				value.Hidden = true
				s.tasks[id] = value
			} else {
				delete(s.tasks, id)
			}
		}
	}
	s.mutex.Unlock()
	s.persist()
}
