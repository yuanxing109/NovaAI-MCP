// Package upstream 把外部 MCP 服务聚合进本服务的工具面。
//
// 定位：NovaAI-MCP 不只是"一个以 root 运行的工具箱"，它同时是一个
// **MCP 聚合网关** —— 对外一个地址、一份合并后的工具列表；对内是
// 自身 29 个核心工具 + 用户通过 KernelSU WebUI 添加的任意上游 MCP。
//
// # 命名空间
//
// 上游工具以 `{upstream}__{tool}` 出现在 tools/list 里，双下划线分隔。
// 上游名**禁止**包含 `__`（config.Validate 会把关），因此分隔符无歧义。
//
// # 状态模型
//
//	running   可达，工具已合并
//	stopped   配置存在，探测不通（连接拒绝/超时；stdio 进程不在）
//	error     探测本身出错（协议不兼容、HTTP 非 2xx、返回异常）
//	disabled  用户禁用，不探测、不暴露
//
// 状态**不落盘**：每次启动重新探测，不做后台轮询。刷新时机只有三个 ——
// 启动时、novaai_config probe_upstreams、以及调用某个非 running 上游前
// 的实时探测。详见 docs/upstream.md。
//
// # 安全边界
//
// 上游 MCP 的安全性由上游自己负责，本包只做转发，不覆盖上游的鉴权。
// 本包能施加的控制只有两条：`denyTools` 与 `riskCeiling`（见 Authorize）。
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
)

// namespaceSep 与命名空间相关函数见 merge.go。

// defaultTimeout 在上游配置缺省或全局值为 0 时兜底。
const defaultTimeout = 60 * time.Second

// StartupProbeTimeout 是**启动时**那一轮探测的总时限。
//
// 与调用时用的 shellTimeoutSeconds 分开：那个默认 60 秒，是给"上游确实在
// 干活"留的余量；而启动探测只是"连一下、拉个工具列表"，配了一个不可达的
// 上游就让 daemon 多等一分钟启动，代价太大 —— 而启动延迟会直接影响
// 开机时模块能否及时跟上（service.sh 的看门狗也会看到更长的空窗）。
//
// 超时不是失败：被截断的上游状态会停在 stopped，之后 probe_upstreams
// 或一次调用都会重新探。宁可先起来，也不要为了一个探测卡住启动。
const StartupProbeTimeout = 15 * time.Second

// launchWait 是 autoLaunch 拉起上游后等待其就绪的时间。
//
// 固定 2 秒而不是"轮询直到就绪"：多数上游是被 Intent 拉起的 App，
// 冷启动时间不可预测；固定等待 + 一次重探是可控且可解释的行为。
// 没起来就让调用方看到明确的失败，而不是无限等下去。
const launchWait = 2 * time.Second

// Tool 是一个上游工具的描述。字段与 MCP tools/list 条目对应。
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ContentItem 是 MCP 结果里的内容块。
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Outcome 是上游 tools/call 的结果。
//
// 本包不做渲染也不做截断：原样带回来交给 mcp 层 —— 那里是所有工具结果的
// 唯一出口，`resultPreviewBytes` 的截断必须只在一个地方发生。
type Outcome struct {
	Content           []ContentItem `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError,omitempty"`
}

// StatusInfo 是状态快照，给 novaai_upstream_status 与 WebUI 用。
type StatusInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Target     string `json:"target"`
	Status     string `json:"status"`
	Enabled    bool   `json:"enabled"`
	Tools      int    `json:"tools"`
	Launch     string `json:"launch"`
	AutoLaunch bool   `json:"autoLaunch"`
	Reason     string `json:"reason,omitempty"`
}

type entry struct {
	cfg    config.UpstreamConfig
	status string
	reason string
	tools  []Tool
	tr     transport
}

// Registry 是上游注册表：连接管理、状态维护、工具发现、路由转发。
//
// 零值不可用，必须经 NewRegistry 构造。
type Registry struct {
	mu      sync.RWMutex
	cfg     *config.Config
	audit   *audit.Logger
	entries map[string]*entry
	order   []string
	timeout time.Duration
}

func NewRegistry(cfg *config.Config) *Registry {
	r := &Registry{entries: map[string]*entry{}, timeout: defaultTimeout}
	// 启动时的装配不算"重载"，不写审计 —— 否则每次开机都会留下一条
	// 容易被误读成"有人改了配置"的记录。
	r.reload(cfg, false)
	return r
}

// SetAudit 注入审计器。允许在 NewRegistry 之后调用，避免 main 里
// 审计与上游注册表的构造顺序耦合。
func (r *Registry) SetAudit(l *audit.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audit = l
}

// timeoutOf 取本次会话的超时。
//
// 复用 shellTimeoutSeconds 是刻意的：上游调用与 shell 命令同属"外部进程，
// 可能卡住"这一类，用户只需要记住一个超时旋钮。
func timeoutOf(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.ShellTimeoutSeconds > 0 {
		return time.Duration(cfg.ShellTimeoutSeconds) * time.Second
	}
	return defaultTimeout
}

// Reload 用新配置重建注册表。
//
// 会关掉所有旧连接（stdio 子进程一并回收），然后**同步**探测一次。
// 这是 novaai_config reload_upstreams 与 WebUI 保存配置后的入口。
//
// 由于它会改变整个工具面，每次调用都写一条 upstream_reload 审计 ——
// 这是"谁在什么时候动了上游配置"的唯一线索。启动时的第一次装配走
// reload(cfg, false)，不写。
func (r *Registry) Reload(cfg *config.Config) {
	r.reload(cfg, true)
}

func (r *Registry) reload(cfg *config.Config, logIt bool) {
	before := r.names()

	r.mu.Lock()
	if r.cfg == nil || len(r.cfg.Upstreams) != len(cfg.Upstreams) ||
		r.orderDirty(cfg) {
		// 只有集合变了才丢弃旧连接：单纯的 enable/禁用它不值得重启子进程。
		for _, name := range r.order {
			if e := r.entries[name]; e != nil && e.tr != nil {
				e.tr.close()
			}
		}
		r.entries = map[string]*entry{}
		r.order = nil
	}
	for _, uc := range cfg.Upstreams {
		e := r.entries[uc.Name]
		if e == nil {
			e = &entry{status: config.UpstreamStopped}
			r.entries[uc.Name] = e
			r.order = append(r.order, uc.Name)
		}
		if e.tr != nil && (e.cfg.Type != uc.Type ||
			e.cfg.URL != uc.URL || e.cfg.Command != uc.Command) {
			// 连接目标变了，旧管道必须作废。
			e.tr.close()
			e.tr = nil
			e.status = config.UpstreamStopped
			e.tools = nil
			e.reason = ""
		}
		e.cfg = uc
		if !uc.Enabled {
			if e.tr != nil {
				e.tr.close()
				e.tr = nil
			}
			e.status = config.UpstreamDisabled
			e.reason = ""
			e.tools = nil
		} else if e.status == config.UpstreamDisabled {
			e.status = config.UpstreamStopped
		}
	}
	r.cfg = cfg
	r.timeout = timeoutOf(cfg)
	r.mu.Unlock()

	if logIt {
		after := r.names()
		r.log("upstream_reload", strings.Join(after, ","), "ok",
			detailOfChange(before, after))
	}
}

// names 返回当前上游名的快照。
func (r *Registry) names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

// detailOfChange 生成一条"变了什么"的审计说明。
//
// 只记集合差异，不记整份配置：审计文件是给人事后排障用的，
// 把 url / command 全量抄进去既长又会把令牌之类的敏感串带进日志。
func detailOfChange(before, after []string) string {
	in := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	var added, removed []string
	for _, n := range after {
		if !in(before, n) {
			added = append(added, n)
		}
	}
	for _, n := range before {
		if !in(after, n) {
			removed = append(removed, n)
		}
	}
	switch {
	case len(added) == 0 && len(removed) == 0:
		return fmt.Sprintf("上游集合未变（%d 个）", len(after))
	case len(removed) == 0:
		return "新增: " + strings.Join(added, ",")
	case len(added) == 0:
		return "移除: " + strings.Join(removed, ",")
	default:
		return "新增: " + strings.Join(added, ",") + "；移除: " + strings.Join(removed, ",")
	}
}

// orderDirty 判断新配置里是否出现了注册表不知道的上游名。
func (r *Registry) orderDirty(cfg *config.Config) bool {
	for _, uc := range cfg.Upstreams {
		if _, ok := r.entries[uc.Name]; !ok {
			return true
		}
	}
	return false
}

// Close 关闭所有上游连接（stdio 子进程会被回收）。
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range r.order {
		if e := r.entries[name]; e != nil && e.tr != nil {
			e.tr.close()
			e.tr = nil
		}
	}
}

// Init 探测所有启用的上游。启动时调用一次。
//
// 并发探测：一个不可达的上游最坏要等一个 timeout，串行会让启动
// 随上游数量线性变慢。
//
// 调用方应当给一个**有界**的 ctx（main.go 用 StartupProbeTimeout），
// 否则一个坏上游会把启动拖满一个 shellTimeoutSeconds。
func (r *Registry) Init(ctx context.Context) {
	r.ProbeAll(ctx)
}

// ProbeAll 重新探测所有启用的上游。
func (r *Registry) ProbeAll(ctx context.Context) {
	r.ProbeAllExcept(ctx, "")
}

// ProbeAllExcept 探测所有启用的上游，但跳过 skip 指定的那个（空串表示不跳过）。
//
// 为什么需要"跳过"：Restart 会自己重探目标上游，若再走一遍 ProbeAll，
// 同一个上游会被探测两次 —— 对 stdio 意味着反复 spawn/kill 同一个子进程。
func (r *Registry) ProbeAllExcept(ctx context.Context, skip string) {
	r.mu.RLock()
	names := append([]string(nil), r.order...)
	r.mu.RUnlock()

	var wg sync.WaitGroup
	for _, name := range names {
		if name == skip {
			continue
		}
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			r.probe(ctx, n)
		}(name)
	}
	wg.Wait()
}

// Probe 探测单个上游。名字不存在时报错。
func (r *Registry) Probe(ctx context.Context, name string) error {
	if !r.probe(ctx, name) {
		return fmt.Errorf("未知上游: %s", name)
	}
	return nil
}

// probe 探测一个上游并更新状态，返回该上游是否存在。
func (r *Registry) probe(ctx context.Context, name string) bool {
	r.mu.Lock()
	e := r.entries[name]
	if e == nil {
		r.mu.Unlock()
		return false
	}
	if e.status == config.UpstreamDisabled {
		r.mu.Unlock()
		return true
	}
	cfg := e.cfg
	existing := e.tr
	r.mu.Unlock()

	// 探测本身不持锁：它要发起网络/进程交互，可能耗时整个 timeout。
	status, reason, tools, tr, err := connect(ctx, cfg, r.timeout, existing)

	r.mu.Lock()
	// 期间可能被 Reload 换掉了，重新取一次。
	if cur := r.entries[name]; cur == e {
		if e.tr != nil && e.tr != tr {
			e.tr.close()
		}
		e.tr = tr
		e.status = status
		e.reason = reason
		// tools 是**上一次成功探测**的结果，不是"本次探测结果"。
		//
		// 探测失败时保留它是有意的：exposeWhenStopped 的语义是"未运行时
		// 也把工具列给客户端"，而停止的服务不可能回答 tools/list。
		// 若在这里清空，那个开关就永远无效 —— 一旦上游停过一次，
		// 工具列表就再也回不来了（直到它重新运行）。
		// 状态（status）反映当前存活，工具表反映最后已知的 schema，
		// 两者回答的是不同问题，不能共用一个赋值。
		if status == config.UpstreamRunning {
			e.tools = tools
		}
	} else if tr != nil {
		tr.close()
	}
	r.mu.Unlock()

	if err != nil {
		r.log("upstream_probe", name, status, err.Error())
	} else {
		r.log("upstream_probe", name, status, "")
	}
	return true
}

// Restart 关掉旧连接并强制重探指定上游。
//
// 若重探后仍未运行、且该上游配了可执行的 launch（intent / command），
// 就按 launch 把它拉起来再重探一次。**这是 WebUI「启动」按钮的实现** ——
// 拉起的逻辑只有这里一份，页面不重复实现 `am start`。
func (r *Registry) Restart(ctx context.Context, name string) error {
	r.mu.Lock()
	e := r.entries[name]
	if e == nil {
		r.mu.Unlock()
		return fmt.Errorf("未知上游: %s", name)
	}
	cfg := e.cfg
	if e.tr != nil {
		e.tr.close()
		e.tr = nil
	}
	e.status = config.UpstreamStopped
	e.reason = ""
	e.tools = nil
	r.mu.Unlock()

	r.log("upstream_restart", name, config.UpstreamStopped, "")

	if cfg.LaunchType() == config.LaunchManual {
		r.probe(ctx, name)
		return nil
	}

	// 已配置 launch 时直接走"拉起 + 等待 + 重探"，不再先白白探测一次：
	// 那一次必然失败（我们刚刚关掉了连接），只是多花一个 timeout。
	_, _ = r.launchAndWait(ctx, name)
	return nil
}

// Status 返回所有上游的状态快照，按配置顺序。
func (r *Registry) Status() []StatusInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]StatusInfo, 0, len(r.order))
	for _, name := range r.order {
		e := r.entries[name]
		if e == nil {
			continue
		}
		out = append(out, StatusInfo{
			Name:       e.cfg.Name,
			Type:       e.cfg.Type,
			Target:     targetOf(e.cfg),
			Status:     e.status,
			Enabled:    e.cfg.Enabled,
			Tools:      len(e.tools),
			Launch:     e.cfg.LaunchType(),
			AutoLaunch: e.cfg.AutoLaunch,
			Reason:     e.reason,
		})
	}
	return out
}

func targetOf(u config.UpstreamConfig) string {
	if u.Type == config.UpstreamTypeHTTP {
		return u.URL
	}
	parts := append([]string{u.Command}, u.Args...)
	return strings.Join(parts, " ")
}

// Has 判断某个上游名是否已注册。
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.entries[name]
	return ok
}

// Count 返回注册的上游数量（含 disabled）。
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.order)
}

// MergedTools / FullName / SplitName 见 merge.go ——
// 命名空间与合并规则集中在那一个文件里，不允许第二处实现。

func (r *Registry) log(event, name, result, detail string) {
	r.mu.RLock()
	l := r.audit
	r.mu.RUnlock()
	if l == nil {
		return
	}
	l.Log(audit.Entry{
		Event:  event,
		Tool:   "upstream:" + name,
		Result: result,
		Detail: detail,
	})
}

// rawResult 把 upstream 返回的 MCP result 解析成 Outcome。
func rawResult(raw json.RawMessage) (*Outcome, error) {
	if len(raw) == 0 {
		return &Outcome{}, nil
	}
	var out Outcome
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("上游返回的 result 不是 MCP 工具结果: %w", err)
	}
	return &out, nil
}
