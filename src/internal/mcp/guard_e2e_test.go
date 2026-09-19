package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/pathguard"
	"github.com/novaai/novaai-mcp/internal/profile"
	"github.com/novaai/novaai-mcp/internal/tools"
)

// 本文件是**端到端**回归：把 tools/call 真的送进 Server.Handle，
// 而不是只测 pathguard 的判定函数。
//
// 为什么两者都要：单元测试证明"判定正确"，只有端到端能证明"判定被接线"。
// 一个 guard 写好了却没人调用，单元测试全绿而行为毫无变化 ——
// 这正是 fs_read 此前的状态（完全没有守卫）。

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Default()
	// 状态目录指向一个确定的位置，让 stateDir 规则可预期。
	pathguard.SetStateDir(cfg.Paths.StateDir)

	// 审计写到临时目录，避免污染真实状态目录（也不让测试因权限失败）。
	cfg.Paths.AuditDir = t.TempDir()
	logger, err := audit.NewLogger(cfg)
	if err != nil {
		t.Fatalf("构造审计器失败: %v", err)
	}
	t.Cleanup(logger.Close)

	reg := tools.NewRegistry()
	deps := &tools.Deps{
		Config:   cfg,
		Audit:    logger,
		Profiles: profile.NewStore(cfg),
		Version:  "test",
		Commit:   "test",
	}
	tools.RegisterAll(reg, deps)

	return &Server{cfg: &ServerConfig{
		Config:   cfg,
		Registry: reg,
		Audit:    logger,
		Deps:     deps,
	}}
}

// callTool 调用一个工具，返回 Content 文本（JSON-RPC error 时为错误消息）。
func callTool(t *testing.T, s *Server, name string, args map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		t.Fatalf("构造参数失败: %v", err)
	}
	resp := s.Handle(&JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  raw,
	}, Identity{Profile: "agent_full", RateKey: "test"})

	if resp == nil {
		t.Fatal("Handle 返回 nil")
	}
	if resp.Error != nil {
		return resp.Error.Message
	}
	b, _ := json.Marshal(resp.Result)
	return string(b)
}

// ---- 只读根：写默认拒绝 ----

func TestE2EWriteToAndroidDataDeniedByDefault(t *testing.T) {
	s := newTestServer(t)
	out := callTool(t, s, "novaai_fs_write", map[string]any{
		"action":  "create",
		"path":    "/sdcard/Android/data/com.example.app/files/evil.txt",
		"content": "x",
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("未确认时写入 Android/data 应当被拒，实际: %s", out)
	}
	// 错误里必须提示"可确认"，否则用户不知道还有这条路。
	if !strings.Contains(out, "confirmDangerous") {
		t.Errorf("拒绝理由未提示 confirmDangerous: %s", out)
	}
}

func TestE2EWriteToAndroidDataAllowedWithConfirm(t *testing.T) {
	s := newTestServer(t)
	// 用 touch 而不是 create：create 会真的写文件，而这个路径在
	// 开发机上不存在也无需存在 —— guard 通过后 os 调用失败是正常的，
	// 我们要断言的是**没有**返回 PROTECTED_PATH。
	out := callTool(t, s, "novaai_fs_write", map[string]any{
		"action":           "touch",
		"path":             "/sdcard/Android/data/com.example.app/files/x.txt",
		"confirmDangerous": true,
	})
	if strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("确认后不应再被 PROTECTED_PATH 拒绝，实际: %s", out)
	}
}

func TestE2EConfirmDoesNotUnlockSystemPartition(t *testing.T) {
	s := newTestServer(t)
	out := callTool(t, s, "novaai_fs_write", map[string]any{
		"action":           "create",
		"path":             "/system/build.prop",
		"content":          "x",
		"confirmDangerous": true,
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("确认不得解锁 /system，实际: %s", out)
	}
}

func TestE2EWriteToModulesDirStillDenied(t *testing.T) {
	s := newTestServer(t)
	out := callTool(t, s, "novaai_fs_write", map[string]any{
		"action":           "create",
		"path":             "/data/adb/modules/evil/system.prop",
		"content":          "x",
		"confirmDangerous": true,
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("确认不得解锁 /data/adb/modules，实际: %s", out)
	}
}

// ---- 普通路径不受影响 ----

func TestE2EWriteToNormalPathNotBlocked(t *testing.T) {
	s := newTestServer(t)
	for _, p := range []string{
		"/sdcard/DCIM/a.txt",
		"/sdcard/Android/media/com.example.app/x.txt", // media 是公开的
		"/data/local/tmp/x.txt",
	} {
		out := callTool(t, s, "novaai_fs_write", map[string]any{
			"action": "touch", "path": p,
		})
		if strings.Contains(out, "PROTECTED_PATH") {
			t.Errorf("%s 不该被拒，实际: %s", p, out)
		}
	}
}

// ---- 读取：Android/data 可读 ----

func TestE2EReadAndroidDataNotBlockedByGuard(t *testing.T) {
	s := newTestServer(t)
	// 路径不存在，因此会得到 READ_FAILED；关键是**不是** PROTECTED_PATH。
	out := callTool(t, s, "novaai_fs_read", map[string]any{
		"action": "text",
		"path":   "/sdcard/Android/data/com.example.app/files/x.txt",
	})
	if strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("读取 Android/data 不该被守卫拒绝，实际: %s", out)
	}
}

func TestE2EReadBlockDeviceDenied(t *testing.T) {
	s := newTestServer(t)
	out := callTool(t, s, "novaai_fs_read", map[string]any{
		"action": "binary",
		"path":   "/dev/block/by-name/boot",
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("读取块设备应当被拒，实际: %s", out)
	}
}

// ---- fs_manage remove 走同一条确认语义 ----

func TestE2EManageRemoveAndroidDataNeedsConfirm(t *testing.T) {
	s := newTestServer(t)

	// 不带 confirmDangerous：连 NOT_CONFIRMED 都过不去（这是更早的一道闸）。
	out := callTool(t, s, "novaai_fs_manage", map[string]any{
		"action": "remove",
		"path":   "/sdcard/Android/data/com.example.app",
	})
	if !strings.Contains(out, "NOT_CONFIRMED") {
		t.Fatalf("未确认的 remove 应当报 NOT_CONFIRMED，实际: %s", out)
	}

	// 带 confirmDangerous 且目标是硬拒绝位置：仍然被拒。
	out = callTool(t, s, "novaai_fs_manage", map[string]any{
		"action":           "remove",
		"path":             "/data/adb/modules/x",
		"confirmDangerous": true,
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("确认不得解锁 /data/adb/modules，实际: %s", out)
	}
}
