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

// State 是一个已登记的 MCP 会话。
//
// 字段必须全部可 JSON 序列化：session_list 直接把它编码给客户端。
// 不要在这里放 func / chan —— encoding/json 遇到它们会整体失败，
// 而调用方是"先写 200 再编码"，失败时客户端只会收到一个空响应体。
//
// Profile 是"最近一次请求解析出的 profile"的**观测记录**，供 session_list
// 展示；它不是鉴权依据。同一个 Mcp-Session-Id 可能被不同 token 复用，
// 因此权限始终取本次请求的 token 解析结果（mcp.Identity.Profile）。
type State struct {
	ID         string    `json:"id"`
	Profile    string    `json:"profile"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	Closed     bool      `json:"closed"`
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

// Create 登记一个新会话。
//
// 只应在处理 initialize 时调用。其余请求若没有携带有效的 Mcp-Session-Id，
// 一律走无状态路径（不登记），否则不实现会话的客户端每发一个请求就会
// 占用一个名额，很快撞上 MaxSessions 并被 -32014 拒绝。
func (m *Manager) Create(profile string) (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) >= m.cfg.Session.MaxSessions {
		return nil, ErrTooManySessions
	}

	id, _ := randomID()
	s := &State{
		ID:         id,
		Profile:    profile,
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

// SetProfile 记录"本次请求把这个会话绑定到了哪个 profile"，仅供观测。
//
// 鉴权不读这个值：Mcp-Session-Id 由客户端自行携带，同一个 id 可能被不同
// token 复用，所以权限必须每次请求重新按 token 解析。这里写入只是让
// session_list 能显示会话当前的归属。
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

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
