package profile

import (
	"path/filepath"
	"sync"

	"github.com/novaai/novaai-mcp/internal/config"
)

// Store 是内置档位的容器。
//
// 现在只有 default 一个档位，也没有任何按 token 的动态绑定：
// 鉴权结果固定为全局 default profile，永不从 session 或 token 读取。
type Store struct {
	mu       sync.RWMutex
	profiles map[string]config.Profile
}

func NewStore() *Store {
	return &Store{profiles: config.DefaultProfiles()}
}

// Get 返回档位。名字不参与决策 —— 全局只有 default 一个档位。
//
// 保留 name 参数是为了不动调用点；它当前不产生任何分支，
// 这正是本轮重构的诉求：一个固定的权限归属，没有可误读的路径。
func (s *Store) Get(name string) config.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.profiles[config.DefaultProfileName]; ok {
		return p
	}
	// 内置档位缺失：这是不可能发生的配置错误。真到了这里也只能给出
	// 最保守的形状 —— 空白名单，而不是通配。
	return config.Profile{AllowTools: []string{}, DenyTools: []string{}, RiskCeiling: 0}
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
