package tools

import (
	"context"
	"encoding/json"

	"github.com/novaai/novaai-mcp/internal/tools/v02"
)

func RegisterAll(reg *Registry, deps *Deps) {
	// v0.02 系列工具，通过回调注册避免循环依赖。
	// 权威数量以 Registry.Count() 为准，这里不写死数字（会漂移）。
	v02.RegisterAllV02Tools(func(name, title, desc string, schema map[string]any, handler v02.Handler) {
		reg.Register(&Tool{
			Name:        name,
			Title:       title,
			Description: desc,
			InputSchema: schema,
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				return handler(ctx, args)
			},
		})
	}, &v02.Deps{
		Config:    deps.Config,
		Adapter:   deps.Adapter,
		StateDir:  deps.StateDir,
		Version:   deps.Version,
		Commit:    deps.Commit,
		Upstreams: deps.Upstreams,
	})

	// 独立注册的工具（不经过 v02 回调通路）：
	//   novaai_health_status
	//   novaai_upstream_status
	registerHealthTools(reg, deps)
	registerUpstreamTools(reg, deps)
}
