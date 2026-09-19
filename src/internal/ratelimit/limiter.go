// Package ratelimit 提供三层令牌桶限流：全局、按会话、按工具，
// 外加一个按会话的并发调用信号量。
//
// 设计约定：
//   - qps <= 0 视为"未配置"，该层不限流，直接放行；
//   - 桶是惰性创建的，只在第一次用到该会话/工具时分配；
//   - 并发槽由调用方负责 Release，通常 defer 在 tools/call 处理末尾。
package ratelimit

import (
	"fmt"
	"sync"
	"time"

	"github.com/novaai/novaai-mcp/internal/config"
)

// Scope 标识触发了哪一层限流。
type Scope string

const (
	ScopeGlobal     Scope = "global"
	ScopeSession    Scope = "session"
	ScopeTool       Scope = "tool"
	ScopeConcurrent Scope = "concurrency"
)

// LimitError 表示一次调用被限流拒绝。
type LimitError struct {
	Scope Scope
	Tool  string
	Limit float64
}

func (e *LimitError) Error() string {
	switch e.Scope {
	case ScopeGlobal:
		return fmt.Sprintf("触发全局限流（%.1f QPS），请降低调用频率", e.Limit)
	case ScopeSession:
		return fmt.Sprintf("触发会话限流（%.1f QPS），请降低调用频率", e.Limit)
	case ScopeTool:
		return fmt.Sprintf("工具 %s 触发限流（%.1f QPS）", e.Tool, e.Limit)
	case ScopeConcurrent:
		return fmt.Sprintf("并发调用数超过上限（%.0f）", e.Limit)
	default:
		return "触发限流"
	}
}

type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	qps    float64
	burst  float64
}

func newBucket(qps, burst float64) *bucket {
	if burst <= 0 {
		burst = qps
	}
	if burst < 1 {
		burst = 1
	}
	return &bucket{
		tokens: burst,
		last:   time.Now(),
		qps:    qps,
		burst:  burst,
	}
}

// allow 消耗一个令牌。qps<=0 时恒为 true。
func (b *bucket) allow() bool {
	if b == nil || b.qps <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * b.qps
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Limiter 是限流器本体，零值不可用，必须经 NewLimiter 构造。
type Limiter struct {
	cfg *config.RateLimitConfig

	global *bucket

	mu         sync.Mutex
	perSession map[string]*bucket
	perTool    map[string]*bucket
	sessSem    map[string]chan struct{}
}

func NewLimiter(cfg *config.Config) *Limiter {
	l := &Limiter{
		cfg:        &cfg.RateLimit,
		perSession: make(map[string]*bucket),
		perTool:    make(map[string]*bucket),
		sessSem:    make(map[string]chan struct{}),
	}
	l.global = newBucket(cfg.RateLimit.Global.QPS, cfg.RateLimit.Global.Burst)
	for name, b := range cfg.RateLimit.PerTool {
		l.perTool[name] = newBucket(b.QPS, b.Burst)
	}
	return l
}

func (l *Limiter) sessionBucket(id string) *bucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.perSession[id]
	if !ok {
		b = newBucket(l.cfg.PerSession.QPS, l.cfg.PerSession.Burst)
		l.perSession[id] = b
	}
	return b
}

// Allow 依次检查全局 / 会话 / 工具三层限流。
// 任一层拒绝即返回 *LimitError，并且不再消耗后面层的令牌。
func (l *Limiter) Allow(sessionID, tool string) error {
	if !l.global.allow() {
		return &LimitError{Scope: ScopeGlobal, Tool: tool, Limit: l.cfg.Global.QPS}
	}
	if !l.sessionBucket(sessionID).allow() {
		return &LimitError{Scope: ScopeSession, Tool: tool, Limit: l.cfg.PerSession.QPS}
	}
	l.mu.Lock()
	tb, ok := l.perTool[tool]
	l.mu.Unlock()
	if ok && !tb.allow() {
		return &LimitError{Scope: ScopeTool, Tool: tool, Limit: l.cfg.PerTool[tool].QPS}
	}
	return nil
}

// AcquireSlot 尝试占用一个会话并发槽。返回 false 表示已达上限。
// 成功时必须配对调用 ReleaseSlot。
func (l *Limiter) AcquireSlot(sessionID string) bool {
	max := l.cfg.PerSession.MaxConcurrentTools
	if max <= 0 {
		return true
	}
	l.mu.Lock()
	sem, ok := l.sessSem[sessionID]
	if !ok {
		sem = make(chan struct{}, max)
		l.sessSem[sessionID] = sem
	}
	l.mu.Unlock()

	select {
	case sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// ReleaseSlot 释放一个会话并发槽。
func (l *Limiter) ReleaseSlot(sessionID string) {
	l.mu.Lock()
	sem, ok := l.sessSem[sessionID]
	l.mu.Unlock()
	if !ok {
		return
	}
	select {
	case <-sem:
	default:
	}
}

// ConcurrencyLimit 返回配置的会话并发上限，用于错误提示。
func (l *Limiter) ConcurrencyLimit() float64 {
	return float64(l.cfg.PerSession.MaxConcurrentTools)
}

// Sweep 只保留 keep 中的会话，其余一律清理。
// 应由上层在会话建立/回收时调用，避免 map 随历史会话无限增长。
func (l *Limiter) Sweep(keep map[string]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id := range l.perSession {
		if !keep[id] {
			delete(l.perSession, id)
		}
	}
	for id := range l.sessSem {
		if !keep[id] {
			delete(l.sessSem, id)
		}
	}
}
