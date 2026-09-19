package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
)

var (
	ErrTooManySessions = errors.New("会话数超过上限")
	ErrSessionNotFound = errors.New("会话不存在")
	ErrSessionExpired  = errors.New("会话已过期")
)

type State struct {
	ID         string
	Profile    string
	PeerUID    int
	CreatedAt  time.Time
	LastSeenAt time.Time
	Closed     bool
	CloseFn    func()

	UploadBytes   int64
	DownloadBytes int64
	Concurrency   int
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*State
	cfg      *config.Config
	audit    *audit.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewManager(cfg *config.Config, auditLogger *audit.Logger) *Manager {
	m := &Manager{
		sessions: make(map[string]*State),
		cfg:      cfg,
		audit:    auditLogger,
		stopCh:   make(chan struct{}),
	}
	go m.sweepLoop()
	return m
}

func (m *Manager) Create(profile string, peerUID int) (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) >= m.cfg.Session.MaxSessions {
		return nil, ErrTooManySessions
	}

	id, _ := randomID()
	s := &State{
		ID:         id,
		Profile:    profile,
		PeerUID:    peerUID,
		CreatedAt:  time.Now(),
		LastSeenAt: time.Now(),
	}
	m.sessions[id] = s

	m.audit.Log(audit.Entry{
		Event:   "session_create",
		Session: id,
		Profile: profile,
	})

	return s, nil
}

func (m *Manager) Get(id string) (*State, error) {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	if s.Closed {
		return nil, ErrSessionExpired
	}
	return s, nil
}

func (m *Manager) Touch(id string) {
	m.mu.Lock()
	if s, ok := m.sessions[id]; ok {
		s.LastSeenAt = time.Now()
	}
	m.mu.Unlock()
}

// SetProfile 把会话重新绑定到本次请求鉴权后解析出的 profile。
//
// 必须做这件事：Mcp-Session-Id 由客户端自行携带，同一个 id 可能被不同 token
// 复用。如果只在创建时绑定 profile，低权限 token 就能复用高权限 token 建立的
// 会话，形成权限混淆。profile 必须始终跟随"本次请求的 token"。
func (m *Manager) SetProfile(id, profile string) {
	if profile == "" {
		return
	}
	m.mu.Lock()
	if s, ok := m.sessions[id]; ok {
		s.Profile = profile
	}
	m.mu.Unlock()
}

func (m *Manager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		s.Closed = true
	}
	m.mu.Unlock()
	if ok && s.CloseFn != nil {
		s.CloseFn()
	}
	m.audit.Log(audit.Entry{Event: "session_close", Session: id})
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
}

func (m *Manager) List() []State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]State, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	return out
}

func (m *Manager) RevokeAll() int {
	m.mu.Lock()
	count := len(m.sessions)
	m.mu.Unlock()
	m.CloseAll()
	return count
}

// Stop 停止后台清扫 goroutine。可重复调用。
func (m *Manager) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

func (m *Manager) sweepLoop() {
	interval := time.Duration(m.cfg.Session.SweepIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.sweep()
		}
	}
}

func (m *Manager) sweep() {
	now := time.Now()
	idle := time.Duration(m.cfg.Session.IdleTimeoutSeconds) * time.Second

	m.mu.Lock()
	expired := []string{}
	for id, s := range m.sessions {
		if now.Sub(s.LastSeenAt) > idle {
			expired = append(expired, id)
		}
	}
	m.mu.Unlock()

	for _, id := range expired {
		m.audit.Log(audit.Entry{Event: "session_idle_expire", Session: id})
		m.Close(id)
	}
}

func (m *Manager) AddUpload(id string, n int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	if s.UploadBytes+n > m.cfg.RateLimit.PerSession.TotalUploadBytes {
		return errors.New("会话上传配额超限")
	}
	s.UploadBytes += n
	return nil
}

func (m *Manager) AddDownload(id string, n int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	if s.DownloadBytes+n > m.cfg.RateLimit.PerSession.TotalDownloadBytes {
		return errors.New("会话下载配额超限")
	}
	s.DownloadBytes += n
	return nil
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
