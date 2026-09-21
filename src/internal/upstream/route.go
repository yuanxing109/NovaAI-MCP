package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/profile"
)

// Authorize 判定一次上游工具调用是否被该上游的策略允许。
//
// 本包能施加的控制只有两条（其余交给上游自己）：
//
//   - denyTools：按**裸名**或**带前缀名**匹配，命中即拒。
//     两种写法都支持是有意的 —— WebUI 里用户看到的是带前缀的名，
//     手写配置时更自然的是裸名。
//   - riskCeiling：风险由 profile.ResolveRisk 按工具名与 action 推断
//     （未知工具名算 1）。所以它实际能挡的是"名字恰好命中本地风险表
//     且等级超过 ceiling"的工具，粒度有限 —— 这一条写在 docs/upstream.md
//     的已知限制里，不假装它是完备的权限模型。
//
// 返回的 risk 供审计使用。
func (r *Registry) Authorize(upstreamName, toolName string, args json.RawMessage) (risk int, err error) {
	r.mu.RLock()
	e := r.entries[upstreamName]
	r.mu.RUnlock()
	if e == nil {
		return 0, fmt.Errorf("未知上游: %s", upstreamName)
	}

	full := FullName(upstreamName, toolName)
	for _, d := range e.cfg.DenyTools {
		if d == toolName || d == full {
			return 0, fmt.Errorf("工具 %s 被上游 %s 的 denyTools 拒绝", full, upstreamName)
		}
	}

	risk = profile.ResolveRisk(toolName, args)
	if ceiling := e.cfg.EffectiveRiskCeiling(); risk > ceiling {
		return risk, fmt.Errorf("工具 %s 的风险等级 %d 超过上游 %s 的上限 %d",
			full, risk, upstreamName, ceiling)
	}
	return risk, nil
}

// Call 路由一次上游工具调用。
//
// 流程与方案一致：
//
//	查状态 → 非 running 时**实时探测一次**
//	  ├ running  → 转发
//	  ├ stopped  → autoLaunch && 有 launch 配置 → 拉起 → 等 2s → 重探 → 转发或失败
//	  ├ error    → 返回原因
//	  └ disabled → 返回"已禁用"
func (r *Registry) Call(ctx context.Context, upstreamName, toolName string, args json.RawMessage) (*Outcome, error) {
	status, reason := r.statusOf(upstreamName)
	if status == "" {
		return nil, fmt.Errorf("未知上游: %s", upstreamName)
	}

	if status != config.UpstreamRunning {
		// 方案 §2.4：调用时若状态非 running，实时探测一次（不做后台轮询）。
		r.probe(ctx, upstreamName)
		status, reason = r.statusOf(upstreamName)
	}

	if status == config.UpstreamDisabled {
		return nil, fmt.Errorf("upstream %s 已禁用", upstreamName)
	}

	if status != config.UpstreamRunning {
		r.mu.RLock()
		e := r.entries[upstreamName]
		auto := e != nil && e.cfg.AutoLaunch
		hasLaunch := e != nil && e.cfg.LaunchType() != config.LaunchManual
		r.mu.RUnlock()

		if status == config.UpstreamStopped && auto && hasLaunch {
			ok, why := r.launchAndWait(ctx, upstreamName)
			if !ok {
				return nil, fmt.Errorf("upstream %s 启动失败: %s", upstreamName, why)
			}
			status, reason = r.statusOf(upstreamName)
		}
	}

	if status != config.UpstreamRunning {
		if status == config.UpstreamStopped {
			return nil, fmt.Errorf(
				"upstream %s 未运行（stopped），请先手动启动%s",
				upstreamName, hintAutoLaunch(reason))
		}
		return nil, fmt.Errorf("upstream %s 异常（error）: %s", upstreamName, reason)
	}

	start := time.Now()
	raw, err := r.forward(ctx, upstreamName, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": rawArgs(args),
	})
	if err != nil {
		// 转发失败：状态可能已经变了（进程死了、端口关了）。
		// 立刻重探一次，让状态反映现实，而不是留着一个陈旧的 running。
		r.probe(ctx, upstreamName)
		r.log("upstream_call", upstreamName, "error", err.Error())
		return nil, fmt.Errorf("转发到 upstream %s 失败: %w", upstreamName, err)
	}

	out, perr := rawResult(raw)
	if perr != nil {
		r.log("upstream_call", upstreamName, "error", perr.Error())
		return nil, perr
	}
	r.log("upstream_call", FullName(upstreamName, toolName), "ok",
		fmt.Sprintf("%dms", time.Since(start).Milliseconds()))
	return out, nil
}

// hintAutoLaunch 在停止原因后补一句可操作的提示。
func hintAutoLaunch(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return "（探测原因: " + reason + "）"
}

// forward 取当前连接并发一次请求。
//
// 转发与探测共用该上游的并发闸（maxConcurrent）：探测本身就是
// initialize + tools/list，也是对上游的真实负载。
func (r *Registry) forward(ctx context.Context, upstreamName, method string, params any) (json.RawMessage, error) {
	r.mu.RLock()
	e := r.entries[upstreamName]
	if e == nil {
		r.mu.RUnlock()
		return nil, fmt.Errorf("未知上游: %s", upstreamName)
	}
	tr := e.tr
	r.mu.RUnlock()
	if tr == nil {
		return nil, fmt.Errorf("upstream %s 没有可用连接", upstreamName)
	}

	if err := e.acquire(ctx, upstreamName); err != nil {
		return nil, err
	}
	defer e.release()

	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return tr.call(callCtx, method, params)
}

// statusOf 读取一个上游的状态与原因。第二个返回值为空串表示上游不存在。
func (r *Registry) statusOf(name string) (string, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e := r.entries[name]
	if e == nil {
		return "", ""
	}
	return e.status, e.reason
}

// rawArgs 归一化 arguments。
//
// MCP 允许 arguments 缺省；上游那边的 handler 通常期望一个对象而不是 null，
// 所以缺省时补一个空对象。空数组同理（有些客户端会发 `[]`）。
func rawArgs(args json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return json.RawMessage(`{}`)
	}
	return args
}
