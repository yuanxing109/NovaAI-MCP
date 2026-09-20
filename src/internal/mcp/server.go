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
	"github.com/novaai/novaai-mcp/internal/upstream"
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
	// Upstreams 是上游 MCP 聚合注册表。nil 表示不做聚合（测试里常见）。
	Upstreams *upstream.Registry
}

// Identity 是本次请求的协议层标识，由中间件解析后传入。
//
// 两个字段职责不同，不要混用：
//   - SessionID：MCP 会话 id，只用于审计与日志；无状态请求为空串。
//   - Profile：永远是 config.DefaultProfileName。它是常量，不是"解析结果"——
//     中间件不再做鉴权，也没有任何输入能改变它。
type Identity struct {
	SessionID string
	Profile   string
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
		"tools": s.mergedTools(),
	}}
}

// mergedTools 合并本地工具与上游工具，作为 tools/list 的唯一出口。
//
// 上游工具**不进** tools.Registry：它们的数量随用户配置动态变化，
// 塞进注册表会让"工具总数"这个被断言锁住的数字跟着配置漂移，
// 也会让 register_test 的负向清单失去意义。合并只发生在这一处。
//
// 上游工具没有 Handler —— 它们不可通过本地注册表执行，调用一律走
// handleToolsCall 的上游分支。
func (s *Server) mergedTools() []*tools.Tool {
	local := s.cfg.Registry.List()
	if s.cfg.Upstreams == nil {
		return local
	}
	remote := s.cfg.Upstreams.MergedTools()
	if len(remote) == 0 {
		return local
	}
	out := make([]*tools.Tool, 0, len(local)+len(remote))
	out = append(out, local...)
	for _, t := range remote {
		out = append(out, &tools.Tool{
			Name:        t.Name,
			Title:       t.Title,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out
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

	// 通知形式的 tools/call 没有回包渠道，执行但丢弃结果。
	notify := IsNotification(req)

	// ---- ① 工具存在性：先本地，再按命名空间前缀找上游 ----
	localTool, isLocal := s.cfg.Registry.Get(params.Name)
	upName, upTool := "", ""
	isUpstream := false
	if !isLocal && s.cfg.Upstreams != nil {
		if n, t, ok := s.cfg.Upstreams.SplitName(params.Name); ok {
			upName, upTool, isUpstream = n, t, true
		}
	}
	if !isLocal && !isUpstream {
		if notify {
			return nil
		}
		return errorResponse(req.ID, -32015, "工具不存在: "+params.Name)
	}

	// ---- ② 权限：本地走静态档位；上游走该上游的 denyTools / riskCeiling ----
	//
	// 这是整个服务唯一的策略收口点：所有工具调用都经由这里，不必在工具内部
	// 各自加守卫。档位不由任何请求输入决定 —— 没有 token，没有来源判定，
	// 也没有会话绑定。唯一的 default 档位允许全部本地工具（denyTools 为空、
	// allowTools 为 ["*"]），所以这一道对本地工具当前的净效果是"永远放行"；
	// 保留它是因为风险上限与工具名匹配的语义仍在，未来要收窄时改一处即可。
	//
	// 会话不携带权限。鉴权结果固定为全局 default profile，永不从 session
	// 读取。历史实现曾把 profile 挂在 session 上，仅作观测；现已删除，
	// 防止误读。
	if isLocal && s.cfg.Deps != nil && s.cfg.Deps.Profiles != nil {
		profName := config.DefaultProfileName
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
	if isUpstream {
		risk, err := s.cfg.Upstreams.Authorize(upName, upTool, params.Arguments)
		if err != nil {
			s.cfg.Audit.Log(audit.Entry{
				Event:   "upstream_denied",
				Tool:    params.Name,
				Session: ident.SessionID,
				Risk:    risk,
				Result:  "denied",
				Detail:  err.Error(),
			})
			if notify {
				return nil
			}
			// 上游策略拒绝是"业务失败"而不是协议故障：
			// 客户端应当看到一次正常的工具失败，而不是 JSON-RPC 错误。
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: errorResult(err.Error())}
		}
	}

	// ---- ③ 限流：全局 / shell / 并发 ----
	//
	// 没有按身份的桶：所有来源本来就是同一个身份，多一个维度只是多一处
	// 可被误读的状态。上游工具的带前缀名不会命中 shellTools 表，
	// 所以它们只吃全局桶 —— 这正是想要的。
	if s.cfg.RateLimit != nil {
		if err := s.cfg.RateLimit.Allow(params.Name); err != nil {
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
		if !s.cfg.RateLimit.AcquireSlot() {
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
		defer s.cfg.RateLimit.ReleaseSlot()
	}

	// ---- ④ 路由：本地执行 / 上游转发 ----
	ctx := context.Background()
	start := time.Now()

	entry := audit.Entry{
		Event:   "tool_call",
		Tool:    params.Name,
		Session: ident.SessionID,
	}
	if s.cfg.Config.Audit.Enabled {
		entry.ArgsPreview = audit.BuildArgsPreview(params.Arguments)
	}

	if isUpstream {
		// 上游工具不经 safeCall：转发路径里没有第三方 Handler 可 panic，
		// 而 upstream.Call 自己已经把协议错误转成了 error。
		outcome, err := s.cfg.Upstreams.Call(ctx, upName, upTool, params.Arguments)
		entry.DurationMS = time.Since(start).Milliseconds()
		if err != nil {
			entry.Result = "error"
			entry.Detail = err.Error()
			s.cfg.Audit.Log(entry)
			if notify {
				return nil
			}
			// 上游不可用/转发失败都是业务失败，不是协议故障。
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: errorResult(err.Error())}
		}
		entry.Result = "ok"
		s.cfg.Audit.Log(entry)
		if notify {
			return nil
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Result: renderUpstreamResult(outcome, s.resultLimit())}
	}

	ctx = tools.WithSessionID(ctx, ident.SessionID)
	ctx = tools.WithProfile(ctx, ident.Profile)
	ctx = tools.WithDeps(ctx, s.cfg.Deps)

	result, err := safeCall(ctx, localTool, params.Arguments)
	entry.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		entry.Result = "error"
		entry.Detail = err.Error()
	} else {
		entry.Result = "ok"
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

// renderUpstreamResult 把上游返回的 MCP 结果原样透出。
//
// 上游给的已经是 {content:[...], structuredContent, isError} 的形状，
// 再套一层 successResult 会把它当普通对象 JSON 序列化进 content[0].text，
// 客户端要多解析一层才能拿到真实结果 —— 那是明显的退化。
//
// 截断仍然在这里做：这是所有工具结果（含上游）的唯一出口，
// resultPreviewBytes 的语义必须对两者一致。
func renderUpstreamResult(o *upstream.Outcome, limit int64) CallToolResult {
	if o == nil {
		return CallToolResult{Content: []ContentItem{{Type: "text", Text: ""}}}
	}
	items := make([]ContentItem, 0, len(o.Content))
	total := 0
	for _, c := range o.Content {
		items = append(items, ContentItem{Type: c.Type, Text: c.Text})
		total += len(c.Text)
	}
	if len(items) == 0 {
		items = []ContentItem{{Type: "text", Text: toText(o.StructuredContent)}}
		total = len(items[0].Text)
	}

	if limit > 0 && int64(total) > limit {
		trimmed := truncateBytes(items[0].Text, limit)
		return CallToolResult{
			Content: []ContentItem{{Type: "text", Text: trimmed +
				fmt.Sprintf("\n\n[结果已截断：原始 %d 字节，上限 %d 字节。请用更精确的参数缩小范围。]",
					total, limit)}},
			IsError: o.IsError,
		}
	}
	return CallToolResult{
		Content:           items,
		StructuredContent: o.StructuredContent,
		IsError:           o.IsError,
	}
}

// resultLimit 返回单个工具结果的字节上限。0 表示不限制。
func (s *Server) resultLimit() int64 {
	if s.cfg.Config == nil {
		return 0
	}
	return s.cfg.Config.ResultPreviewBytes
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
