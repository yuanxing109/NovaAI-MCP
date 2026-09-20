// Package ratelimit 提供两层令牌桶限流（全局、shell）外加一个并发信号量。
//
// 设计约定：
//   - qps <= 0 视为"未配置"，该层不限流，直接放行；
//   - 桶在构造时一次性建好，没有惰性创建，也没有需要清扫的 key 表。
//
// 重构后没有"按身份"的层：所有来源本来就是同一个身份（无 token、
// 不分来源），再多一个维度只是多一处可被误读的状态。
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
	global    *bucket
	shell     *bucket
	globalQPS float64
	shellQPS  float64

	sem chan struct{}
}

func NewLimiter(cfg *config.Config) *Limiter {
	l := &Limiter{
		global:    newBucket(cfg.Limits.GlobalQPS, cfg.Limits.GlobalQPS*2),
		shell:     newBucket(cfg.Limits.ShellQPS, cfg.Limits.ShellQPS*2),
		globalQPS: cfg.Limits.GlobalQPS,
		shellQPS:  cfg.Limits.ShellQPS,
	}
	if n := cfg.Limits.MaxConcurrent; n > 0 {
		l.sem = make(chan struct{}, n)
	}
	return l
}

// shellTools 是需要单独计数的高消耗工具。
var shellTools = map[string]bool{
	"novaai_shell":  true,
	"novaai_script": true,
}

// Allow 依次检查全局 / shell 两层限流。
// 任一层拒绝即返回 *LimitError，并且不再消耗后面层的令牌。
func (l *Limiter) Allow(tool string) error {
	if !l.global.allow() {
		return &LimitError{Scope: ScopeGlobal, Tool: tool, Limit: l.globalQPS}
	}
	if shellTools[tool] && !l.shell.allow() {
		return &LimitError{Scope: ScopeTool, Tool: tool, Limit: l.shellQPS}
	}
	return nil
}

// AcquireSlot 尝试占用一个并发槽。返回 false 表示已达上限。
// 成功时必须配对调用 ReleaseSlot。
func (l *Limiter) AcquireSlot() bool {
	if l.sem == nil {
		return true
	}
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// ReleaseSlot 释放一个并发槽。
func (l *Limiter) ReleaseSlot() {
	if l.sem == nil {
		return
	}
	select {
	case <-l.sem:
	default:
	}
}

// ConcurrencyLimit 返回配置的并发上限，用于错误提示。
func (l *Limiter) ConcurrencyLimit() float64 {
	return float64(cap(l.sem))
}
