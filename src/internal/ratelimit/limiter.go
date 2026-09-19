// Package ratelimit 提供三层令牌桶限流：全局、按客户端身份、按工具，
// 外加一个按客户端身份的并发调用信号量。
//
// 设计约定：
//   - qps <= 0 视为"未配置"，该层不限流，直接放行；
//   - 桶是惰性创建的，只在第一次用到该 key 时分配；
//   - 并发槽由调用方负责 Release，通常 defer 在 tools/call 处理末尾。
//
// 关于 key：第二层和并发槽的 key 必须是**客户端不可伪造**的身份标识，
// 当前取 token 的 SHA-256。绝不能用 Mcp-Session-Id —— 那是客户端自报的
// 请求头，只要每次换一个（或不带）就能拿到一个全新的满额桶，限流形同虚设。
// 因为 key 的取值范围由 token 数量决定（很小且固定），桶表天然有界，
// 不需要额外的清扫逻辑。
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
	ScopeClient     Scope = "client"
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
	case ScopeClient:
		return fmt.Sprintf("触发客户端身份限流（%.1f QPS），请降低调用频率", e.Limit)
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

	mu      sync.Mutex
	perKey  map[string]*bucket
	perTool map[string]*bucket
	keySem  map[string]chan struct{}
}

func NewLimiter(cfg *config.Config) *Limiter {
	l := &Limiter{
		cfg:     &cfg.RateLimit,
		perKey:  make(map[string]*bucket),
		perTool: make(map[string]*bucket),
		keySem:  make(map[string]chan struct{}),
	}
	l.global = newBucket(cfg.RateLimit.Global.QPS, cfg.RateLimit.Global.Burst)
	for name, b := range cfg.RateLimit.PerTool {
		l.perTool[name] = newBucket(b.QPS, b.Burst)
	}
	return l
}

func (l *Limiter) keyBucket(key string) *bucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.perKey[key]
	if !ok {
		b = newBucket(l.cfg.PerSession.QPS, l.cfg.PerSession.Burst)
		l.perKey[key] = b
	}
	return b
}

// Allow 依次检查全局 / 客户端身份 / 工具三层限流。
// 任一层拒绝即返回 *LimitError，并且不再消耗后面层的令牌。
//
// key 必须是客户端不可伪造的身份标识（见包注释）。
func (l *Limiter) Allow(key, tool string) error {
	if !l.global.allow() {
		return &LimitError{Scope: ScopeGlobal, Tool: tool, Limit: l.cfg.Global.QPS}
	}
	if !l.keyBucket(key).allow() {
		return &LimitError{Scope: ScopeClient, Tool: tool, Limit: l.cfg.PerSession.QPS}
	}
	l.mu.Lock()
	tb, ok := l.perTool[tool]
	l.mu.Unlock()
	if ok && !tb.allow() {
		return &LimitError{Scope: ScopeTool, Tool: tool, Limit: l.cfg.PerTool[tool].QPS}
	}
	return nil
}

// AcquireSlot 尝试占用一个客户端并发槽。返回 false 表示已达上限。
// 成功时必须配对调用 ReleaseSlot。
func (l *Limiter) AcquireSlot(key string) bool {
	max := l.cfg.PerSession.MaxConcurrentTools
	if max <= 0 {
		return true
	}
	l.mu.Lock()
	sem, ok := l.keySem[key]
	if !ok {
		sem = make(chan struct{}, max)
		l.keySem[key] = sem
	}
	l.mu.Unlock()

	select {
	case sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// ReleaseSlot 释放一个客户端并发槽。
func (l *Limiter) ReleaseSlot(key string) {
	l.mu.Lock()
	sem, ok := l.keySem[key]
	l.mu.Unlock()
	if !ok {
		return
	}
	select {
	case <-sem:
	default:
	}
}

// ConcurrencyLimit 返回配置的并发上限，用于错误提示。
func (l *Limiter) ConcurrencyLimit() float64 {
	return float64(l.cfg.PerSession.MaxConcurrentTools)
}
