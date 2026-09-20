package tools

import (
	"context"
	"encoding/json"
	"errors"
)

// registerUpstreamTools 注册上游聚合的观测工具。
//
// 与 health_tools.go 一样走直注册通路（不经过 v02 回调），因为它读的是
// tools.Deps 上的注册表，而不是 v02 的运行时上下文。
func registerUpstreamTools(reg *Registry, deps *Deps) {
	reg.Register(&Tool{
		Name:  "novaai_upstream_status",
		Title: "上游状态",
		Description: "读取所有上游 MCP 服务的连接状态与已合并的工具数。" +
			"AI 可据此提示用户启动未运行的上游。",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			d := DepsFromContext(ctx)
			if d == nil {
				return nil, errors.New("deps 未注入")
			}
			if d.Upstreams == nil {
				// 上游聚合未启用（测试或极简装配）。返回空列表而不是报错：
				// "没有上游"是合法状态，不是失败。
				return map[string]any{
					"success":   true,
					"upstreams": []any{},
					"tools":     0,
				}, nil
			}
			list := d.Upstreams.Status()
			return map[string]any{
				"success":   true,
				"upstreams": list,
				"tools":     d.Upstreams.ToolCount(),
			}, nil
		},
	})
}
