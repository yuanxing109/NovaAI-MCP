package profile

import (
	"testing"

	"github.com/novaai/novaai-mcp/internal/config"
)

// 本文件锁住"档位固定为 default"这条不变式。
//
// 重构前这里锁的是 Store.Get 的**回退语义**：名字不存在时必须落到
// conservative 而不是 default，避免 typo 升格为完全权限。
// 现在没有第二个档位，也就没有回退路径可走 —— Get 对任何名字都返回
// default，配置里也写不出别的档位（config.Validate 会拒绝）。

func TestGetAlwaysReturnsDefault(t *testing.T) {
	s := NewStore()
	for _, name := range []string{"", "default", "redonly", "no_such_profile"} {
		p := s.Get(name)
		if p.RiskCeiling != 3 || len(p.AllowTools) != 1 || p.AllowTools[0] != "*" {
			t.Fatalf("Get(%q) = %+v，期望始终是 default 档位", name, p)
		}
	}
}

// default 不 deny 任何工具。
func TestDefaultProfileAllowsAll(t *testing.T) {
	s := NewStore()
	p := s.Get(config.DefaultProfileName)
	if len(p.DenyTools) != 0 {
		t.Fatalf("default.denyTools 应为空，实际: %v", p.DenyTools)
	}
	for _, tool := range []string{
		"novaai_status", "novaai_fs_write", "novaai_shell", "novaai_script",
		"novaai_root_module", "novaai_systemless", "novaai_config",
	} {
		if !Allows(&p, tool, 0) {
			t.Errorf("default 不应拒绝 %s", tool)
		}
	}
}

// shell 必须放行：装完即用、含 shell 是明确诉求。
func TestDefaultProfileAllowsShell(t *testing.T) {
	s := NewStore()
	p := s.Get(config.DefaultProfileName)
	if !Allows(&p, "novaai_shell", 3) {
		t.Fatal("default 必须放行 novaai_shell")
	}
}

// 风险上限仍然生效：risk 超过 ceiling 一律拒绝。
func TestAllowsEnforcesRiskCeiling(t *testing.T) {
	p := config.Profile{AllowTools: []string{"*"}, RiskCeiling: 1}
	if Allows(&p, "novaai_shell", 2) {
		t.Fatal("risk=2 超过 ceiling=1 时必须拒绝")
	}
	if !Allows(&p, "novaai_shell", 1) {
		t.Fatal("risk=1 未超过 ceiling=1，必须放行")
	}
}
