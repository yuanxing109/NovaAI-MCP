package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/novaai/novaai-mcp/internal/health"
)

func registerHealthTools(reg *Registry, deps *Deps) {
	reg.Register(&Tool{
		Name:        "novaai_health_status",
		Title:       "健康状态",
		Description: "读取服务健康状态",
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
			snapshot := health.Snapshot()
			return map[string]any{
				"success": true,
				"data":    snapshot,
			}, nil
		},
	})
}
