package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
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
// 只有一层中间件：hostMiddleware。没有鉴权层、没有 LAN 层、没有来源判定、
// 没有 CORS —— 重构后所有来源一视同仁，权限边界就是网络可达性。
func BuildMiddlewareChain(server *Server, mc *MiddlewareConfig) http.Handler {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}

		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeJSONRPCError(w, nil, -32600, "仅支持 POST /mcp", nil)
			return
		}

		// 用 MaxBytesReader 而不是 io.LimitReader：后者静默截断，
		// 超限会伪装成 JSON 解析失败（-32700），把客户端引向"JSON 写错了"
		// 这个错误方向。MaxBytesReader 会返回可识别的 *http.MaxBytesError。
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, config.MaxRequestBytes))
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

		// ---- 会话登记 ----
		//
		// 只有两种情况占用会话名额：
		//   1. 客户端带了有效的 Mcp-Session-Id；
		//   2. 本次请求包含 initialize。
		// 其余走无状态路径（sessionID 为空，不登记）。这是必要的：
		// 不实现会话的客户端如果每个请求都新建会话，会在 MaxSessions
		// 处撞墙并持续收到 -32014，直到空闲超时把它们清掉。
		//
		// 会话不携带权限。鉴权结果固定为全局 default profile，
		// 永不从 session 读取（见 server.go 的同名说明）。
		sessionID := ""
		if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
			if _, err := mc.Sessions.Get(sid); err == nil {
				sessionID = sid
				mc.Sessions.Touch(sid)
			}
		}
		if sessionID == "" && containsInitialize(reqs) {
			sess, err := mc.Sessions.Create()
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
			Profile:   config.DefaultProfileName,
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

	return hostMiddleware(mc, handler)
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
// 序列化失败，客户端只会收到一个空响应体。先 Marshal 就能在写头之前
// 把失败转成正常的 JSON-RPC 错误。
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
func hostAllowed(r *http.Request) bool {
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
	return net.ParseIP(host) != nil
}

// hostMiddleware 是唯一一层中间件。
//
// 只做两件事：
//   - Host 必须是 IP 字面量或 localhost；
//   - 有 Origin 头直接拒绝。
//
// 它防的是浏览器 DNS-rebinding，**不校验来源 IP** ——
// Python/Go/curl 直接用内网 IP 访问不会被它拦住。
// 后者在"同网段任何设备可 root shell"这条残余风险里已覆盖，
// 见 docs/security.md。
func hostMiddleware(mc *MiddlewareConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, host := peerInfo(r)
		// unix socket 没有 Host/Origin 语义，且只有 root 能连
		if kind == "unix" {
			next.ServeHTTP(w, r)
			return
		}

		if !hostAllowed(r) {
			mc.Audit.Log(audit.Entry{
				Event:  "host_rejected",
				Peer:   audit.PeerInfo{Type: kind, IP: host},
				Detail: "Host=" + r.Host,
			})
			writeJSONRPCError(w, nil, -32001, "Host 头不被信任", nil)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			mc.Audit.Log(audit.Entry{
				Event:  "origin_rejected",
				Peer:   audit.PeerInfo{Type: kind, IP: host},
				Detail: "Origin=" + origin,
			})
			writeJSONRPCError(w, nil, -32001, "不接受带 Origin 头的请求", nil)
			return
		}

		next.ServeHTTP(w, r)
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
