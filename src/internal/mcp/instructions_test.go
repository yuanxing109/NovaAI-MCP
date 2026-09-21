package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/tools"
)

// newInfoServer 构造一个只够回答 initialize 的 Server。
//
// StateDir 一律用 t.TempDir()，**不要**拿真实设备路径（/data/adb/novaai-mcp）
// 当 StateDir：audit.NewLogger 会立刻 MkdirAll(stateDir/audit)，而 CI
// （ubuntu-latest、非 root 用户）建不了 /data —— 本文件的早期版本就是这么写的，
// 结果本地（Windows 上 "/data" 落在 C:\data，能建）全绿、CI 一跑就
// "构造审计器失败"。默认路径那条规则改用纯函数断言（见
// TestInstructionsUsesDefaultStateDir），不落盘。
func newInfoServer(t *testing.T, version string) *Server {
	t.Helper()

	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.ShellTimeoutSeconds = 5

	logger, err := audit.NewLogger(cfg)
	if err != nil {
		t.Fatalf("构造审计器失败: %v", err)
	}
	t.Cleanup(logger.Close)

	return &Server{cfg: &ServerConfig{
		Config: cfg, Registry: tools.NewRegistry(), Audit: logger,
		Deps:    &tools.Deps{Config: cfg, Audit: logger, StateDir: cfg.StateDir},
		Version: version,
	}}
}

func initializeInfo(t *testing.T, s *Server) map[string]any {
	t.Helper()
	req := &JSONRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize",
		Params: json.RawMessage(`{"protocolVersion":"2025-06-18",` +
			`"clientInfo":{"name":"test","version":"1"}}`),
	}
	resp := s.handleInitialize(req, "")
	if resp == nil {
		t.Fatal("initialize 没有响应")
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	return out
}

// serverInfo.version 必须是**构建注入的版本**。
//
// 这条以前会失败：握手读的是 mcp.ServerVersion 常量（0.05），而
// novaai_status 读的是构建注入值 —— 模块升到 0.06 后两处报的版本不一样。
func TestInitializeReportsInjectedVersion(t *testing.T) {
	s := newInfoServer(t, "9.9.9-test")
	info := initializeInfo(t, s)["serverInfo"].(map[string]any)
	if got := info["version"]; got != "9.9.9-test" {
		t.Fatalf("serverInfo.version = %v，期望注入的 9.9.9-test", got)
	}
}

// 没有注入版本时退回常量，不能变成空字符串。
func TestInitializeVersionFallsBack(t *testing.T) {
	s := newInfoServer(t, "")
	info := initializeInfo(t, s)["serverInfo"].(map[string]any)
	if got := info["version"]; got != ServerVersion {
		t.Fatalf("serverInfo.version = %v，期望退回常量 %s", got, ServerVersion)
	}
}

// instructions 必须给出技能目录：skills/*.md 没有工具入口，握手时不说，
// 客户端就无从知道它们存在（这正是它们随包分发却没人读的原因）。
func TestInstructionsPointAtSkillsDir(t *testing.T) {
	s := newInfoServer(t, "x")
	ins, _ := initializeInfo(t, s)["instructions"].(string)
	if !strings.Contains(ins, s.stateDir()+"/skills/") {
		t.Fatalf("instructions 未给出技能目录 %s/skills/：%q", s.stateDir(), ins)
	}
	if !strings.Contains(ins, "novaai_fs_read") {
		t.Fatalf("instructions 未说明读取方式：%q", ins)
	}
}

// stateDir 末尾带斜杠时不能拼出 "//skills/"。
func TestInstructionsSkillsPathNoDoubleSlash(t *testing.T) {
	if got := buildInstructions("/data/adb/novaai-mcp/"); strings.Contains(got, "//skills/") {
		t.Fatalf("路径出现双斜杠：%q", got)
	}
}

// 配置缺席时退回 DefaultStateDir，不能拼出裸 "/skills/"。
//
// 纯函数断言，不构造审计器 —— 否则会真的去创建 /data/adb/novaai-mcp。
func TestInstructionsUsesDefaultStateDir(t *testing.T) {
	s := &Server{cfg: &ServerConfig{}}
	got := s.stateDir()
	if got != config.DefaultStateDir {
		t.Fatalf("stateDir() = %q，期望 %q", got, config.DefaultStateDir)
	}
	if !strings.Contains(buildInstructions(got), config.DefaultStateDir+"/skills/") {
		t.Fatalf("instructions 未使用默认状态目录：%q", buildInstructions(got))
	}
}
