package profile

import (
	"path/filepath"
	"sync"

	"github.com/novaai/novaai-mcp/internal/config"
)

type Store struct {
	mu       sync.RWMutex
	profiles map[string]config.Profile
	binding  config.SessionBinding
}

func NewStore(cfg *config.Config) *Store {
	return &Store{
		profiles: cfg.Profiles,
		binding:  cfg.SessionBinding,
	}
}

func (s *Store) Get(name string) config.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.profiles[name]; ok {
		return p
	}
	return config.Profile{AllowTools: []string{"*"}, RiskCeiling: 1}
}

func (s *Store) ResolveByTokenHash(hash string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if name, ok := s.binding.ByTokenHash[hash]; ok {
		return name
	}
	if s.binding.Fallback != "" {
		return s.binding.Fallback
	}
	return "default"
}

func (s *Store) Update(cfg *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles = cfg.Profiles
	s.binding = cfg.SessionBinding
}

// Allows 检查 profile 是否允许调用指定工具
func Allows(p *config.Profile, tool string, risk int) bool {
	if risk > p.RiskCeiling {
		return false
	}
	for _, d := range p.DenyTools {
		if matchGlob(d, tool) {
			return false
		}
	}
	for _, a := range p.AllowTools {
		if matchGlob(a, tool) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}
