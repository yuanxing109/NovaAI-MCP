package tools

import (
	"testing"

	"github.com/novaai/novaai-mcp/internal/profile"
)

// 风险表与注册表必须**互为子集**。
//
// 背景：`internal/profile/risk.go` 曾经列着 30 来个早已删除的工具
// （novaai_reverse_* / hook_* / skill / network / backup / device_info…），
// 而维护它的审计脚本没写 exit，所以那批幽灵条目一直没让 CI 报警。
//
// 两个方向都是缺陷：
//   - 表里有、注册表没有 → 谎报一个不存在的能力；
//   - 注册表有、表里没有 → ResolveRisk 落到兜底值 1（不是 0），只读工具
//     会在低风险上限的上游/档位下被挡掉。
func TestRiskTableMatchesRegistry(t *testing.T) {
	reg := newTestRegistry(t)

	registered := map[string]bool{}
	for _, tool := range reg.List() {
		registered[tool.Name] = true
	}

	declared := map[string]bool{}
	for _, name := range profile.KnownTools() {
		declared[name] = true
		if !registered[name] {
			t.Errorf("风险表里的 %s 不是已注册工具 —— 删工具时忘了删条目", name)
		}
	}
	for _, tool := range reg.List() {
		if !declared[tool.Name] {
			t.Errorf("工具 %s 不在风险表里 —— ResolveRisk 会按兜底值 1 处理", tool.Name)
		}
	}
}
