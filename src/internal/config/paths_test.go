package config

import (
	"strings"
	"testing"
)

// 本文件锁住"配置里的路径必须是 POSIX 形式"。
//
// 背景：stateDir 下的子路径曾经用 filepath.Join 拼接，在 Windows 上会产出
// `\data\adb\novaai-mcp\workspace`。而 pathguard.Normalize 对不以 "/" 开头的
// 输入**原样返回**，于是这条路径会被当成相对路径，受保护判定静默失效。
//
// 这类 bug 只在跨平台时显现，因此必须有测试而不是靠 review。

func TestDefaultPathsArePOSIX(t *testing.T) {
	cfg := Default()

	paths := map[string]string{
		"stateDir":      cfg.StateDir,
		"workspaceRoot": cfg.WorkspaceRoot(),
		"auditDir":      cfg.AuditDir(),
		"unixSocket":    cfg.UnixSocket,
	}
	for name, p := range paths {
		if !strings.HasPrefix(p, "/") {
			t.Errorf("%s = %q，必须以 / 开头", name, p)
		}
		if strings.Contains(p, `\`) {
			t.Errorf("%s = %q 含反斜杠 —— 这些是 Android 路径，"+
				"不能用 filepath.Join 拼接", name, p)
		}
	}
}

// 直接把每个子路径与 stateDir 手工拼接的结果比对，
// 防止将来有人"顺手"改回 filepath.Join 而前缀检查又恰好通过。
func TestDefaultSubpathsJoinWithSlash(t *testing.T) {
	cfg := Default()
	base := cfg.StateDir
	if base != "/data/adb/novaai-mcp" {
		t.Fatalf("stateDir 默认值变了: %q", base)
	}

	want := map[string]string{
		"workspaceRoot": base + "/workspace",
		"auditDir":      base + "/audit",
		"unixSocket":    base + "/mcp.sock",
	}
	got := map[string]string{
		"workspaceRoot": cfg.WorkspaceRoot(),
		"auditDir":      cfg.AuditDir(),
		"unixSocket":    cfg.UnixSocket,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q，期望 %q", k, got[k], w)
		}
	}
}

// 默认档位固定为 default，且放行全部工具。
func TestDefaultProfileIsStable(t *testing.T) {
	cfg := Default()
	if cfg.Profile != DefaultProfileName {
		t.Errorf("默认 profile = %q，期望 %q", cfg.Profile, DefaultProfileName)
	}
	p, ok := DefaultProfiles()[DefaultProfileName]
	if !ok {
		t.Fatalf("缺少 %s 档位", DefaultProfileName)
	}
	if p.RiskCeiling != 3 {
		t.Errorf("%s 的 riskCeiling = %d，期望 3", DefaultProfileName, p.RiskCeiling)
	}
	if len(p.AllowTools) != 1 || p.AllowTools[0] != "*" {
		t.Errorf("%s 的 allowTools = %v，期望 [\"*\"]", DefaultProfileName, p.AllowTools)
	}
	if len(p.DenyTools) != 0 {
		t.Errorf("%s 的 denyTools = %v，期望为空", DefaultProfileName, p.DenyTools)
	}
}
