package tools

import (
	"context"
	"encoding/json"
	"errors"
)

func registerAuthTools(reg *Registry, deps *Deps) {
	reg.Register(&Tool{
		Name:        "novaai_auth_status",
		Title:       "认证状态",
		Description: "读取当前认证状态",
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
			return map[string]any{
				"success": true,
				"data": map[string]any{
					"tokenEnabled": d.Config.Security.Token.Enabled,
					"lanEnabled":   d.Config.Security.LAN.Enabled,
					"anonymous":    d.Config.Security.Anonymous,
				},
			}, nil
		},
	})
}
