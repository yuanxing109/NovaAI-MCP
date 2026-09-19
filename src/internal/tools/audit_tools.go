package tools

import (
	"context"
	"encoding/json"
	"errors"
)

func registerAuditTools(reg *Registry, deps *Deps) {
	reg.Register(&Tool{
		Name:        "novaai_audit_status",
		Title:       "审计状态",
		Description: "读取审计日志状态",
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
					"enabled":       d.Config.Audit.Enabled,
					"maxFileBytes":  d.Config.Audit.MaxFileBytes,
					"maxFiles":      d.Config.Audit.MaxFiles,
					"retentionDays": d.Config.Audit.RetentionDays,
				},
			}, nil
		},
	})
}
