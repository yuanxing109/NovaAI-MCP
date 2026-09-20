package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/novaai/novaai-mcp/internal/audit"
)

// 会话不携带权限。鉴权结果固定为全局 default profile，永不从 session 读取。
// 历史实现曾把 profile 挂在 session 上，仅作观测；现已删除，防止误读。

var (
	ErrTooManySessions = errors.New("会话数超过上限")
	ErrSessionNotFound = errors.New("会话不存在")
	ErrSessionExpired  = errors.New("会话已过期")
)

// 会话相关的固定行为。
//
// 它们不再是配置项：方案里配置只保留 stateDir/listen/unixSocket/profile/
// limits/audit/shellTimeoutSeconds/resultPreviewBytes。取值沿用重构前默认值。
const (
	idleTimeout   = 30 * time.Minute
	maxSessions   = 32
	sweepInterval = 5 * time.Minute
)

// State 是一个已登记的 MCP 会话。
//
// 字段必须全部可 JSON 序列化。不要在这里放 func / chan —— encoding/json
// 遇到它们会整体失败。
//
// 不含 Profile：会话不携带权限，见本文件顶部说明。
type State struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	Closed     bool      `json:"closed"`
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*State
	audit    *audit.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewManager(auditLogger *audit.Logger) *Manager {
	m := &Manager{
		sessions: make(map[string]*State),
		audit:    auditLogger,
		stopCh:   make(chan struct{}),
	}
	go m.sweepLoop()
	return m
}

// Create 登记一个新会话。
//
// 只应在处理 initialize 时调用。其余请求若没有携带有效的 Mcp-Session-Id，
// 一律走无状态路径（不登记），否则不实现会话的客户端每发一个请求就会
// 占用一个名额，很快撞上上限并被 -32014 拒绝。
func (m *Manager) Create() (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) >= maxSessions {
		return nil, ErrTooManySessions
	}

	id, _ := randomID()
	s := &State{
		ID:         id,
		CreatedAt:  time.Now(),
		LastSeenAt: time.Now(),
	}
	m.sessions[id] = s

	m.audit.Log(audit.Entry{
		Event:   "session_create",
		Session: id,
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

func (m *Manager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		s.Closed = true
	}
	m.mu.Unlock()
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

// Stop 停止后台清扫 goroutine。可重复调用。
func (m *Manager) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

func (m *Manager) sweepLoop() {
	ticker := time.NewTicker(sweepInterval)
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

	m.mu.Lock()
	expired := []string{}
	for id, s := range m.sessions {
		if now.Sub(s.LastSeenAt) > idleTimeout {
			expired = append(expired, id)
		}
	}
	m.mu.Unlock()

	for _, id := range expired {
		m.audit.Log(audit.Entry{Event: "session_idle_expire", Session: id})
		m.Close(id)
	}
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
