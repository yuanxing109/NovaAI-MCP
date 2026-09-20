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
//
// 重构后的变化：confirmDangerous 机制已移除，/sdcard/Android/{data,obb}
// 与 /system 一样是硬拒绝，工具层没有放行通道。

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Default()
	// 状态目录指向一个确定的位置，让 stateDir 规则可预期；
	// 审计随之写到该目录下的 audit/。
	cfg.StateDir = t.TempDir()
	pathguard.SetStateDir(cfg.StateDir)

	logger, err := audit.NewLogger(cfg)
	if err != nil {
		t.Fatalf("构造审计器失败: %v", err)
	}
	t.Cleanup(logger.Close)

	reg := tools.NewRegistry()
	deps := &tools.Deps{
		Config:   cfg,
		Audit:    logger,
		Profiles: profile.NewStore(),
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
	}, Identity{Profile: config.DefaultProfileName})

	if resp == nil {
		t.Fatal("Handle 返回 nil")
	}
	if resp.Error != nil {
		return resp.Error.Message
	}
	b, _ := json.Marshal(resp.Result)
	return string(b)
}

// ---- 应用私有外部存储：硬拒绝，无确认通道 ----

func TestAndroidDataHardDenied(t *testing.T) {
	s := newTestServer(t)

	for _, p := range []string{
		"/sdcard/Android/data/com.example.app/files/evil.txt",
		"/sdcard/Android/obb/com.example.app/main.1.obb",
		"/storage/emulated/0/Android/data/com.example.app/x",
		"/mnt/sdcard/Android/data/com.example.app/x",
	} {
		out := callTool(t, s, "novaai_fs_write", map[string]any{
			"action":  "create",
			"path":    p,
			"content": "x",
		})
		if !strings.Contains(out, "PROTECTED_PATH") {
			t.Errorf("写入 %s 应当被硬拒绝，实际: %s", p, out)
		}
	}
}

// 硬拒绝不能通过任何参数放行 —— confirmDangerous 已经不存在，
// 传了也不该有任何效果。
func TestAndroidDataDenyIgnoresRemovedConfirmFlag(t *testing.T) {
	s := newTestServer(t)
	out := callTool(t, s, "novaai_fs_write", map[string]any{
		"action":           "touch",
		"path":             "/sdcard/Android/data/com.example.app/files/x.txt",
		"confirmDangerous": true,
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("confirmDangerous 已移除，不应放行任何路径，实际: %s", out)
	}
}

func TestE2EWriteToSystemPartitionDenied(t *testing.T) {
	s := newTestServer(t)
	out := callTool(t, s, "novaai_fs_write", map[string]any{
		"action":  "create",
		"path":    "/system/build.prop",
		"content": "x",
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("/system 必须被拒绝，实际: %s", out)
	}
}

func TestE2EWriteToModulesDirStillDenied(t *testing.T) {
	s := newTestServer(t)
	out := callTool(t, s, "novaai_fs_write", map[string]any{
		"action":  "create",
		"path":    "/data/adb/modules/evil/system.prop",
		"content": "x",
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("/data/adb/modules 必须被拒绝，实际: %s", out)
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

// ---- 读取：Android/data 仍然可读 ----

func TestE2EReadAndroidDataNotBlockedByGuard(t *testing.T) {
	s := newTestServer(t)
	// 路径不存在，因此会得到 OPEN_FAILED；关键是**不是** PROTECTED_PATH。
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

// ---- fs_manage remove 走同一条硬拒绝 ----

func TestE2EManageRemoveAndroidDataDenied(t *testing.T) {
	s := newTestServer(t)
	out := callTool(t, s, "novaai_fs_manage", map[string]any{
		"action": "remove",
		"path":   "/sdcard/Android/data/com.example.app",
	})
	if !strings.Contains(out, "PROTECTED_PATH") {
		t.Fatalf("remove Android/data 应当被拒，实际: %s", out)
	}
}
