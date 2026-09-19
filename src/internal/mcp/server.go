package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"
	"unicode/utf8"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/profile"
	"github.com/novaai/novaai-mcp/internal/ratelimit"
	"github.com/novaai/novaai-mcp/internal/tools"
)

// ServerVersion 是 serverInfo.version 的唯一来源，避免多处硬编码漂移。
const ServerVersion = "0.05"

// 支持的 MCP 协议版本，按新到旧排列。
var supportedProtocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const latestProtocol = "2025-06-18"

type ServerConfig struct {
	Config    *config.Config
	Registry  *tools.Registry
	Audit     *audit.Logger
	RateLimit *ratelimit.Limiter
	Deps      *tools.Deps
}

// Identity 是本次请求的鉴权结果，由中间件解析后传入协议层。
//
// 三个字段职责不同，不要混用：
//   - SessionID：MCP 会话 id，只用于审计与 session_list；无状态请求为空串。
//   - Profile：本次请求的 token 解析出的 profile，**权限决策的唯一依据**。
//     不能改读会话上记录的 profile —— 同一个 Mcp-Session-Id 可以被不同
//     token 复用，那样会让低权限 token 继承高权限会话。
//   - RateKey：客户端不可伪造的限流身份键（token 哈希），只给限流器用。
type Identity struct {
	SessionID string
	Profile   string
	RateKey   string
}

type Server struct {
	cfg *ServerConfig
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ContentItem 是 MCP tools/call 结果的内容块。
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// CallToolResult 是 tools/call 的标准返回体。
//
// 规范要求工具结果放在 content 数组里，而不是直接把业务对象当 result 返回。
// 这是与 Claude Desktop / Cursor 等严格客户端互通的关键。
type CallToolResult struct {
	Content           []ContentItem `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError,omitempty"`
}

func NewServer(cfg *ServerConfig) *Server {
	return &Server{cfg: cfg}
}

// IsNotification 判断请求是否为 JSON-RPC 通知（无 id）。
// 规范规定通知绝不能有响应体。
func IsNotification(req *JSONRPCRequest) bool {
	if len(req.ID) == 0 {
		return true
	}
	return string(req.ID) == "null"
}

// Handle 处理单个 JSON-RPC 请求。
// 返回 nil 表示这是通知，调用方不应写任何响应。
func (s *Server) Handle(req *JSONRPCRequest, ident Identity) *JSONRPCResponse {
	if req.Method == "" {
		if IsNotification(req) {
			return nil
		}
		return errorResponse(req.ID, -32600, "invalid request: method 缺失")
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req, ident.SessionID)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req, ident)
	case "prompts/list":
		if IsNotification(req) {
			return nil
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Result: map[string]any{"prompts": []any{}}}
	case "resources/list":
		if IsNotification(req) {
			return nil
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Result: map[string]any{"resources": []any{}}}
	case "ping":
		if IsNotification(req) {
			return nil
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "notifications/initialized", "notifications/cancelled", "notifications/roots/list_changed":
		// 通知类方法：一律静默丢弃
		return nil
	default:
		if IsNotification(req) {
			return nil
		}
		return errorResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

func errorResponse(id json.RawMessage, code int, msg string) *JSONRPCResponse {
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id,
		Error: &JSONRPCError{Code: code, Message: msg}}
}

// negotiateProtocol 优先回显客户端请求的版本，不支持则回退到最新版。
func negotiateProtocol(client string) string {
	for _, v := range supportedProtocols {
		if v == client {
			return v
		}
	}
	return latestProtocol
}

func (s *Server) handleInitialize(req *JSONRPCRequest, sessionID string) *JSONRPCResponse {
	if IsNotification(req) {
		return nil
	}

	var params struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	_ = json.Unmarshal(req.Params, &params)

	// 只声明真实具备的能力。声明了却返回空列表会让部分客户端报错。
	capabilities := map[string]any{
		"tools": map[string]any{"listChanged": false},
	}

	s.cfg.Audit.Log(audit.Entry{
		Event:   "initialize",
		Session: sessionID,
		Detail: fmt.Sprintf("client=%s/%s protocol=%s",
			params.ClientInfo.Name, params.ClientInfo.Version, params.ProtocolVersion),
	})

	return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
		"protocolVersion": negotiateProtocol(params.ProtocolVersion),
		"capabilities":    capabilities,
		"serverInfo": map[string]any{
			"name":        "novaai-android-mcp",
			"title":       "NovaAI Mobile Control Protocol",
			"version":     ServerVersion,
			"description": "Android Root MCP 服务",
		},
		"instructions": buildInstructions(),
	}}
}

func (s *Server) handleToolsList(req *JSONRPCRequest) *JSONRPCResponse {
	if IsNotification(req) {
		return nil
	}
	return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
		"tools": s.cfg.Registry.List(),
	}}
}

func (s *Server) handleToolsCall(req *JSONRPCRequest, ident Identity) *JSONRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		if IsNotification(req) {
			return nil
		}
		return errorResponse(req.ID, -32602, "invalid params: "+err.Error())
	}

	t, ok := s.cfg.Registry.Get(params.Name)
	if !ok {
		if IsNotification(req) {
			return nil
		}
		return errorResponse(req.ID, -32015, "工具不存在: "+params.Name)
	}

	// 通知形式的 tools/call 没有回包渠道，执行但丢弃结果。
	notify := IsNotification(req)

	// ---- 权限：静态 profile（白名单/黑名单 + 风险上限）----
	//
	// 这是整个服务唯一的策略收口点：所有工具调用都经由这里，不必在工具内部
	// 各自加守卫。策略来自配置文件的静态 profile，不做交互式确认 —— 配置一次
	// 长期生效，避免每次调用都要人工放行。
	//
	// profile 取本次请求 token 的解析结果（ident.Profile），不读会话记录：
	// Mcp-Session-Id 由客户端携带，可以被复用。
	if s.cfg.Deps != nil && s.cfg.Deps.Profiles != nil {
		profName := ident.Profile
		if profName == "" {
			profName = "default"
		}
		p := s.cfg.Deps.Profiles.Get(profName)
		risk := profile.ResolveRisk(params.Name, params.Arguments)
		if !profile.Allows(&p, params.Name, risk) {
			s.cfg.Audit.Log(audit.Entry{
				Event:   "profile_denied",
				Tool:    params.Name,
				Session: ident.SessionID,
				Profile: profName,
				Result:  "denied",
				Detail:  fmt.Sprintf("risk=%d ceiling=%d", risk, p.RiskCeiling),
			})
			if notify {
				return nil
			}
			return errorResponse(req.ID, -32003,
				fmt.Sprintf("工具 %s 不被 profile %q 允许（risk=%d）",
					params.Name, profName, risk))
		}
	}

	// ---- 限流：全局 / 客户端身份 / 工具 ----
	//
	// 键用 ident.RateKey（token 哈希）而不是 sessionID：后者是客户端自报的
	// 请求头，每次换一个就能拿到全新的满额桶，限流会被完全绕过。
	if s.cfg.RateLimit != nil {
		if err := s.cfg.RateLimit.Allow(ident.RateKey, params.Name); err != nil {
			s.cfg.Audit.Log(audit.Entry{
				Event: "rate_limited", Tool: params.Name, Session: ident.SessionID,
				Result: "denied", Detail: err.Error(),
			})
			if notify {
				return nil
			}
			return errorResponse(req.ID, -32009, err.Error())
		}
		// ---- 并发槽 ----
		if !s.cfg.RateLimit.AcquireSlot(ident.RateKey) {
			lerr := &ratelimit.LimitError{
				Scope: ratelimit.ScopeConcurrent,
				Tool:  params.Name,
				Limit: s.cfg.RateLimit.ConcurrencyLimit(),
			}
			s.cfg.Audit.Log(audit.Entry{
				Event: "concurrency_limited", Tool: params.Name, Session: ident.SessionID,
				Result: "denied", Detail: lerr.Error(),
			})
			if notify {
				return nil
			}
			return errorResponse(req.ID, -32010, lerr.Error())
		}
		defer s.cfg.RateLimit.ReleaseSlot(ident.RateKey)
	}

	ctx := context.Background()
	ctx = tools.WithSessionID(ctx, ident.SessionID)
	ctx = tools.WithProfile(ctx, ident.Profile)
	ctx = tools.WithDeps(ctx, s.cfg.Deps)

	start := time.Now()
	result, err := safeCall(ctx, t, params.Arguments)
	elapsed := time.Since(start).Milliseconds()

	entry := audit.Entry{
		Event:      "tool_call",
		Tool:       params.Name,
		Session:    ident.SessionID,
		DurationMS: elapsed,
	}
	if err != nil {
		entry.Result = "error"
		entry.Detail = err.Error()
	} else {
		entry.Result = "ok"
	}
	if s.cfg.Config.Audit.IncludeArgs {
		entry.ArgsPreview = audit.BuildArgsPreview(params.Arguments, &s.cfg.Config.Audit)
	}
	s.cfg.Audit.Log(entry)

	if notify {
		return nil
	}

	// 工具自身的执行失败属于"工具结果错误"，按 MCP 规范应放在
	// result.isError 里，而不是 JSON-RPC error —— 否则客户端会当成协议故障。
	if err != nil {
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Result: errorResult(err.Error())}
	}
	return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
		Result: successResult(result, s.resultLimit())}
}

// resultLimit 返回单个工具结果的字节上限。0 表示不限制。
func (s *Server) resultLimit() int64 {
	if s.cfg.Config == nil {
		return 0
	}
	return s.cfg.Config.Limits.ResultPreviewBytes
}

// safeCall 包住工具 Handler，任何 panic 都转成普通错误，
// 避免一个工具写崩整个 daemon。
func safeCall(ctx context.Context, t *tools.Tool, args json.RawMessage) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("工具 %s 内部 panic: %v", t.Name, r)
			result = nil
			_ = debug.Stack() // 保留调用点，便于后续接 crash dump
		}
	}()
	return t.Handler(ctx, args)
}

// successResult 渲染工具返回值，并按 limits.resultPreviewBytes 截断。
//
// 截断必须在这里做：这是所有工具结果的唯一出口。超限时同时丢弃
// structuredContent —— 只截 content 而保留完整的结构化副本，帧大小
// 一点没省，还会让两者不一致。
func successResult(v any, limit int64) CallToolResult {
	text := toText(v)
	if limit > 0 && int64(len(text)) > limit {
		return CallToolResult{
			Content: []ContentItem{{Type: "text", Text: truncateBytes(text, limit) +
				fmt.Sprintf("\n\n[结果已截断：原始 %d 字节，上限 %d 字节。请用更精确的参数缩小范围。]",
					len(text), limit)}},
		}
	}
	return CallToolResult{
		Content:           []ContentItem{{Type: "text", Text: text}},
		StructuredContent: structured(v),
	}
}

// truncateBytes 把 s 截到不超过 limit 字节，且不切断 UTF-8 字符。
func truncateBytes(s string, limit int64) string {
	if limit <= 0 || int64(len(s)) <= limit {
		return s
	}
	n := int(limit)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func errorResult(msg string) CallToolResult {
	return CallToolResult{
		Content: []ContentItem{{Type: "text", Text: msg}},
		IsError: true,
	}
}

// toText 把工具返回值渲染成给模型看的文本。
// 字符串原样输出，其余类型转成缩进 JSON。
func toText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.RawMessage:
		return string(x)
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// structured 只在返回值天然是对象/数组时才附带 structuredContent，
// 标量包一层没有意义。
func structured(v any) any {
	switch v.(type) {
	case map[string]any, []any:
		return v
	default:
		return nil
	}
}

func buildInstructions() string {
	return "NovaAI-MCP：Android Root 全能力服务。" +
		"所有工具通过 tools/call 调用，参数放在 arguments 对象里。" +
		"工具的业务失败会以 isError=true 返回，请读取 content[0].text 获取原因。"
}
