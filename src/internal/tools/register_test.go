package tools

import (
	"testing"

	"github.com/novaai/novaai-mcp/internal/adapter"
	"github.com/novaai/novaai-mcp/internal/config"
)

// expectedToolCount 是 README 与 docs 对外承诺的**本地**工具总数。
//
// 写死在这里是刻意的：数字漂移过一次（README 写 61 而实际 56），
// 就用断言锁住。
//
// 来源拆解（方案 §1.6 + §2.7）：
//   - 28 个 v02 回调通路注册（服务 4 / 文件 6 / 归档 4 / 执行 2 /
//     应用 4 / 系统 5 / Root 3）—— §1.6 的清单里 health_status 与
//     upstream_status 不在 v02；
//   - 1 个直注册：novaai_health_status；
//   - 1 个直注册：novaai_upstream_status（§2.7 新增的上游状态工具）。
//
// **上游 MCP 的工具不计入**：它们的数量随用户配置动态变化，
// 由 upstream.Registry.MergedTools() 在 tools/list 时实时合并。
// 若这是有意的增删：请同步 README.md 的工具表与 docs/extensions.md。
const expectedToolCount = 30

// mustPresent 是"这个工具必须存在"的清单，覆盖两个注册通路。
var mustPresent = []string{
	// v02 回调通路
	"novaai_status",
	"novaai_shell",
	"novaai_log",
	"novaai_power",
	// 直注册通路
	"novaai_health_status",
	"novaai_upstream_status",
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

// TestRemovedToolsAreGone 锁住精简结果：被裁掉的工具不允许留在注册表里。
//
// 它们的能力一律改由 novaai_shell 承担。留一个"半死"的工具比删掉更糟：
// 客户端会看到它、调用它，而它不再有人维护。
func TestRemovedToolsAreGone(t *testing.T) {
	reg := newTestRegistry(t)

	for _, name := range []string{
		"novaai_reverse_apk", "novaai_hook_frida", "novaai_skill",
		"novaai_schedule", "novaai_app_permission", "novaai_app_policy",
		"novaai_app_export", "novaai_default_app", "novaai_notification",
		"novaai_service", "novaai_property", "novaai_setting",
		"novaai_display", "novaai_audio", "novaai_connectivity",
		"novaai_locale_time", "novaai_input_method", "novaai_developer",
		"novaai_accessibility", "novaai_network", "novaai_backup",
		"novaai_device_info", "novaai_auth_status", "novaai_session_status",
		"novaai_session_list", "novaai_audit_status",
	} {
		if _, ok := reg.Get(name); ok {
			t.Errorf("工具 %s 已被裁掉，不应再注册", name)
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
