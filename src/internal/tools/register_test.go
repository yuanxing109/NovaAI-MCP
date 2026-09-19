package tools

import (
	"testing"

	"github.com/novaai/novaai-mcp/internal/adapter"
	"github.com/novaai/novaai-mcp/internal/config"
)

// expectedToolCount 是 README 与 docs 对外承诺的工具总数。
//
// 写死在这里是刻意的：早期 README 写"共 61 个工具"而实际只有 56 个，
// 原因是 `audit_actions.ps1` 只扫描 `internal/tools/v02`，漏掉了
// `internal/tools` 里那 5 个（auth/session×2/audit/health）用
// `reg.Register(&Tool{...})` 注册的工具。数字漂移过一次，就用断言锁住。
//
// 若这是有意的增删：请同步 README.md 的工具表与 docs/extensions.md。
const expectedToolCount = 61

// mustPresent 是"这个工具必须存在"的清单，覆盖两个注册通路。
var mustPresent = []string{
	// v02 回调通路
	"novaai_status",
	"novaai_shell",
	"novaai_skill",
	"novaai_schedule",
	// 直注册通路（audit_actions.ps1 扫不到这些）
	"novaai_auth_status",
	"novaai_session_status",
	"novaai_session_list",
	"novaai_audit_status",
	"novaai_health_status",
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()

	cfg := config.Default()
	reg := NewRegistry()
	RegisterAll(reg, &Deps{
		Config:   cfg,
		Adapter:  adapter.NewAOSPAdapter(map[string]string{}),
		StateDir: t.TempDir(),
		Version:  "test",
	})
	return reg
}

func TestRegisteredToolCount(t *testing.T) {
	reg := newTestRegistry(t)

	if got := reg.Count(); got != expectedToolCount {
		t.Fatalf("工具数 = %d，期望 %d。\n"+
			"若这是有意的增删，请同步 README.md 的工具表与 docs/extensions.md，"+
			"并更新本常量。", got, expectedToolCount)
	}
}

func TestRequiredToolsAreRegistered(t *testing.T) {
	reg := newTestRegistry(t)

	for _, name := range mustPresent {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("工具未注册: %s", name)
		}
	}
}

// TestToolsListHasNoDuplicates 保证 order 与 tools map 一致：
// Register 对已存在的名字不重复追加 order，因此两个长度必须相等。
func TestToolsListHasNoDuplicates(t *testing.T) {
	reg := newTestRegistry(t)

	list := reg.List()
	if len(list) != reg.Count() {
		t.Fatalf("List() 返回 %d 个，Count() = %d", len(list), reg.Count())
	}

	seen := map[string]bool{}
	for _, tool := range list {
		if seen[tool.Name] {
			t.Errorf("tools/list 里出现重复工具: %s", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Handler == nil {
			t.Errorf("工具 %s 没有 handler", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("工具 %s 没有描述", tool.Name)
		}
	}
}
