package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ctxRateKeyKey 承载"本次请求的限流身份键"。
const ctxRateKeyKey ctxKey = "novaai.ratekey"

// profileFromContext 取出鉴权阶段解析出的 profile 名。
func profileFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxProfileKey).(string); ok && v != "" {
		return v
	}
	return "default"
}

// rateKeyFromContext 取出限流身份键。缺失时退化为本地身份，不会放行成"无限额"。
func rateKeyFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRateKeyKey).(string); ok && v != "" {
		return v
	}
	return localRateKey
}

// localRateKey 是 unix socket 与匿名 loopback 共用的限流身份。
//
// 这两类入口都没有 token，但都已经是"本机特权入口"（unix socket 仅 root
// 可连，匿名 loopback 需要显式打开 security.anonymous），共用一个桶是合理的。
const localRateKey = "local"

// withIdentity 把本次请求对应的 profile 名与限流身份键放进 context。
//
// token 为空（unix socket / 匿名 loopback）时按 sessionBinding.fallback 解析，
// 这样无 token 入口也有确定的、可配置的权限归属，而不是隐式全权。
//
// 限流键取 token 哈希而不是 Mcp-Session-Id：后者是客户端自报的请求头，
// 换一个就能拿到全新的满额桶。
func withIdentity(r *http.Request, mc *MiddlewareConfig, token string) *http.Request {
	name := "default"
	rateKey := localRateKey
	if mc.Profiles != nil {
		hash := ""
		if token != "" {
			hash = auth.HashToken(token)
			rateKey = "tok:" + hash
		}
		name = mc.Profiles.ResolveByTokenHash(hash)
	}
	ctx := context.WithValue(r.Context(), ctxProfileKey, name)
	ctx = context.WithValue(ctx, ctxRateKeyKey, rateKey)
	return r.WithContext(ctx)
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

		// 用 MaxBytesReader 而不是 io.LimitReader：后者静默截断，
		// 超限会伪装成 JSON 解析失败（-32700），把客户端引向"JSON 写错了"
		// 这个错误方向。MaxBytesReader 会返回可识别的 *http.MaxBytesError。
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mc.Config.Limits.MaxRequestBytes))
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeJSONRPCError(w, nil, -32600,
					fmt.Sprintf("请求体超过上限 %d 字节", mbe.Limit), nil)
				return
			}
			writeJSONRPCError(w, nil, -32700, "请求体读取失败", nil)
			return
		}

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 {
			writeJSONRPCError(w, nil, -32700, "空请求体", nil)
			return
		}

		// 先解析再决定会话：要不要登记会话取决于方法名（见下）。
		batched := trimmed[0] == '['
		var reqs []JSONRPCRequest
		if batched {
			if err := json.Unmarshal(trimmed, &reqs); err != nil {
				writeJSONRPCError(w, nil, -32700, "JSON 解析失败", nil)
				return
			}
			if len(reqs) == 0 {
				writeJSONRPCError(w, nil, -32600, "空的批量请求", nil)
				return
			}
		} else {
			var one JSONRPCRequest
			if err := json.Unmarshal(trimmed, &one); err != nil {
				writeJSONRPCError(w, nil, -32700, "JSON 解析失败", nil)
				return
			}
			reqs = []JSONRPCRequest{one}
		}

		profName := profileFromContext(r.Context())
		rateKey := rateKeyFromContext(r.Context())

		// ---- 会话登记 ----
		//
		// 只有两种情况占用会话名额：
		//   1. 客户端带了有效的 Mcp-Session-Id；
		//   2. 本次请求包含 initialize。
		// 其余走无状态路径（sessionID 为空，不登记）。这是必要的：
		// 不实现会话的客户端如果每个请求都新建会话，会在 MaxSessions
		// 处撞墙并持续收到 -32014，直到空闲超时把它们清掉。
		sessionID := ""
		if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
			if _, err := mc.Sessions.Get(sid); err == nil {
				sessionID = sid
				mc.Sessions.Touch(sid)
				// profile 跟随本次鉴权结果，不继承上一次（防权限混淆）
				mc.Sessions.SetProfile(sid, profName)
			}
		}
		if sessionID == "" && containsInitialize(reqs) {
			sess, err := mc.Sessions.Create(profName)
			if err != nil {
				writeJSONRPCError(w, nil, -32014, "会话创建失败", nil)
				return
			}
			sessionID = sess.ID
		}
		if sessionID != "" {
			w.Header().Set("Mcp-Session-Id", sessionID)
		}

		ident := Identity{
			SessionID: sessionID,
			Profile:   profName,
			RateKey:   rateKey,
		}

		if batched {
			out := make([]*JSONRPCResponse, 0, len(reqs))
			for i := range reqs {
				if resp := server.Handle(&reqs[i], ident); resp != nil {
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

		resp := server.Handle(&reqs[0], ident)
		if resp == nil {
			writeNoContent(w)
			return
		}
		writeJSON(w, resp)
	})

	return authMiddleware(mc, lanMiddleware(mc, hostMiddleware(mc, handler)))
}

// containsInitialize 判断一批请求里是否包含 initialize。
// 只要有一个，本次就要登记会话（批量里通常只有一个 initialize）。
func containsInitialize(reqs []JSONRPCRequest) bool {
	for i := range reqs {
		if reqs[i].Method == "initialize" {
			return true
		}
	}
	return false
}

// writeJSON 先序列化再写响应。
//
// 顺序是关键：一旦开始写响应体，200 状态码就已经发出去了，此时再发现
// 序列化失败，客户端只会收到一个空响应体。novaai_session_list 曾经就是
// 这样坏的 —— State 里有个 func 字段，encoding/json 对它必然失败，
// 而错误被丢弃，于是工具返回 "HTTP 200 + 0 字节"。
// 先 Marshal 就能在写头之前把失败转成正常的 JSON-RPC 错误。
func writeJSON(w http.ResponseWriter, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		writeJSONRPCError(w, nil, -32603, "响应序列化失败: "+err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
	_, _ = w.Write([]byte("\n"))
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
			next.ServeHTTP(w, withIdentity(r, mc, ""))
			return
		}

		ip := net.ParseIP(host)
		isLoopback := ip != nil && ip.IsLoopback()

		// loopback 在 anonymous=true 时免 token；默认 false，即本机也要 token。
		// 这是必要的：设备上任何 App 都能访问 127.0.0.1，而本服务有 root 能力。
		if isLoopback && mc.Config.Security.Anonymous {
			next.ServeHTTP(w, withIdentity(r, mc, ""))
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
		next.ServeHTTP(w, withIdentity(r, mc, token))
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
