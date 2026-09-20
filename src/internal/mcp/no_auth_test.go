package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/profile"
)

// 本文件锁住重构后的核心承诺：**无鉴权**。
//
// 所有来源一视同仁 —— loopback、局域网、unix socket 都不需要 token。
// 权限边界不再是"来源"，而是"网络可达性"：把 listen 改成 127.0.0.1:5322
// 就是本地模式，改回 0.0.0.0:5322 就重新对局域网开放。
//
// 唯一保留的两道来源校验是 Host 与 Origin —— 它们防的是浏览器
// DNS-rebinding，**不校验来源 IP**（见 docs/security.md）。

// middlewareProbe 只跑 hostMiddleware 一层，用最小 handler 观察
// "是否走到业务层"。
func middlewareProbe(t *testing.T) (http.Handler, *config.Config) {
	t.Helper()

	cfg := config.Default()
	cfg.StateDir = t.TempDir() // 审计写到临时目录

	lg, err := audit.NewLogger(cfg)
	if err != nil {
		t.Fatalf("构造审计器失败: %v", err)
	}
	t.Cleanup(lg.Close)

	mc := &MiddlewareConfig{Config: cfg, Audit: lg}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("reached"))
	})
	return hostMiddleware(mc, inner), cfg
}

// probeRequest 构造一次请求并返回"是否走到业务层"与错误信息。
//
// 断言方式很重要：中间件用 **JSON-RPC error envelope** 表达拒绝，
// HTTP 状态码一律 200（见 writeJSONRPCError）。因此**不能**用 w.Code
// 判断是否通过——那样连"被拒绝"都会看成"通过"。判定一律走 envelope。
func probeRequest(h http.Handler, remoteAddr, host, origin string) (reached bool, message string) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.RemoteAddr = remoteAddr
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	body := w.Body.String()
	if strings.HasPrefix(body, "{") {
		var env struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &env); err == nil && env.Error != nil {
			return false, env.Error.Message
		}
	}
	return true, ""
}

// 核心承诺：任何来源都不需要 token —— loopback 与局域网都要能到业务层。
func TestNoAuth_AnySourceAllowed(t *testing.T) {
	h, _ := middlewareProbe(t)

	for _, addr := range []string{
		"127.0.0.1:54211",
		"192.168.1.50:40000",
		"10.0.0.7:33512",
	} {
		reached, msg := probeRequest(h, addr, "127.0.0.1:5322", "")
		if !reached {
			t.Fatalf("来源 %s 应当免 token 通过，实际被拒: %s", addr, msg)
		}
	}
}

// domainHostProbe 是 hostMiddleware 对 Host 头的判定入口。
func domainHostProbe(t *testing.T) http.Handler {
	t.Helper()
	h, _ := middlewareProbe(t)
	return h
}

// 新增：Host 为域名时拒绝（防浏览器 DNS-rebinding）。
func TestHostRejectsDomain(t *testing.T) {
	h := domainHostProbe(t)

	for _, host := range []string{"evil.example.com", "attacker.local:5322", "localhost.evil.com"} {
		reached, msg := probeRequest(h, "127.0.0.1:54211", host, "")
		if reached {
			t.Errorf("Host=%q 应当被拒绝，实际通过", host)
		}
		if !strings.Contains(msg, "Host") {
			t.Errorf("Host=%q 的拒绝理由应点明 Host，实际: %s", host, msg)
		}
	}

	// 反例：IP 字面量与 localhost 必须放行。
	for _, host := range []string{"127.0.0.1:5322", "192.168.1.9:5322", "localhost:5322", "[::1]:5322"} {
		if reached, msg := probeRequest(h, "127.0.0.1:54211", host, ""); !reached {
			t.Errorf("Host=%q 应当放行，实际被拒: %s", host, msg)
		}
	}
}

// 新增：带 Origin 头时拒绝。
func TestOriginHeaderRejected(t *testing.T) {
	h := domainHostProbe(t)

	reached, msg := probeRequest(h, "127.0.0.1:54211", "127.0.0.1:5322", "https://evil.example.com")
	if reached {
		t.Fatal("带 Origin 头的请求应当被拒绝")
	}
	if !strings.Contains(msg, "Origin") {
		t.Errorf("拒绝理由应点明 Origin，实际: %s", msg)
	}
}

// 新增：default 档位不 deny 任何工具。
func TestDefaultProfileAllowsAll(t *testing.T) {
	cfg := config.Default()
	p := profile.NewStore().Get(cfg.Profile)
	if len(p.DenyTools) != 0 {
		t.Fatalf("default.denyTools 应为空，实际: %v", p.DenyTools)
	}
	for _, tool := range []string{
		"novaai_status", "novaai_shell", "novaai_script",
		"novaai_fs_write", "novaai_config", "novaai_root_module",
	} {
		if !profile.Allows(&p, tool, 0) {
			t.Errorf("default 不应拒绝 %s", tool)
		}
	}
}

// 保留/改名：default 档位含 shell。
//
// 免 token 只是通路；用户要的是"能跑命令"。这两件事必须一起断言。
func TestDefaultProfileAllowsShell(t *testing.T) {
	cfg := config.Default()
	p := profile.NewStore().Get(cfg.Profile)
	if !profile.Allows(&p, "novaai_shell", 3) {
		t.Fatalf("档位 %q 不允许 novaai_shell —— 与「装完即用、含 shell」的诉求不符", cfg.Profile)
	}
}
