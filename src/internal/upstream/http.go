package upstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// httpTransport 通过 HTTP POST 与上游说话。
//
// 每次 call 都是一个独立的 POST —— MCP 的 Streamable HTTP 允许这种无状态
// 用法。会话 id 若上游在 initialize 响应里给了（Mcp-Session-Id 响应头），
// 后续请求会带上，因为部分上游会用它会话粘性。
type httpTransport struct {
	url    string
	client *http.Client

	mu     sync.Mutex
	sid    string
	inited bool

	seq atomic.Int64
}

func newHTTPTransport(url string, timeout time.Duration) *httpTransport {
	return &httpTransport{
		url: url,
		client: &http.Client{
			Timeout: timeout,
			// 上游都是本机/局域网服务。不做代理、不跟随重定向：
			// 跟随重定向会让"Host 校验"这类本地保护在上游侧形同虚设，
			// 也容易把请求打到意料之外的地方。
			Transport: &http.Transport{
				Proxy: nil,
				DialContext: (&net.Dialer{
					Timeout: timeout,
				}).DialContext,
				TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
				DisableKeepAlives:     false,
				MaxIdleConnsPerHost:   2,
				ResponseHeaderTimeout: timeout,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (h *httpTransport) close() {
	h.client.CloseIdleConnections()
}

func (h *httpTransport) notify(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("%w: 编码请求失败: %v", errProtocol, err)
	}
	_, err = h.post(ctx, body)
	// 通告不关心响应；只要请求本身发得出去就算成功。
	if err != nil && errors.Is(err, errUnreachable) {
		return err
	}
	return nil
}

func (h *httpTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := int(h.seq.Add(1))
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("%w: 编码请求失败: %v", errProtocol, err)
	}
	raw, err := h.post(ctx, body)
	if err != nil {
		return nil, err
	}

	// 有些上游对通知返回 202 空体；对请求则必须是 JSON。
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: %s 返回了空响应体", errProtocol, method)
	}

	var resp rpcResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("%w: %s 的响应不是 JSON-RPC: %v",
			errProtocol, method, err)
	}
	if resp.Error != nil {
		return nil, &rpcCallError{inner: resp.Error}
	}
	return resp.Result, nil
}

// post 发一次 POST，处理状态码与连接层错误。
func (h *httpTransport) post(ctx context.Context, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: 构造请求失败: %v", errProtocol, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	h.mu.Lock()
	sid := h.sid
	h.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		// 上下文超时算"不可达"而不是"协议异常"：
		// 服务没起来与"起来了但卡住"在运维上是同一类处理（重试/拉起）。
		return nil, fmt.Errorf("%w: %v", errUnreachable, unwrapNetErr(err))
	}
	defer resp.Body.Close()

	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		h.mu.Lock()
		h.sid = s
		h.mu.Unlock()
	}

	// 限长读取：上游是任意的，不能让它用超大响应体把本服务撑爆。
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: 读取响应失败: %v", errUnreachable, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%w: HTTP %d: %s",
			errProtocol, resp.StatusCode, summarize(raw))
	}
	return raw, nil
}

// maxResponseBytes 是单个上游响应的读取上限（8 MiB）。
//
// 超过它的上游结果本来也会被 resultPreviewBytes 截断，这里只是防止
// 在截断之前把内存吃光。
const maxResponseBytes = 8 << 20

// ensureInit 完成 MCP 握手。幂等。
func (h *httpTransport) ensureInit(ctx context.Context) error {
	h.mu.Lock()
	done := h.inited
	h.mu.Unlock()
	if done {
		return nil
	}

	if _, err := h.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "novaai-mcp", "version": "0.05"},
	}); err != nil {
		return err
	}
	// 通告失败不致命：部分上游不实现它。
	_ = h.notify(ctx, "notifications/initialized", map[string]any{})

	h.mu.Lock()
	h.inited = true
	h.mu.Unlock()
	return nil
}

// unwrapNetErr 把 Go 的嵌套网络错误压成一行可读文本。
//
// 默认的 *url.Error 会带上完整 URL 与 "Post \"http://...\"" 前缀，
// 直接塞进状态原因里噪音太大。
func unwrapNetErr(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("连接超时")
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		if oe.Op == "dial" {
			return fmt.Errorf("连接失败: %v", oe.Err)
		}
	}
	return err
}

// summarize 截断一段用于错误信息的文本。
func summarize(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	if s == "" {
		return "(空响应体)"
	}
	return s
}
