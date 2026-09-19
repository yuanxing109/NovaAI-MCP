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

// Get 返回指定 profile。名字不存在时回退到 "default"。
//
// 这里曾经返回一个凭空构造的 profile（AllowTools: ["*"]、RiskCeiling: 1），
// 那是 fail-open：它比 default 宽松，也必然比用户想绑定的那个 profile 宽松。
// 具体后果是一个 typo —— `byTokenHash` 里把 "readonly" 写成 "redonly" ——
// 会把只读身份提升为可写，而 readonly 的 ceiling 是 0，构造出来的却是 1
// （已覆盖 novaai_fs_write / novaai_download / novaai_transfer_upload）。
//
// 回退到 default 而不是在这里报错，是因为本函数没有 error 返回位；配置层
// （config.Validate）负责在启动时拒绝悬空的 profile 引用，运行时只保证
// 绝不放行到比配置更宽的范围。
func (s *Store) Get(name string) config.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.profiles[name]; ok {
		return p
	}
	if p, ok := s.profiles["default"]; ok {
		return p
	}
	// profiles 里连 default 都没有：这是配置错误，Validate 会拦。
	// 真到了这里也只能给出最保守的形状——空白名单，而不是通配。
	return config.Profile{AllowTools: []string{}, DenyTools: []string{}, RiskCeiling: 0}
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
