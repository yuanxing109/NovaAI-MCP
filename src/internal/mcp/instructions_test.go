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
func newInfoServer(t *testing.T, version, stateDir string) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.StateDir = stateDir
	cfg.ShellTimeoutSeconds = 5

	logger, err := audit.NewLogger(cfg)
	if err != nil {
		t.Fatalf("构造审计器失败: %v", err)
	}
	t.Cleanup(logger.Close)

	return &Server{cfg: &ServerConfig{
		Config: cfg, Registry: tools.NewRegistry(), Audit: logger,
		Deps:    &tools.Deps{Config: cfg, Audit: logger, StateDir: stateDir},
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
	s := newInfoServer(t, "9.9.9-test", "/tmp/nova-test")
	info := initializeInfo(t, s)["serverInfo"].(map[string]any)
	if got := info["version"]; got != "9.9.9-test" {
		t.Fatalf("serverInfo.version = %v，期望注入的 9.9.9-test", got)
	}
}

// 没有注入版本时退回常量，不能变成空字符串。
func TestInitializeVersionFallsBack(t *testing.T) {
	s := newInfoServer(t, "", "/tmp/nova-test")
	info := initializeInfo(t, s)["serverInfo"].(map[string]any)
	if got := info["version"]; got != ServerVersion {
		t.Fatalf("serverInfo.version = %v，期望退回常量 %s", got, ServerVersion)
	}
}

// instructions 必须给出技能目录：skills/*.md 没有工具入口，握手时不说，
// 客户端就无从知道它们存在（这正是它们随包分发却没人读的原因）。
func TestInstructionsPointAtSkillsDir(t *testing.T) {
	s := newInfoServer(t, "x", "/data/adb/novaai-mcp")
	ins, _ := initializeInfo(t, s)["instructions"].(string)
	if !strings.Contains(ins, "/data/adb/novaai-mcp/skills/") {
		t.Fatalf("instructions 未给出技能目录：%q", ins)
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

// stateDir 为空时用 DefaultStateDir，不能拼出裸 "/skills/"。
func TestInstructionsUsesDefaultStateDir(t *testing.T) {
	s := &Server{cfg: &ServerConfig{}}
	if got := s.stateDir(); got != config.DefaultStateDir {
		t.Fatalf("stateDir() = %q，期望 %q", got, config.DefaultStateDir)
	}
	if !strings.Contains(buildInstructions(s.stateDir()), config.DefaultStateDir+"/skills/") {
		t.Fatal("instructions 未使用默认状态目录")
	}
}
