// 会话落盘与网关运行态。knowledge.json 与 config.json 同目录，仅 Core 读写：
// 桌面进程保存设置时会整份回写启动时的旧 config.json，令牌若存在其中会被冲掉，
// 因此知识会话独立成文件，彻底避开该竞态。
package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store 负责 knowledge.json 的原子读写（临时文件 + rename，权限 0600）。
type Store struct {
	path  string
	mutex sync.Mutex
}

// NewStore 创建会话存储；path 为 knowledge.json 完整路径。
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load 读取会话；文件不存在视为空会话（未配置），不报错。
func (store *Store) Load() (Session, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	data, err := os.ReadFile(store.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Session{}, nil
		}
		return Session{}, fmt.Errorf("read knowledge session: %w", err)
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return Session{}, fmt.Errorf("decode knowledge session: %w", err)
	}
	return session, nil
}

// Save 原子写入会话。
func (store *Store) Save(session Session) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode knowledge session: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create knowledge session directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, filepath.Base(store.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary knowledge session: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("protect temporary knowledge session: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("write temporary knowledge session: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("sync temporary knowledge session: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close temporary knowledge session: %w", err)
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("replace knowledge session: %w", err)
	}
	return nil
}

// Clear 删除会话文件；文件不存在视为已清空。
func (store *Store) Clear() error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if err := os.Remove(store.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove knowledge session: %w", err)
	}
	return nil
}

// Service 知识网关运行态：当前会话 + 落盘。并发安全。
type Service struct {
	store *Store
	mutex sync.RWMutex
	// session 当前会话；Token 仅经 Current() 在 Core 内部使用。
	session Session
}

// NewService 加载既有会话（缺文件即未配置态）。
func NewService(store *Store) *Service {
	session, err := store.Load()
	if err != nil {
		// 会话文件损坏不应阻断 Core 启动；按未登录处理，用户重新登录即可覆盖修复。
		session = Session{}
	}
	return &Service{store: store, session: session}
}

// Status 返回面向前端的登录态视图（不含令牌）。
func (service *Service) Status() StatusView {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	return StatusView{
		Configured: service.session.ServerURL != "",
		LoggedIn:   service.session.Token != "",
		ServerURL:  service.session.ServerURL,
		Username:   service.session.Username,
		Role:       service.session.Role,
	}
}

// Current 返回完整会话（含令牌）；仅 Core 网关内部调用。
func (service *Service) Current() Session {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	return service.session
}

// SignIn 更新会话并落盘。
func (service *Service) SignIn(session Session) error {
	if err := service.store.Save(session); err != nil {
		return err
	}
	service.mutex.Lock()
	service.session = session
	service.mutex.Unlock()
	return nil
}

// SignOut 清空会话并删除落盘文件。
func (service *Service) SignOut() error {
	if err := service.store.Clear(); err != nil {
		return err
	}
	service.mutex.Lock()
	service.session = Session{}
	service.mutex.Unlock()
	return nil
}
