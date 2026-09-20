package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/novaai/novaai-mcp/internal/config"
)

// transport 是一个上游 MCP 会话的两种形态之一（HTTP 短连接 / stdio 长管道）。
//
// 抽象点选在"JSON-RPC 方法调用"这一层，而不是更底层的字节流：
// 两种形态在这层的差异只剩"往哪写、从哪读"，initialize / tools/list /
// tools/call 的流程完全共用（见 probe.go 与 route.go）。
type transport interface {
	// call 发一次请求并返回 result。上游返回 JSON-RPC error 时返回 *rpcCallError。
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	// notify 发一次通告（无 id，不等待响应）。
	notify(ctx context.Context, method string, params any) error
	// close 释放连接；对 stdio 意味着回收子进程。
	close()
}

// rpcRequest / rpcResponse 是 MCP 的 JSON-RPC 信封。
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("上游 JSON-RPC 错误 %d: %s", e.Code, e.Message)
}

// rpcCallError 表示上游**回应了**，但回应是错误。
//
// 与"连不上"必须区分：前者归 `error`（协议层有问题），后者归
// `stopped`（服务没在跑）。见 docs/upstream.md 的状态模型。
type rpcCallError struct{ inner *rpcError }

func (e *rpcCallError) Error() string { return e.inner.Error() }

var (
	// errUnreachable：服务没在跑（连接拒绝、超时、进程不存在、管道关闭）。
	errUnreachable = errors.New("上游不可达")

	// errProtocol：服务在跑但答不通（协议不兼容、HTTP 非 2xx、返回异常）。
	errProtocol = errors.New("上游协议异常")
)

// classify 把一次探测的结果归到 running / stopped / error。
//
// 归类的默认方向是 `error` 而不是 `stopped`：`stopped` 会触发 autoLaunch
// 去拉起进程，把"协议不对"误判成"没在跑"会导致每次调用都白拉一次。
// 宁可显示成 error 让用户去看原因。
func classify(err error) string {
	switch {
	case err == nil:
		return config.UpstreamRunning
	case errors.Is(err, errUnreachable):
		return config.UpstreamStopped
	default:
		return config.UpstreamError
	}
}
