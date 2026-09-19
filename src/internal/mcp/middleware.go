package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/auth"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/profile"
	"github.com/novaai/novaai-mcp/internal/ratelimit"
	"github.com/novaai/novaai-mcp/internal/session"
	"github.com/novaai/novaai-mcp/internal/tools"
)

type MiddlewareConfig struct {
	Config    *config.Config
	Audit     *audit.Logger
	Sessions  *session.Manager
	RateLimit *ratelimit.Limiter
	Registry  *tools.Registry
	Profiles  *profile.Store
}

type ctxKey string

// ctxProfileKey 承载"本次请求鉴权后解析出的 profile 名"。
const ctxProfileKey ctxKey = "novaai.profile"

// profileFromContext 取出鉴权阶段解析出的 profile 名。
func profileFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxProfileKey).(string); ok && v != "" {
		return v
	}
	return "default"
}

// withProfile 把本次请求对应的 profile 名放进 context。
//
// token 为空（unix socket / 匿名 loopback）时按 sessionBinding.fallback 解析，
// 这样无 token 入口也有确定的、可配置的权限归属，而不是隐式全权。
func withProfile(r *http.Request, mc *MiddlewareConfig, token string) *http.Request {
	name := "default"
	if mc.Profiles != nil {
		hash := ""
		if token != "" {
			hash = auth.HashToken(token)
		}
		name = mc.Profiles.ResolveByTokenHash(hash)
	}
	return r.WithContext(context.WithValue(r.Context(), ctxProfileKey, name))
}

func peerInfo(r *http.Request) (kind string, ip string) {
	ra := r.RemoteAddr
	if ra == "" || strings.HasPrefix(ra, "@") || strings.HasPrefix(ra, "/") {
		return "unix", ""
	}
	host, _, err := net.SplitHostPort(ra)
	if err != nil {
		return "unknown", ""
	}
	return "tcp", host
}

// BuildMiddlewareChain 组装请求处理链。
//
// 顺序（外 → 内）：host/origin 校验 → LAN 限制 → 鉴权 → 业务处理。
// Host 校验必须最先做：DNS rebinding 攻击在拿到响应前就该被拒。
func BuildMiddlewareChain(server *Server, mc *MiddlewareConfig) http.Handler {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}

		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST, OPTIONS")
			writeJSONRPCError(w, nil, -32600, "仅支持 POST /mcp", nil)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, mc.Config.Limits.MaxRequestBytes))
		if err != nil {
			writeJSONRPCError(w, nil, -32700, "请求体读取失败", nil)
			return
		}

		sessionID := r.Header.Get("Mcp-Session-Id")
		profName := profileFromContext(r.Context())

		// 会话：不存在或已过期就新建，并把新 ID 回给客户端
		var sess *session.State
		if sessionID != "" {
			if s, err := mc.Sessions.Get(sessionID); err == nil {
				sess = s
				mc.Sessions.Touch(sessionID)
				// profile 跟随本次鉴权结果，不继承上一次（防权限混淆）
				mc.Sessions.SetProfile(sess.ID, profName)
			}
		}
		if sess == nil {
			newSess, err := mc.Sessions.Create(profName, -1)
			if err != nil {
				writeJSONRPCError(w, nil, -32014, "会话创建失败", nil)
				return
			}
			sess = newSess
			// 新会话建立时顺手回收限流器里已失效会话的桶。
			// 限流器的 perSession/perTool 表是按会话 ID 惰性增长的，
			// 会话本身会被 sweep 清理，但限流表不会，长期运行会持续膨胀。
			if mc.RateLimit != nil {
				active := make(map[string]bool)
				for _, st := range mc.Sessions.List() {
					active[st.ID] = true
				}
				mc.RateLimit.Sweep(active)
			}
		}
		w.Header().Set("Mcp-Session-Id", sess.ID)

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 {
			writeJSONRPCError(w, nil, -32700, "空请求体", nil)
			return
		}

		// ---- JSON-RPC 批量请求 ----
		if trimmed[0] == '[' {
			var batch []JSONRPCRequest
			if err := json.Unmarshal(trimmed, &batch); err != nil {
				writeJSONRPCError(w, nil, -32700, "JSON 解析失败", nil)
				return
			}
			if len(batch) == 0 {
				writeJSONRPCError(w, nil, -32600, "空的批量请求", nil)
				return
			}
			out := make([]*JSONRPCResponse, 0, len(batch))
			for i := range batch {
				if resp := server.Handle(&batch[i], sess.ID); resp != nil {
					out = append(out, resp)
				}
			}
			if len(out) == 0 {
				// 整批都是通知：按规范不回响应体
				writeNoContent(w)
				return
			}
			writeJSON(w, out)
			return
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(trimmed, &req); err != nil {
			writeJSONRPCError(w, nil, -32700, "JSON 解析失败", nil)
			return
		}

		resp := server.Handle(&req, sess.ID)
		if resp == nil {
			writeNoContent(w)
			return
		}
		writeJSON(w, resp)
	})

	return authMiddleware(mc, lanMiddleware(mc, hostMiddleware(mc, handler)))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeNoContent 用于通知类请求：规范要求没有响应体。
func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusAccepted)
}

// hostAllowed 判断 Host 头是否可信。
//
// 只接受 IP 字面量与 localhost。攻击者可以把自己的域名解析到 127.0.0.1
// 来绕过 loopback 判定（DNS rebinding），但 Host 头会暴露域名，因此这里拒绝。
func hostAllowed(r *http.Request, cfg *config.Config) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, o := range cfg.Network.AllowedOrigins {
		if u, err := url.Parse(o); err == nil && strings.EqualFold(u.Hostname(), host) {
			return true
		}
	}
	return false
}

// originAllowed 判断浏览器 Origin 是否在白名单内。
// 没有 Origin 头说明不是浏览器发起的（原生客户端），放行。
func originAllowed(r *http.Request, cfg *config.Config) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, o := range cfg.Network.AllowedOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

func hostMiddleware(mc *MiddlewareConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, host := peerInfo(r)
		// unix socket 没有 Host/Origin 语义，且只有 root 能连
		if kind == "unix" {
			next.ServeHTTP(w, r)
			return
		}

		if mc.Config.Security.AllowCORS {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		if mc.Config.Security.ValidateHost && !hostAllowed(r, mc.Config) {
			mc.Audit.Log(audit.Entry{
				Event:  "host_rejected",
				Peer:   audit.PeerInfo{Type: kind, IP: host},
				Detail: "Host=" + r.Host,
			})
			writeJSONRPCError(w, nil, -32001, "Host 头不被信任", nil)
			return
		}

		if mc.Config.Security.ValidateOrigin && !originAllowed(r, mc.Config) {
			mc.Audit.Log(audit.Entry{
				Event:  "origin_rejected",
				Peer:   audit.PeerInfo{Type: kind, IP: host},
				Detail: "Origin=" + r.Header.Get("Origin"),
			})
			writeJSONRPCError(w, nil, -32001, "Origin 不在允许列表", nil)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func lanMiddleware(mc *MiddlewareConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, host := peerInfo(r)
		if kind == "unix" {
			next.ServeHTTP(w, r)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.IsLoopback() {
			next.ServeHTTP(w, r)
			return
		}
		if !mc.Config.Security.LAN.Enabled {
			writeJSONRPCError(w, nil, -32001, "LAN 访问已关闭", nil)
			return
		}
		allowed := false
		for _, cidr := range mc.Config.Security.LAN.AllowedCIDR {
			if _, ipnet, err := net.ParseCIDR(cidr); err == nil {
				if ipnet.Contains(ip) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			writeJSONRPCError(w, nil, -32001, "源 IP 不在允许范围", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authMiddleware(mc *MiddlewareConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, host := peerInfo(r)

		// unix socket 文件权限即鉴权（默认 0660 且未 chgrp，仅 root 可连）
		if kind == "unix" {
			next.ServeHTTP(w, withProfile(r, mc, ""))
			return
		}

		ip := net.ParseIP(host)
		isLoopback := ip != nil && ip.IsLoopback()

		// loopback 在 anonymous=true 时免 token；默认 false，即本机也要 token。
		// 这是必要的：设备上任何 App 都能访问 127.0.0.1，而本服务有 root 能力。
		if isLoopback && mc.Config.Security.Anonymous {
			next.ServeHTTP(w, withProfile(r, mc, ""))
			return
		}

		if !mc.Config.Security.Token.Enabled {
			if isLoopback {
				// token 关闭且未开匿名：拒绝，避免静默裸奔
				writeJSONRPCError(w, nil, -32001, "token 未启用且 anonymous 为 false，拒绝访问", nil)
				return
			}
			writeJSONRPCError(w, nil, -32001, "token 未启用，禁止非 loopback 访问", nil)
			return
		}

		token := auth.ExtractBearer(r.Header.Get("Authorization"))
		if token == "" && mc.Config.Security.Token.AllowQueryParam {
			token = r.URL.Query().Get("token")
		}
		if !auth.VerifyToken(token, mc.Config.Security.Token.Value) {
			mc.Audit.Log(audit.Entry{
				Event:  "auth_fail",
				Peer:   audit.PeerInfo{Type: kind, IP: host},
				Detail: "invalid token",
			})
			writeJSONRPCError(w, nil, -32001, "鉴权失败", nil)
			return
		}
		next.ServeHTTP(w, withProfile(r, mc, token))
	})
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string, data any) {
	writeJSON(w, JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
			Data:    data,
		},
	})
}
