package upstream

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/novaai/novaai-mcp/internal/config"
)

// stdioTransport 通过子进程的 stdin/stdout 与上游说话（MCP stdio 传输）。
//
// 与 HTTP 的关键差别：它**持有进程**。所以本类型要负责三件事 ——
// 拉起、维持管道、回收（含超时杀整个进程组）。
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	pipes  *bufio.Reader
	mu     sync.Mutex
	pend   map[int]chan rpcResponse
	nextID int
	// ioMu 把一次 tools/call 的"写 + 等"全程串行化。
	//
	// 为什么不靠 mu：mu 只保护 pend 表与写管道的原子性（防串包），
	// 等待响应在锁外 —— 那允许 N 个请求同时在途。对 MCP stdio 规范这没有
	// 错（按 id 多路复用），但 stdio 上游多半是**顺序处理** stdin 的单进程，
	// 无限制地在途只会让上游的队列失控（方案补充：按上游限流，防打挂）。
	// 与 mu 分离是因为 markExited 要拿 mu 唤醒等待者，若 call 持 mu 等待
	// 就死锁了。锁序恒为 ioMu -> mu，无反向。
	ioMu sync.Mutex
	// exited 在 stdout EOF / 进程退出时关闭，用于让所有等待者立刻失败。
	exited chan struct{}
	// exitReason 记录退出的可读原因，只读一次。
	exitOnce   sync.Once
	exitReason string
	// stderr 保留最近若干行 stderr，用于解释"为什么起不来"。
	stderrMu sync.Mutex
	stderr   []string
}

// stderrKeep 保留的 stderr 行数上限。
const stderrKeep = 20

func newStdioTransport(u config.UpstreamConfig) (*stdioTransport, error) {
	args := u.Args
	cmd := exec.Command(u.Command, args...)
	configureProcAttr(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: 建立 stdin 管道失败: %v", errProtocol, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: 建立 stdout 管道失败: %v", errProtocol, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: 建立 stderr 管道失败: %v", errProtocol, err)
	}

	t := &stdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		pipes:  bufio.NewReaderSize(stdout, 1<<20),
		pend:   map[int]chan rpcResponse{},
		exited: make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: 启动 %s 失败: %v", errProtocol, u.Command, err)
	}

	go t.readLoop()
	go t.stderrLoop(stderr)
	return t, nil
}

// readLoop 逐行读取 stdout。
//
// MCP stdio 是"一行一个 JSON"。解析失败的行按上游的日志丢弃 ——
// 很多实现会把调试输出混进 stdout，为此把整个管道判死会过于脆弱。
func (t *stdioTransport) readLoop() {
	for {
		line, err := t.pipes.ReadBytes('\n')
		if len(line) > 0 {
			t.dispatch(line)
		}
		if err != nil {
			t.markExited("管道关闭: " + err.Error())
			return
		}
	}
}

func (t *stdioTransport) dispatch(line []byte) {
	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return // 非 JSON 行：上游的日志，忽略
	}
	var id int
	if len(resp.ID) == 0 || json.Unmarshal(resp.ID, &id) != nil {
		return // 无 id：响应错位的通告，忽略
	}
	t.mu.Lock()
	ch := t.pend[id]
	delete(t.pend, id)
	t.mu.Unlock()
	if ch != nil {
		ch <- resp
	}
}

func (t *stdioTransport) stderrLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		t.stderrMu.Lock()
		t.stderr = append(t.stderr, sc.Text())
		if len(t.stderr) > stderrKeep {
			t.stderr = t.stderr[len(t.stderr)-stderrKeep:]
		}
		t.stderrMu.Unlock()
	}
}

// StderrTail 返回最近几行 stderr，用于解释失败原因。
func (t *stdioTransport) StderrTail() string {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()
	if len(t.stderr) == 0 {
		return ""
	}
	return strings.Join(t.stderr, " | ")
}

func (t *stdioTransport) markExited(reason string) {
	t.exitOnce.Do(func() {
		t.stderrMu.Lock()
		tail := strings.Join(t.stderr, " | ")
		t.stderrMu.Unlock()
		if tail != "" {
			reason += "；stderr: " + tail
		}
		t.exitReason = reason
		close(t.exited)
		// 唤醒所有等待者。
		t.mu.Lock()
		for id, ch := range t.pend {
			delete(t.pend, id)
			close(ch)
		}
		t.mu.Unlock()
	})
}

// Exited 返回进程是否已经退出，以及退出原因。
func (t *stdioTransport) Exited() (bool, string) {
	select {
	case <-t.exited:
		return true, t.exitReason
	default:
		return false, ""
	}
}

func (t *stdioTransport) notify(_ context.Context, method string, params any) error {
	if exited, reason := t.Exited(); exited {
		return fmt.Errorf("%w: %s", errUnreachable, reason)
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("%w: 编码失败: %v", errProtocol, err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.stdin.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("%w: 写管道失败: %v", errUnreachable, err)
	}
	return nil
}

func (t *stdioTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if exited, reason := t.Exited(); exited {
		return nil, fmt.Errorf("%w: %s", errUnreachable, reason)
	}

	// 一次调用全程持 ioMu：同一 stdio 上游的 tools/call 排队执行。
	// markExited 只拿 mu，不会与这里形成锁环。
	t.ioMu.Lock()
	defer t.ioMu.Unlock()

	// 排队期间进程可能退出了，出队后再查一次。
	if exited, reason := t.Exited(); exited {
		return nil, fmt.Errorf("%w: %s", errUnreachable, reason)
	}

	t.mu.Lock()
	t.nextID++
	id := t.nextID
	ch := make(chan rpcResponse, 1)
	t.pend[id] = ch
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		delete(t.pend, id)
		t.mu.Unlock()
		return nil, fmt.Errorf("%w: 编码失败: %v", errProtocol, err)
	}
	_, werr := t.stdin.Write(append(body, '\n'))
	t.mu.Unlock()

	if werr != nil {
		t.mu.Lock()
		delete(t.pend, id)
		t.mu.Unlock()
		return nil, fmt.Errorf("%w: 写管道失败: %v", errUnreachable, werr)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			exited, reason := t.Exited()
			if !exited {
				reason = "响应通道被关闭"
			}
			return nil, fmt.Errorf("%w: %s", errUnreachable, reason)
		}
		if resp.Error != nil {
			return nil, &rpcCallError{inner: resp.Error}
		}
		return resp.Result, nil
	case <-t.exited:
		t.drop(id)
		_, reason := t.Exited()
		return nil, fmt.Errorf("%w: %s", errUnreachable, reason)
	case <-ctx.Done():
		t.drop(id)
		// 超时：按方案杀整个进程组，避免残缺状态残留。
		// 让下一次调用重新 spawn，比留一个半死进程更可预测。
		t.close()
		return nil, fmt.Errorf("%w: 等待 %s 响应超时: %v",
			errUnreachable, method, ctx.Err())
	}
}

func (t *stdioTransport) drop(id int) {
	t.mu.Lock()
	delete(t.pend, id)
	t.mu.Unlock()
}

// close 回收子进程。幂等。
func (t *stdioTransport) close() {
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = killProcessGroup(t.cmd)
	}
	t.markExited("已被关闭")
	// 回收僵尸：Wait 一定要调，否则进程表会积累条目。
	go func() { _ = t.cmd.Wait() }()
}

// alive 报告子进程是否仍在运行。探测时用它判断 stopped。
func (t *stdioTransport) alive() bool {
	exited, _ := t.Exited()
	return !exited
}
