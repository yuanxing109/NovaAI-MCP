package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func registerSessionTools(reg *Registry, _ *Deps) {
	reg.Register(&Tool{
		Name:        "novaai_session_status",
		Title:       "会话状态",
		Description: "读取当前会话状态",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			sessionID := SessionIDFromContext(ctx)
			d := DepsFromContext(ctx)
			if d == nil {
				return nil, errors.New("deps 未注入")
			}
			s, err := d.Sessions.Get(sessionID)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"success": true,
				"data": map[string]any{
					"sessionId":     s.ID,
					"profile":       s.Profile,
					"createdAt":     s.CreatedAt.Format(time.RFC3339),
					"lastSeenAt":    s.LastSeenAt.Format(time.RFC3339),
					"uploadBytes":   s.UploadBytes,
					"downloadBytes": s.DownloadBytes,
				},
			}, nil
		},
	})

	reg.Register(&Tool{
		Name:        "novaai_session_list",
		Title:       "列出所有会话",
		Description: "列出当前所有活跃会话",
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
			return map[string]any{"success": true, "sessions": d.Sessions.List()}, nil
		},
	})
}
