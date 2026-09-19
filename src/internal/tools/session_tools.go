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
		Description: "读取本次请求的会话与身份信息",
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
			sessionID := SessionIDFromContext(ctx)
			data := map[string]any{
				"sessionId": sessionID,
				"profile":   ProfileFromContext(ctx),
				// tracked=false 表示本次请求没有携带 Mcp-Session-Id，
				// 服务按无状态处理（不占用会话名额）。这是正常状态，不是错误。
				"tracked": sessionID != "",
			}
			if sessionID != "" {
				if s, err := d.Sessions.Get(sessionID); err == nil {
					data["createdAt"] = s.CreatedAt.Format(time.RFC3339)
					data["lastSeenAt"] = s.LastSeenAt.Format(time.RFC3339)
				}
			}
			return map[string]any{"success": true, "data": data}, nil
		},
	})

	reg.Register(&Tool{
		Name:        "novaai_session_list",
		Title:       "列出所有会话",
		Description: "列出当前所有已登记的活跃会话",
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
