package config

import (
	"strings"
	"testing"
)

// 本文件锁住"配置里的路径必须是 POSIX 形式"。
//
// 背景：default.go 曾经用 filepath.Join 拼 stateDir 下的子路径。
// 在 Linux/Android 上它与 path.Join 等价，所以问题不可见；在 Windows 上
// 它会产出 `\data\adb\novaai-mcp\workspace`。而 pathguard.Normalize
// 对不以 "/" 开头的输入**原样返回**，于是这条路径会被当成相对路径，
// 受保护判定静默失效 —— 不报错，只是不再保护。
//
// 这类 bug 只在跨平台时显现，因此必须有测试而不是靠 review。

func TestDefaultPathsArePOSIX(t *testing.T) {
	cfg := Default()

	paths := map[string]string{
		"stateDir":      cfg.Paths.StateDir,
		"workDir":       cfg.Paths.WorkDir,
		"workspaceRoot": cfg.Paths.WorkspaceRoot,
		"auditDir":      cfg.Paths.AuditDir,
		"unixSocket":    cfg.Security.UnixSocket.Path,
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
	base := cfg.Paths.StateDir
	if base != "/data/adb/novaai-mcp" {
		t.Fatalf("stateDir 默认值变了: %q", base)
	}

	want := map[string]string{
		"workspaceRoot": base + "/workspace",
		"auditDir":      base + "/audit",
		"unixSocket":    base + "/mcp.sock",
	}
	got := map[string]string{
		"workspaceRoot": cfg.Paths.WorkspaceRoot,
		"auditDir":      cfg.Paths.AuditDir,
		"unixSocket":    cfg.Security.UnixSocket.Path,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q，期望 %q", k, got[k], w)
		}
	}
}

// profile 名与工具名都是标识符，不该受平台影响；
// 这条同时确认 DefaultProfiles 在两个平台上一致。
func TestDefaultProfilesAreStable(t *testing.T) {
	cfg := Default()
	p, ok := cfg.Profiles["readonly"]
	if !ok {
		t.Fatal("缺少 readonly profile")
	}
	if p.RiskCeiling != 0 {
		t.Errorf("readonly 的 riskCeiling = %d，期望 0", p.RiskCeiling)
	}
	if len(p.AllowTools) == 0 {
		t.Error("readonly 的 allowTools 为空")
	}
}
