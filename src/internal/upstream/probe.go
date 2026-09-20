package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/novaai/novaai-mcp/internal/config"
)

// connect 完成一次完整探测：initialize → tools/list。
//
// 返回的状态与原因用于更新注册表；tr 非 nil 且 status 为 running 时
// 调用方可复用它。
//
// **复用已有连接是有意的**：stdio 上游的管道建立代价高（要 spawn 进程），
// 每次探测都重建会让"WebUI 点一下探测"变成"重启上游"。但连接一旦不可用
// 就必须丢弃 —— 拿一个半死的管道继续发请求没有意义。
func connect(ctx context.Context, u config.UpstreamConfig, timeout time.Duration, existing transport) (
	status, reason string, tools []Tool, tr transport, err error,
) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tr = existing
	if tr != nil && !reusable(tr) {
		tr.close()
		tr = nil
	}
	if tr == nil {
		tr, err = dial(u, timeout)
		if err != nil {
			st := classify(err)
			if errors.Is(err, errProtocol) {
				st = config.UpstreamError
			}
			return st, err.Error(), nil, nil, err
		}
	}

	tools, err = handshake(ctx, tr)
	if err != nil {
		tr.close()
		st := classify(err)
		if errors.Is(err, errProtocol) {
			st = config.UpstreamError
		}
		return st, err.Error(), nil, nil, err
	}
	return config.UpstreamRunning, "", tools, tr, nil
}

// reusable 判断一条已有连接是否还能继续用。
//
// HTTP 是无状态的，永远可以重试；stdio 要先确认子进程还活着，
// 否则会在一个已死的管道上等到超时 —— 那是把"进程没了"伪装成"慢"。
func reusable(tr transport) bool {
	if st, ok := tr.(*stdioTransport); ok {
		return st.alive()
	}
	return true
}

// dial 按类型建立一条新连接。
func dial(u config.UpstreamConfig, timeout time.Duration) (transport, error) {
	switch u.Type {
	case config.UpstreamTypeHTTP:
		return newHTTPTransport(u.URL, timeout), nil
	case config.UpstreamTypeStdio:
		return newStdioTransport(u)
	default:
		return nil, fmt.Errorf("%w: 未知上游类型 %q", errProtocol, u.Type)
	}
}

// handshake 走完 initialize → tools/list。
func handshake(ctx context.Context, tr transport) ([]Tool, error) {
	if h, ok := tr.(*httpTransport); ok {
		if err := h.ensureInit(ctx); err != nil {
			return nil, err
		}
	} else {
		// stdio 由我们 spawn，上游必然已就绪；仍跑一次 initialize
		// 以便发现"起来了但不是 MCP"的情况。
		if _, err := tr.call(ctx, "initialize", initParams()); err != nil {
			return nil, err
		}
		_ = tr.notify(ctx, "notifications/initialized", map[string]any{})
	}

	raw, err := tr.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res struct {
		Tools []struct {
			Name        string         `json:"name"`
			Title       string         `json:"title"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("%w: tools/list 结构异常: %v", errProtocol, err)
	}

	out := make([]Tool, 0, len(res.Tools))
	seen := map[string]bool{}
	for _, t := range res.Tools {
		name := strings.TrimSpace(t.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out = append(out, Tool{
			Name:        name,
			Title:       t.Title,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out, nil
}

func initParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "novaai-mcp", "version": "0.05"},
	}
}
