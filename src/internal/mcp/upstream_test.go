package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/pathguard"
	"github.com/novaai/novaai-mcp/internal/profile"
	"github.com/novaai/novaai-mcp/internal/tools"
	"github.com/novaai/novaai-mcp/internal/upstream"
)

// 本文件验证**聚合网关**这一层的接线：tools/list 是否真的合并了上游工具、
// tools/call 是否真的按前缀转发、上游策略拒绝是否真的变成了 isError。
//
// 上游包自己的测试（internal/upstream）证明"连接、状态、路由正确"；
// 这里证明"它们被接进了协议层"。两者缺一不可 —— 单元测试全绿而
// server.go 忘了调用合并，工具面就是空的。
//
// 这里另起一个极简的假上游（不复用 upstream 包测试里的那个）：
// 那是 _test.go 里的私有实现，跨包不可见；把测试辅助代码导出到生产
// 包里则是更差的选择（测试期间才需要的协议实现会进入二进制）。

// fakeUpstream 起一个只实现四个方法的假上游。
//
// 返回的 closeFn 可以提前关掉它（用来造出 "stopped"），且可重复调用 ——
// httptest.Server.Close 自身幂等。
func fakeUpstream(t *testing.T) (url string, closeFn func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		reply := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID, "result": result,
			})
		}

		switch req.Method {
		case "initialize":
			reply(map[string]any{"protocolVersion": "2025-06-18",
				"capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fake"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			reply(map[string]any{"tools": []map[string]any{
				{"name": "echo", "description": "回显",
					"inputSchema": map[string]any{"type": "object"}},
				{"name": "novaai_status", "description": "与本地同名",
					"inputSchema": map[string]any{"type": "object"}},
			}})
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &p)
			reply(map[string]any{
				"content":           []map[string]any{{"type": "text", "text": "upstream-echo:" + p.Name}},
				"structuredContent": map[string]any{"handledBy": "upstream", "tool": p.Name},
			})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp", srv.Close
}

// newUpstreamServer 组装一个带上游的 Server。
func newUpstreamServer(t *testing.T, upstreamsCfg []config.UpstreamConfig) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.ShellTimeoutSeconds = 5
	cfg.Upstreams = upstreamsCfg
	pathguard.SetStateDir(cfg.StateDir)

	logger, err := audit.NewLogger(cfg)
	if err != nil {
		t.Fatalf("构造审计器失败: %v", err)
	}
	t.Cleanup(logger.Close)

	up := upstream.NewRegistry(cfg)
	up.SetAudit(logger)
	up.ProbeAll(context.Background())
	t.Cleanup(up.Close)

	reg := tools.NewRegistry()
	deps := &tools.Deps{
		Config: cfg, Audit: logger, Profiles: profile.NewStore(),
		Version: "test", Commit: "test", Upstreams: up,
	}
	tools.RegisterAll(reg, deps)

	return &Server{cfg: &ServerConfig{
		Config: cfg, Registry: reg, Audit: logger, Deps: deps, Upstreams: up,
	}}
}

// handleRaw 送一条请求，返回原始响应（需要看 isError 时用它）。
func handleRaw(t *testing.T, s *Server, body string) *JSONRPCResponse {
	t.Helper()
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp := s.Handle(&req, Identity{Profile: config.DefaultProfileName})
	if resp == nil {
		t.Fatal("Handle 返回 nil")
	}
	return resp
}

func listToolNames(t *testing.T, s *Server) []string {
	t.Helper()
	resp := handleRaw(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.Error != nil {
		t.Fatalf("tools/list 失败: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	var out struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析 tools/list 失败: %v", err)
	}
	names := make([]string, 0, len(out.Tools))
	for _, x := range out.Tools {
		names = append(names, x.Name)
	}
	return names
}

// ---- tools/list 合并 ----

func TestServerMergesUpstreamTools(t *testing.T) {
	url, _ := fakeUpstream(t)
	s := newUpstreamServer(t, []config.UpstreamConfig{{
		Name: "fake", Type: config.UpstreamTypeHTTP, URL: url, Enabled: true,
	}})

	names := listToolNames(t, s)
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}

	if !set["fake__echo"] {
		t.Errorf("缺少上游工具 fake__echo（实际 %v）", names)
	}
	if !set["fake__novaai_status"] {
		t.Errorf("缺少上游工具 fake__novaai_status（实际 %v）", names)
	}
	// 本地工具必须仍在，且**不**被上游的同名工具顶掉。
	if !set["novaai_status"] {
		t.Error("本地 novaai_status 不应消失")
	}
	if !set["novaai_shell"] {
		t.Error("本地 novaai_shell 不应消失")
	}
	// 本地数量不变：上游工具不进注册表。
	if got := s.cfg.Registry.Count(); got != 30 {
		t.Errorf("本地注册表 = %d，期望 30（上游工具不应计入）", got)
	}
}

// 没有上游时，tools/list 必须与只有本地工具时完全一致。
func TestServerNoUpstreamKeepsToolListUnchanged(t *testing.T) {
	s := newUpstreamServer(t, nil)
	if got := len(listToolNames(t, s)); got != 30 {
		t.Fatalf("工具数 = %d，期望 30", got)
	}
}

// ---- tools/call 路由 ----

func TestServerRoutesUpstreamCall(t *testing.T) {
	url, _ := fakeUpstream(t)
	s := newUpstreamServer(t, []config.UpstreamConfig{{
		Name: "fake", Type: config.UpstreamTypeHTTP, URL: url, Enabled: true,
	}})

	resp := handleRaw(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call",`+
			`"params":{"name":"fake__echo","arguments":{"x":1}}}`)
	if resp.Error != nil {
		t.Fatalf("不应是协议错误: %s", resp.Error.Message)
	}

	out, _ := json.Marshal(resp.Result)
	// 上游结果是 {content:[...], structuredContent} 形状，应当**原样透出**，
	// 而不是被当成普通对象再序列化进 content[0].text。
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured map[string]any `json:"structuredContent"`
		IsError    bool           `json:"isError"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("解析结果失败: %v（%s）", err, out)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "upstream-echo:echo") {
		t.Fatalf("content 不是上游的结果: %s", out)
	}
	if res.Structured["handledBy"] != "upstream" {
		t.Errorf("structuredContent 应原样透出，实际: %v", res.Structured)
	}
	if res.IsError {
		t.Error("成功的上游调用不应是 isError")
	}
}

func TestServerUpstreamPolicyDenied(t *testing.T) {
	url, _ := fakeUpstream(t)
	s := newUpstreamServer(t, []config.UpstreamConfig{{
		Name: "fake", Type: config.UpstreamTypeHTTP, URL: url, Enabled: true,
		DenyTools: []string{"echo"},
	}})

	resp := handleRaw(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call",`+
			`"params":{"name":"fake__echo","arguments":{}}}`)
	if resp.Error != nil {
		t.Fatalf("策略拒绝应是工具结果错误，而不是 JSON-RPC error: %s", resp.Error.Message)
	}
	out, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(out), `"isError":true`) {
		t.Fatalf("denyTools 命中时应返回 isError=true，实际: %s", out)
	}
	if !strings.Contains(string(out), "denyTools") {
		t.Errorf("错误信息应说明原因，实际: %s", out)
	}
}

func TestServerUpstreamStoppedIsErrorResult(t *testing.T) {
	url, closeUpstream := fakeUpstream(t)
	s := newUpstreamServer(t, []config.UpstreamConfig{{
		Name: "fake", Type: config.UpstreamTypeHTTP, URL: url, Enabled: true,
	}})

	// 让上游消失，再重探。
	s.cfg.Upstreams.ProbeAll(context.Background()) // 先确认可运行
	closeUpstream()
	s.cfg.Upstreams.ProbeAll(context.Background())

	resp := handleRaw(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call",`+
			`"params":{"name":"fake__echo","arguments":{}}}`)
	if resp.Error != nil {
		t.Fatalf("应为工具结果错误: %s", resp.Error.Message)
	}
	out, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(out), `"isError":true`) {
		t.Fatalf("上游不可用时应返回 isError=true，实际: %s", out)
	}
}

// 前缀对不上任何已注册上游时，仍然是"工具不存在"，不能瞎猜路由。
func TestServerUnknownUpstreamPrefixIsToolNotFound(t *testing.T) {
	url, _ := fakeUpstream(t)
	s := newUpstreamServer(t, []config.UpstreamConfig{{
		Name: "fake", Type: config.UpstreamTypeHTTP, URL: url, Enabled: true,
	}})

	for _, name := range []string{"zzz__echo", "fake_echo", "fake__", "__echo"} {
		resp := handleRaw(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`"}}`)
		if resp.Error == nil || resp.Error.Code != -32015 {
			t.Errorf("%q 应返回 -32015，实际: %+v", name, resp)
		}
	}
}

// ---- novaai_upstream_status ----

func TestServerUpstreamStatusTool(t *testing.T) {
	url, _ := fakeUpstream(t)
	s := newUpstreamServer(t, []config.UpstreamConfig{{
		Name: "fake", Type: config.UpstreamTypeHTTP, URL: url, Enabled: true,
	}})

	out := callTool(t, s, "novaai_upstream_status", map[string]any{})
	if !strings.Contains(out, `"fake"`) || !strings.Contains(out, "running") {
		t.Fatalf("状态输出异常: %s", out)
	}
	if !strings.Contains(out, `"tools":2`) {
		t.Errorf("应报告 2 个上游工具，实际: %s", out)
	}
}

// 没装配上游时必须返回空列表而不是报错 —— "没有上游"是合法状态。
func TestServerUpstreamStatusWithoutRegistry(t *testing.T) {
	s := newTestServer(t) // 这个 helper 不注入 Upstreams
	out := callTool(t, s, "novaai_upstream_status", map[string]any{})
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("应成功返回: %s", out)
	}
	if !strings.Contains(out, `"upstreams":[]`) {
		t.Errorf("应返回空数组: %s", out)
	}
}
