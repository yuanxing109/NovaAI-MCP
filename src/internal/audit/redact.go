package audit

import (
	"encoding/json"
	"strings"
)

// allowlistFields 是允许原样记录的参数名。其余参数一律脱敏为 "***"。
//
// 只有 allowlist 一种脱敏实现，没有可关闭脱敏的开关。
var allowlistFields = []string{"action", "path", "package", "name",
	"query", "url", "tool", "pattern", "cmd", "command"}

// argPreviewBytes 是单个参数值的截断长度。
const argPreviewBytes = 256

// BuildArgsPreview 生成一条脱敏后的参数预览。
func BuildArgsPreview(args json.RawMessage) any {
	if len(args) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return map[string]string{"_error": "unparseable"}
	}

	out := map[string]any{}
	for k, v := range raw {
		if !isAllowlisted(k) {
			out[k] = "***"
			continue
		}
		s := string(v)
		if len(s) > argPreviewBytes {
			s = s[:argPreviewBytes] + "..."
		}
		out[k] = s
	}
	return out
}

func isAllowlisted(key string) bool {
	for _, a := range allowlistFields {
		if strings.EqualFold(a, key) {
			return true
		}
	}
	return false
}
