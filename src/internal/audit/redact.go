package audit

import (
	"encoding/json"
	"strings"

	"github.com/novaai/novaai-mcp/internal/config"
)

func BuildArgsPreview(args json.RawMessage, cfg *config.AuditConfig) any {
	if len(args) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return map[string]string{"_error": "unparseable"}
	}

	out := map[string]any{}
	for k, v := range raw {
		if !isAllowlisted(k, cfg.AllowlistFields) {
			out[k] = "***"
			continue
		}
		s := string(v)
		if len(s) > cfg.ArgPreviewBytes {
			s = s[:cfg.ArgPreviewBytes] + "..."
		}
		out[k] = s
	}
	return out
}

func isAllowlisted(key string, allowlist []string) bool {
	for _, a := range allowlist {
		if strings.EqualFold(a, key) {
			return true
		}
	}
	return false
}
