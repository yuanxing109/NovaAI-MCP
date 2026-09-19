package profile

import (
	"testing"

	"github.com/novaai/novaai-mcp/internal/config"
)

// 本文件锁住 Store.Get 的回退语义。
//
// 曾经的行为是 fail-open：名字不存在时返回一个凭空构造的
// `{AllowTools: ["*"], RiskCeiling: 1}`。那比 default 宽松，也必然比
// 用户想绑定的那个 profile 宽松——一个 typo（readonly -> redonly）
// 就把只读身份提升成可写（fs_write / download / transfer_upload 都在
// risk 1，而 readonly 的 ceiling 是 0）。

func testStore() *Store {
	cfg := config.Default()
	cfg.Profiles["tiny"] = config.Profile{
		AllowTools:  []string{"novaai_status"},
		DenyTools:   []string{},
		RiskCeiling: 0,
	}
	return NewStore(cfg)
}

func TestGetReturnsKnownProfile(t *testing.T) {
	s := testStore()
	p := s.Get("tiny")
	if p.RiskCeiling != 0 || len(p.AllowTools) != 1 {
		t.Fatalf("已知 profile 应当原样返回，实际: %+v", p)
	}
}

func TestGetUnknownFallsBackToDefaultNotWildcard(t *testing.T) {
	s := testStore()
	got := s.Get("redonly") // 打错的 readonly
	want := s.Get("default")

	if got.RiskCeiling != want.RiskCeiling {
		t.Errorf("未知 profile 的 riskCeiling = %d，期望跟随 default = %d",
			got.RiskCeiling, want.RiskCeiling)
	}

	// 关键回归：回退结果绝不允许比 default 更宽。
	// 具体来说，旧实现给的 ceiling 1 会让 novaai_fs_write 通过，
	// 而 default（denyTools 含 shell/config 等）不该允许它被"凭空允许"。
	if got.RiskCeiling == 1 && len(got.DenyTools) == 0 {
		t.Fatal("未知 profile 回退到了一个凭空构造的宽松档位（fail-open 回归）")
	}

	// default 的 denyTools 必须一起带过来，否则 shell 封锁会丢失。
	if len(got.DenyTools) != len(want.DenyTools) {
		t.Errorf("回退结果丢失了 default 的 denyTools: got %v want %v",
			got.DenyTools, want.DenyTools)
	}
	for i := range want.DenyTools {
		if i < len(got.DenyTools) && got.DenyTools[i] != want.DenyTools[i] {
			t.Errorf("denyTools 不一致: got %v want %v", got.DenyTools, want.DenyTools)
			break
		}
	}
}

func TestGetUnknownProfileStillDeniesShell(t *testing.T) {
	s := testStore()
	p := s.Get("no_such_profile")
	// 工具名与风险等级都按最容易通过的情形给：allowTools 为 ["*"]、
	// risk 取 0。即便如此，denyTools 里的 novaai_shell 也必须被拒。
	if Allows(&p, "novaai_shell", 0) {
		t.Fatal("未知 profile 回退后仍然允许 novaai_shell —— 回退必须是保守的")
	}
}

func TestGetWithoutDefaultIsDenyAll(t *testing.T) {
	store := &Store{
		profiles: map[string]config.Profile{},
		binding:  config.SessionBinding{},
	}
	p := store.Get("anything")
	if p.RiskCeiling != 0 {
		t.Errorf("无 default 时 riskCeiling = %d，期望 0", p.RiskCeiling)
	}
	if Allows(&p, "novaai_status", 0) {
		t.Fatal("无 default 时不允许任何工具")
	}
}

// ResolveByTokenHash 只负责解析名字，不校验名字是否存在
// （那是 config.Validate 的职责）。这里锁住它的解析优先级。
func TestResolveByTokenHashPriority(t *testing.T) {
	s := testStore()
	if got := s.ResolveByTokenHash("deadbeef"); got != "default" {
		t.Errorf("无绑定无 fallback 时应为 default，实际 %q", got)
	}
}
