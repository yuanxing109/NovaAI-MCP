package tools

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/novaai/novaai-mcp/internal/adapter"
	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/profile"
	"github.com/novaai/novaai-mcp/internal/session"
)

type Handler func(ctx context.Context, args json.RawMessage) (any, error)

type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Handler     Handler        `json:"-"`
}

type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
	order []string
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]*Tool)}
}

func (r *Registry) Register(t *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name]; !exists {
		r.order = append(r.order, t.Name)
	}
	r.tools[t.Name] = t
}

func (r *Registry) Get(name string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) List() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Deps 工具层共享依赖
type Deps struct {
	Config   *config.Config
	Audit    *audit.Logger
	Sessions *session.Manager
	Profiles *profile.Store
	Adapter  adapter.Adapter
	Version  string
	Commit   string
	StateDir string
}

// ---- 请求级 context 键 ----

type ctxKey string

const (
	ctxSessionID ctxKey = "novaai.session_id"
	ctxDeps      ctxKey = "novaai.deps"
)

func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxSessionID, id)
}

func SessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxSessionID).(string); ok {
		return v
	}
	return ""
}

func WithDeps(ctx context.Context, d *Deps) context.Context {
	return context.WithValue(ctx, ctxDeps, d)
}

func DepsFromContext(ctx context.Context) *Deps {
	if v, ok := ctx.Value(ctxDeps).(*Deps); ok {
		return v
	}
	return nil
}
