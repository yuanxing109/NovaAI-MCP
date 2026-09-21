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

// errSessionExpired 表示"上游声明我们的会话已失效"（MCP 约定用 HTTP 404
// 表达）。它与 errProtocol 的区别在于处理方式：前者应当**重置会话、
// 重新 initialize、重试一次**；后者是真正的协议错误，重试没有意义。
var errSessionExpired = errors.New("上游会话已过期")

// httpTransport 通过 HTTP POST 与上游说话。
//
// 连接与会话都是**长驻**的：http.Client 带 Keep-Alive 连接池，
// initialize 拿到的 Mcp-Session-Id 存在 h.sid 里、后续每次请求都带上，
// tools/list 的结果缓存在 entry 上 —— 会话生命周期 = 上游生命周期，
// 不是请求生命周期。唯一的例外是上游从不返回会话头（无状态上游）：
// 那时 sid 保持为空，每次请求不带该头，自然退化为独立 POST。
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
	if err != nil && errors.Is(err, errSessionExpired) {
		// 会话过期：重置 → 重新 initialize（ensureInit 会看到 inited=false）
		// → 重试一次原请求。仍失败就把错误交给上层 —— 随后的重探会把
		// 状态标成 stopped，而不是留着一个陈旧的 running。
		//
		// 不用担心 initialize 自身递归：重试条件要求"请求时带着会话 id"，
		// 而重置后的 initialize 不带 id，最多失败一次就返回。
		h.resetSession()
		if ierr := h.ensureInit(ctx); ierr != nil {
			return nil, ierr
		}
		raw, err = h.post(ctx, body)
	}
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
//
// 会话过期的判定：请求时**带着**会话 id，却收到 404 —— MCP 约定上游用
// 404 表达"session 不认识"。此时返回 errSessionExpired 让 call 层走
// "重新 initialize + 重试一次"，而不是把一次可自愈的失败直接抛给调用方。
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

	switch {
	case resp.StatusCode == http.StatusNotFound && sid != "":
		return nil, fmt.Errorf("%w: HTTP 404: %s", errSessionExpired, summarize(raw))
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("%w: HTTP %d: %s",
			errProtocol, resp.StatusCode, summarize(raw))
	}
	return raw, nil
}

// resetSession 丢弃当前会话状态，让下一次 ensureInit 重新走 initialize。
func (h *httpTransport) resetSession() {
	h.mu.Lock()
	h.sid = ""
	h.inited = false
	h.mu.Unlock()
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
