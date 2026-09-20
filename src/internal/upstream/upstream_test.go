package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/novaai/novaai-mcp/internal/config"
)

// testConfig 造一个上游测试用的完整配置。
//
// shellTimeoutSeconds 压到 5 秒是有意的：它同时是上游探测/调用的超时，
// 默认 60 会让每个"故意失败"的用例把测试拖到分钟级。
func testConfig(ups ...config.UpstreamConfig) *config.Config {
	cfg := config.Default()
	cfg.ShellTimeoutSeconds = 5
	cfg.Upstreams = ups
	return cfg
}

func httpUpstream(name, url string, mut ...func(*config.UpstreamConfig)) config.UpstreamConfig {
	u := config.UpstreamConfig{
		Name: name, Type: config.UpstreamTypeHTTP, URL: url, Enabled: true,
	}
	for _, m := range mut {
		m(&u)
	}
	return u
}

func stdioUpstream(name string, mut ...func(*config.UpstreamConfig)) config.UpstreamConfig {
	u := config.UpstreamConfig{
		Name: name, Type: config.UpstreamTypeStdio,
		Command: selfExe(), Args: []string{"--nova-fake-stdio"}, Enabled: true,
	}
	for _, m := range mut {
		m(&u)
	}
	return u
}

func statusOf(t *testing.T, r *Registry, name string) StatusInfo {
	t.Helper()
	for _, s := range r.Status() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("状态里没有上游 %s", name)
	return StatusInfo{}
}

func toolNameSet(tools []Tool) map[string]bool {
	out := map[string]bool{}
	for _, x := range tools {
		out[x.Name] = true
	}
	return out
}

// ---------------------------------------------------------------- 连接

func TestUpstream_HTTPConnect(t *testing.T) {
	_, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url)))
	defer r.Close()

	r.ProbeAll(context.Background())

	st := statusOf(t, r, "fake")
	if st.Status != config.UpstreamRunning {
		t.Fatalf("状态 = %q（%s），期望 running", st.Status, st.Reason)
	}
	if st.Tools != 2 {
		t.Fatalf("工具数 = %d，期望 2", st.Tools)
	}
	if st.Type != config.UpstreamTypeHTTP {
		t.Errorf("类型 = %q", st.Type)
	}
}

func TestUpstream_StdioSpawn(t *testing.T) {
	r := NewRegistry(testConfig(stdioUpstream("local")))
	defer r.Close()

	r.ProbeAll(context.Background())

	st := statusOf(t, r, "local")
	if st.Status != config.UpstreamRunning {
		t.Fatalf("状态 = %q（%s），期望 running", st.Status, st.Reason)
	}
	if st.Tools != 2 {
		t.Fatalf("工具数 = %d，期望 2", st.Tools)
	}

	// stdio 上游必须真的持有子进程 —— 这是与 HTTP 的本质差别。
	r.mu.RLock()
	tr := r.entries["local"].tr
	r.mu.RUnlock()
	sx, ok := tr.(*stdioTransport)
	if !ok {
		t.Fatalf("连接类型 = %T，期望 *stdioTransport", tr)
	}
	if !sx.alive() {
		t.Fatal("子进程应处于存活状态")
	}
}

// ---------------------------------------------------- 命名空间与合并

func TestUpstream_ToolNamespace(t *testing.T) {
	if got := FullName("a", "b"); got != "a__b" {
		t.Fatalf("FullName = %q", got)
	}

	_, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url)))
	defer r.Close()
	r.ProbeAll(context.Background())

	names := toolNameSet(r.MergedTools())
	for _, want := range []string{"fake__echo", "fake__novaai_status"} {
		if !names[want] {
			t.Errorf("合并后的工具里缺少 %s（实际 %v）", want, names)
		}
	}
}

func TestUpstream_RouteByPrefix(t *testing.T) {
	_, urlA := startFakeHTTP(t)
	_, urlB := startFakeHTTP(t)
	r := NewRegistry(testConfig(
		httpUpstream("a", urlA),
		httpUpstream("a_b", urlB),
	))
	defer r.Close()
	r.ProbeAll(context.Background())

	cases := []struct {
		full string
		up   string
		tool string
		ok   bool
	}{
		{"a__echo", "a", "echo", true},
		{"a_b__echo", "a_b", "echo", true},
		{"a_bb__echo", "", "", false}, // 不是任何已注册上游的前缀
		{"novaai_status", "", "", false},
		{"zzz__echo", "", "", false},
		// 空工具名不是合法路由目标 —— 否则会一路转发给上游一个不存在的名字。
		{"a__", "", "", false},
		{"a_b__", "", "", false},
	}
	for _, c := range cases {
		up, tool, ok := r.SplitName(c.full)
		if ok != c.ok || up != c.up || tool != c.tool {
			t.Errorf("SplitName(%q) = (%q,%q,%v)，期望 (%q,%q,%v)",
				c.full, up, tool, ok, c.up, c.tool, c.ok)
		}
	}
}

// 最长前缀优先：当 "a" 与 "a_b" 同时存在时，`a_b__x` 必须归 a_b。
func TestUpstream_RouteLongestPrefixWins(t *testing.T) {
	_, urlA := startFakeHTTP(t)
	_, urlB := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("a", urlA), httpUpstream("a_b", urlB)))
	defer r.Close()

	up, tool, ok := r.SplitName("a_b__echo")
	if !ok || up != "a_b" || tool != "echo" {
		t.Fatalf("got (%q,%q,%v)，期望 a_b/echo", up, tool, ok)
	}
}

func TestUpstream_MergeToolList(t *testing.T) {
	_, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url)))
	defer r.Close()

	// 未探测前不应暴露任何上游工具：不知道上游有什么，就不能凭空编造 schema。
	if got := r.MergedTools(); len(got) != 0 {
		t.Fatalf("探测前暴露了 %d 个工具，期望 0", len(got))
	}

	r.ProbeAll(context.Background())
	if got := len(r.MergedTools()); got != 2 {
		t.Fatalf("合并后 %d 个，期望 2", got)
	}
	if got := r.ToolCount(); got != 2 {
		t.Fatalf("ToolCount = %d，期望 2", got)
	}
}

// 上游工具与本地同名时，前缀隔离保证两者并存、互不覆盖。
func TestUpstream_ConflictResolution(t *testing.T) {
	_, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url)))
	defer r.Close()
	r.ProbeAll(context.Background())

	names := toolNameSet(r.MergedTools())
	if !names["fake__novaai_status"] {
		t.Error("上游的同名工具应以 fake__novaai_status 出现")
	}
	if names["novaai_status"] {
		t.Error("上游工具不应以裸名 novaai_status 出现在合并结果里")
	}

	// 而且它必须仍然能被路由到上游，而不是被本地工具截胡。
	up, tool, ok := r.SplitName("fake__novaai_status")
	if !ok || up != "fake" || tool != "novaai_status" {
		t.Fatalf("路由失败: (%q,%q,%v)", up, tool, ok)
	}
}

// ------------------------------------------------------------ 调用转发

func TestUpstream_CallForwards(t *testing.T) {
	_, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url)))
	defer r.Close()
	r.ProbeAll(context.Background())

	out, err := r.Call(context.Background(), "fake", "echo",
		json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if len(out.Content) == 0 {
		t.Fatal("返回的 content 为空")
	}
	if !strings.Contains(out.Content[0].Text, `"hello":"world"`) {
		t.Fatalf("参数没有送到上游: %s", out.Content[0].Text)
	}
}

// 上游回 JSON-RPC error 时应作为调用失败暴露，而不是当成成功。
func TestUpstream_CallPropagatesUpstreamError(t *testing.T) {
	_, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url)))
	defer r.Close()
	r.ProbeAll(context.Background())

	_, err := r.Call(context.Background(), "fake", "boom", nil)
	if err == nil {
		t.Fatal("上游报错时 Call 应返回 error")
	}
	if !strings.Contains(err.Error(), "假上游故意报错") {
		t.Errorf("错误信息应带上游原因，实际: %v", err)
	}
}

func TestUpstream_DisconnectGraceful(t *testing.T) {
	_, goodURL := startFakeHTTP(t)
	dead := freePort(t)
	r := NewRegistry(testConfig(
		httpUpstream("good", goodURL),
		httpUpstream("bad", "http://127.0.0.1:"+itoa(dead)+"/mcp"),
	))
	defer r.Close()
	r.ProbeAll(context.Background())

	if st := statusOf(t, r, "good"); st.Status != config.UpstreamRunning {
		t.Fatalf("good = %q（%s）", st.Status, st.Reason)
	}
	if st := statusOf(t, r, "bad"); st.Status != config.UpstreamStopped {
		t.Fatalf("bad = %q（%s），期望 stopped", st.Status, st.Reason)
	}

	// 一个上游不可达不能影响另一个。
	if _, err := r.Call(context.Background(), "good", "echo", nil); err != nil {
		t.Fatalf("健康上游的调用被拖累: %v", err)
	}
	if _, err := r.Call(context.Background(), "bad", "echo", nil); err == nil {
		t.Fatal("不可达上游的调用应当失败")
	}
}

// -------------------------------------------------------- 超时与进程回收

func TestUpstream_StdioTimeoutKillsProcess(t *testing.T) {
	r := NewRegistry(testConfig(stdioUpstream("local")))
	defer r.Close()
	r.ProbeAll(context.Background())

	r.mu.RLock()
	sx := r.entries["local"].tr.(*stdioTransport)
	r.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.Call(ctx, "local", "hang", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("调用挂住的上游工具应当超时失败")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("超时耗时 %v，超时控制没有生效", elapsed)
	}

	// 超时后必须回收整个进程组，否则会留着孤儿占端口。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !sx.alive() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("超时后子进程仍然存活 —— 进程组没有回收")
}

// ------------------------------------------------------------ 状态模型

func TestUpstream_ProbeStatus(t *testing.T) {
	srv, url := startFakeHTTP(t)
	deadPort := freePort(t)

	// 一个返回 500 的"协议异常"上游。
	bad := newStatusServer(t, http.StatusInternalServerError)
	r := NewRegistry(testConfig(
		httpUpstream("running", url),
		// stopped：端口上没有东西在监听
		httpUpstream("stopped", "http://127.0.0.1:"+itoa(deadPort)+"/mcp"),
		// error：HTTP 非 2xx
		httpUpstream("broken", bad),
		// disabled：不探测
		httpUpstream("off", url, func(u *config.UpstreamConfig) { u.Enabled = false }),
	))
	defer r.Close()
	r.ProbeAll(context.Background())

	want := map[string]string{
		"running": config.UpstreamRunning,
		"stopped": config.UpstreamStopped,
		"broken":  config.UpstreamError,
		"off":     config.UpstreamDisabled,
	}
	for name, exp := range want {
		if st := statusOf(t, r, name); st.Status != exp {
			t.Errorf("%s 状态 = %q（%s），期望 %q", name, st.Status, st.Reason, exp)
		}
	}

	// disabled 的上游即使可达也不暴露工具。
	names := toolNameSet(r.MergedTools())
	if names["off__echo"] {
		t.Error("disabled 的上游不应暴露工具")
	}

	srv.Close() // 让 running 的那个变成 stopped
	r.ProbeAll(context.Background())
	if st := statusOf(t, r, "running"); st.Status != config.UpstreamStopped {
		t.Errorf("服务关闭后状态 = %q，期望 stopped", st.Status)
	}
}

func TestUpstream_StoppedNotExposed(t *testing.T) {
	srv, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url)))
	defer r.Close()

	r.ProbeAll(context.Background())
	if len(r.MergedTools()) != 2 {
		t.Fatal("前置条件失败：运行中应有 2 个工具")
	}

	srv.Close()
	r.ProbeAll(context.Background())

	if got := r.MergedTools(); len(got) != 0 {
		t.Fatalf("未运行且未开 exposeWhenStopped 时暴露了 %d 个工具", len(got))
	}
}

func TestUpstream_ExposeWhenStopped(t *testing.T) {
	srv, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url,
		func(u *config.UpstreamConfig) { u.ExposeWhenStopped = true })))
	defer r.Close()

	r.ProbeAll(context.Background())
	srv.Close()
	r.ProbeAll(context.Background())

	// 暴露的是**上一次成功探测**拿到的列表。
	names := toolNameSet(r.MergedTools())
	if !names["fake__echo"] {
		t.Fatalf("exposeWhenStopped=true 时仍应暴露工具，实际 %v", names)
	}

	// 但调用必须失败，并说清是"未运行"。
	_, err := r.Call(context.Background(), "fake", "echo", nil)
	if err == nil {
		t.Fatal("未运行的上游调用应当失败")
	}
	if !strings.Contains(err.Error(), "未运行") {
		t.Errorf("错误信息应说明未运行，实际: %v", err)
	}
}

func TestUpstream_DisabledCallRefused(t *testing.T) {
	_, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url,
		func(u *config.UpstreamConfig) { u.Enabled = false })))
	defer r.Close()
	r.ProbeAll(context.Background())

	_, err := r.Call(context.Background(), "fake", "echo", nil)
	if err == nil || !strings.Contains(err.Error(), "已禁用") {
		t.Fatalf("期望「已禁用」，实际: %v", err)
	}
}

// ---------------------------------------------------------------- 懒启动

func TestUpstream_AutoLaunch(t *testing.T) {
	port := freePort(t)
	url := "http://127.0.0.1:" + itoa(port) + "/mcp"
	// 子进程不由本服务托管，测试必须自己让它退出（见 stopFakeUpstream）。
	stopFakeUpstream(t, port)

	cfg := testConfig(config.UpstreamConfig{
		Name: "lazy", Type: config.UpstreamTypeHTTP, URL: url, Enabled: true,
		AutoLaunch: true,
		Launch: &config.LaunchConfig{
			Type:    config.LaunchCommand,
			Command: selfExe(),
			Args:    []string{"--nova-fake-http=" + itoa(port)},
		},
	})
	r := NewRegistry(cfg)
	defer r.Close()

	r.ProbeAll(context.Background())
	if st := statusOf(t, r, "lazy"); st.Status != config.UpstreamStopped {
		t.Fatalf("前置条件失败：应为 stopped，实际 %q（%s）", st.Status, st.Reason)
	}

	out, err := r.Call(context.Background(), "lazy", "echo", json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatalf("autoLaunch 后调用应成功: %v", err)
	}
	if len(out.Content) == 0 || !strings.Contains(out.Content[0].Text, `"n":1`) {
		t.Fatalf("调用结果异常: %+v", out)
	}
	if st := statusOf(t, r, "lazy"); st.Status != config.UpstreamRunning {
		t.Fatalf("拉起后状态 = %q，期望 running", st.Status)
	}
}

// 没配 autoLaunch 时，调用一个 stopped 上游应立刻失败并提示手动启动。
func TestUpstream_StoppedWithoutAutoLaunch(t *testing.T) {
	deadPort := freePort(t)
	r := NewRegistry(testConfig(httpUpstream("fake",
		"http://127.0.0.1:"+itoa(deadPort)+"/mcp")))
	defer r.Close()
	r.ProbeAll(context.Background())

	_, err := r.Call(context.Background(), "fake", "echo", nil)
	if err == nil {
		t.Fatal("应当失败")
	}
	if !strings.Contains(err.Error(), "手动启动") {
		t.Errorf("应提示手动启动，实际: %v", err)
	}
}

// ---------------------------------------------------------------- 热重载

func TestUpstream_HotReload(t *testing.T) {
	_, urlA := startFakeHTTP(t)
	_, urlB := startFakeHTTP(t)

	cfg := testConfig(httpUpstream("a", urlA))
	r := NewRegistry(cfg)
	defer r.Close()
	r.ProbeAll(context.Background())
	if got := len(r.MergedTools()); got != 2 {
		t.Fatalf("前置条件失败：%d 个工具", got)
	}

	// 加一个上游并重载。
	cfg2 := testConfig(httpUpstream("a", urlA), httpUpstream("b", urlB))
	r.Reload(cfg2)
	r.ProbeAll(context.Background())

	if r.Count() != 2 {
		t.Fatalf("重载后上游数 = %d，期望 2", r.Count())
	}
	names := toolNameSet(r.MergedTools())
	if !names["a__echo"] || !names["b__echo"] {
		t.Fatalf("重载后工具不完整: %v", names)
	}

	// 移除一个上游并重载。
	cfg3 := testConfig(httpUpstream("b", urlB))
	r.Reload(cfg3)
	r.ProbeAll(context.Background())

	names = toolNameSet(r.MergedTools())
	if names["a__echo"] {
		t.Fatalf("被移除的上游仍在工具列表里: %v", names)
	}
	if r.Has("a") {
		t.Fatal("被移除的上游仍在注册表里")
	}
}

// Reload 把 stdio 上游关掉时必须真的回收子进程。
func TestUpstream_ReloadReapsStdioChild(t *testing.T) {
	r := NewRegistry(testConfig(stdioUpstream("local")))
	r.ProbeAll(context.Background())

	r.mu.RLock()
	sx := r.entries["local"].tr.(*stdioTransport)
	r.mu.RUnlock()
	if !sx.alive() {
		t.Fatal("前置条件失败：子进程应存活")
	}

	r.Reload(testConfig()) // 空配置：所有上游消失
	defer r.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !sx.alive() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("重载后子进程仍存活")
}

// ------------------------------------------------------------ 上游策略

func TestUpstream_DenyTools(t *testing.T) {
	_, url := startFakeHTTP(t)
	r := NewRegistry(testConfig(httpUpstream("fake", url, func(u *config.UpstreamConfig) {
		u.DenyTools = []string{"echo", "fake__novaai_status"}
	})))
	defer r.Close()
	r.ProbeAll(context.Background())

	if _, err := r.Authorize("fake", "echo", nil); err == nil {
		t.Error("denyTools 里的裸名应被拒绝")
	}
	if _, err := r.Authorize("fake", "novaai_status", nil); err == nil {
		t.Error("denyTools 里的带前缀名应被拒绝")
	}
	if _, err := r.Authorize("fake", "other", nil); err != nil {
		t.Errorf("未列入 denyTools 的工具不应被拒: %v", err)
	}
}

func TestUpstream_RiskCeiling(t *testing.T) {
	// Authorize 只读配置，不需要上游真的在跑。
	dummy := "http://127.0.0.1:1/mcp"

	r := NewRegistry(testConfig(httpUpstream("fake", dummy,
		func(u *config.UpstreamConfig) { u.RiskCeiling = 1 })))
	defer r.Close()

	// novaai_shell 在本地风险表里是 2，超过 ceiling 1 → 拒绝。
	if _, err := r.Authorize("fake", "novaai_shell", nil); err == nil {
		t.Error("risk 2 超过 ceiling 1 时应拒绝")
	}
	// 未知工具名的推断风险是 1（profile.ResolveRisk 的兜底），等于 ceiling → 放行。
	if _, err := r.Authorize("fake", "unknown_tool", nil); err != nil {
		t.Errorf("risk 1 未超过 ceiling 1，应放行: %v", err)
	}

	// riskCeiling 缺省（0）表示继承默认 3。
	r.Reload(testConfig(httpUpstream("fake", dummy)))
	if _, err := r.Authorize("fake", "novaai_shell", nil); err != nil {
		t.Errorf("riskCeiling=0 应继承默认 3，不应拒绝: %v", err)
	}
}

func TestUpstream_AuthorizeUnknownUpstream(t *testing.T) {
	r := NewRegistry(testConfig())
	defer r.Close()
	if _, err := r.Authorize("nope", "x", nil); err == nil {
		t.Fatal("未知上游应报错")
	}
}

// ---------------------------------------------------------------- 工具函数

func itoa(n int) string { return strconv.Itoa(n) }

// newStatusServer 起一个总是返回指定状态码的假上游，返回它的 MCP URL。
func newStatusServer(t *testing.T, code int) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte("nope"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp"
}
