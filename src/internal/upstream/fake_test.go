package upstream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain 兼作"假上游"的入口。
//
// 为什么用测试二进制自己当假上游：
//   - stdio 上游测试需要一个**真实的子进程**（那正是被测行为），
//     而仓库里没有可用的外部进程；
//   - 编译第二个二进制会让 `go test ./...` 依赖额外的构建步骤，
//     也让 CI 的 Windows 与 Linux 路径分叉。
//
// 于是按参数分流：带 --nova-fake-stdio 就变成 stdio 上游，
// 带 --nova-fake-http=PORT 就变成 HTTP 上游，直接退出不跑测试。
func TestMain(m *testing.M) {
	for _, a := range os.Args[1:] {
		if a == "--nova-fake-stdio" {
			fakeStdio()
			return
		}
		if strings.HasPrefix(a, "--nova-fake-http=") {
			fakeHTTPServe(strings.TrimPrefix(a, "--nova-fake-http="))
			return
		}
	}
	os.Exit(m.Run())
}

// ---- 假上游的协议实现（stdio 与 HTTP 共用） ----

type fakeTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// fakeToolNames 是假上游对外声称的工具。其中一个刻意与本地工具同名
// （novaai_status），用来验证命名空间前缀真的隔离了两者。
func fakeToolNames() []string { return []string{"echo", "novaai_status"} }

func fakeTools() []fakeTool {
	return []fakeTool{
		{Name: "echo", Description: "回显参数",
			InputSchema: map[string]any{"type": "object"}},
		{Name: "novaai_status", Description: "与本地工具同名，用于验证前缀隔离",
			InputSchema: map[string]any{"type": "object"}},
	}
}

// fakeHandle 处理一条 JSON-RPC 请求，返回带 id 的完整响应体。
// 返回 nil 表示这是通知，不应有响应体。
func fakeHandle(req rpcRequest) []byte {
	reply := func(result any) []byte {
		raw, _ := json.Marshal(result)
		out, _ := json.Marshal(rpcResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage(fmt.Sprintf("%d", req.ID)),
			Result:  raw,
		})
		return out
	}
	fail := func(code int, msg string) []byte {
		out, _ := json.Marshal(rpcResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage(fmt.Sprintf("%d", req.ID)),
			Error:   &rpcError{Code: code, Message: msg},
		})
		return out
	}

	switch req.Method {
	case "initialize":
		return reply(map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "fake-upstream", "version": "1"},
		})
	case "notifications/initialized":
		return nil
	case "tools/list":
		return reply(map[string]any{"tools": fakeTools()})
	case "tools/call":
		return fakeCall(req, reply, fail)
	default:
		return fail(-32601, "method not found")
	}
}

func fakeCall(req rpcRequest, reply func(any) []byte, fail func(int, string) []byte) []byte {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	// Params 在 rpcRequest 里是 any（客户端侧按任意结构传参），
	// 这里绕一次 JSON 编解码把它取出来 —— 与真实上游用 map 接收再解析等价。
	if req.Params != nil {
		if raw, err := json.Marshal(req.Params); err == nil {
			_ = json.Unmarshal(raw, &p)
		}
	}

	if p.Name == "hang" {
		// 有限的阻塞：客户端超时后应该杀掉我们；万一没杀掉，
		// 30 秒后也会自己退出，不会让测试进程一直挂着。
		time.Sleep(30 * time.Second)
	}
	if p.Name == "boom" {
		return fail(-32000, "假上游故意报错")
	}
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	payload, _ := json.Marshal(map[string]any{
		"tool": p.Name,
		"args": json.RawMessage(args),
	})
	return reply(map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(payload)}},
		"structuredContent": json.RawMessage(payload),
	})
}

// fakeStdio 是 stdio 假上游：逐行读 stdin，逐行写 stdout。
func fakeStdio() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 1<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // 与真实上游一样：忽略非法行
		}
		// 混一行非 JSON 的日志，验证客户端会跳过它而不是把整条管道判死。
		fmt.Fprintln(out, "[fake-upstream] received "+req.Method)
		out.Flush()

		body := fakeHandle(req)
		if body == nil {
			continue
		}
		fmt.Fprintln(out, string(body))
		out.Flush()
	}
}

// fakeHTTPServe 是 HTTP 假上游：监听指定端口，服务 MCP。
//
// 额外的 /__exit 端点只在这里注册（不在 fakeMux 里）：它是给测试收尸用的 ——
// autoLaunch 拉起的子进程**不由本服务托管**（launch 的语义是"拉起来"，
// 不是"由我托管"），所以测试必须自己让它退出，否则它会一直占着端口与
// 测试二进制的文件句柄（Windows 上表现为 go test 收尾时 unlinkat 失败）。
func fakeHTTPServe(port string) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake http listen:", err)
		os.Exit(1)
	}
	mux := fakeMux()
	mux.HandleFunc("/__exit", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bye"))
		go func() {
			// 先把响应刷出去再退出，否则调用方会看到连接被重置。
			time.Sleep(120 * time.Millisecond)
			os.Exit(0)
		}()
	})
	_ = http.Serve(ln, mux)
}

// stopFakeUpstream 让一个由 --nova-fake-http 起的假上游退出。
func stopFakeUpstream(t *testing.T, port int) {
	t.Helper()
	t.Cleanup(func() {
		resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/__exit")
		if err == nil {
			_ = resp.Body.Close()
		}
		// 给它一点时间真正退出，避免下一个用例撞上同一个端口。
		time.Sleep(300 * time.Millisecond)
	})
}

func fakeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body := fakeHandle(req)
		if body == nil {
			// 通知：HTTP 上返回 202 空体（与真实实现一致）
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	return mux
}

// ---- 测试侧的便捷构造 ----

// startFakeHTTP 起一个进程内的假上游（httptest），返回 server 与 url。
func startFakeHTTP(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(fakeMux())
	t.Cleanup(srv.Close)
	return srv, srv.URL + "/mcp"
}

// freePort 拿一个当前空闲的端口号。
//
// 存在 TOCTOU（拿到后到子进程监听之间可能被别人占），但对测试足够：
// 失败会以"上游没起来"的形式暴露，而不是静默通过。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// selfExe 返回当前测试二进制路径，供 stdio / launch 测试当作"上游命令"。
func selfExe() string { return os.Args[0] }
