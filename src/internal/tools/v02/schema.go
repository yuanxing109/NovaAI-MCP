package v02

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func enumProp(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": vals}
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func arrProp(desc string) map[string]any {
	return map[string]any{
		"type": "array", "description": desc,
		"items": map[string]any{"type": "string"},
	}
}

func ok(data any) map[string]any {
	return map[string]any{"success": true, "code": "OK", "data": data}
}

func okMsg(msg string) map[string]any {
	return map[string]any{"success": true, "code": "OK", "message": msg}
}

func errFail(code, msg string) map[string]any {
	return map[string]any{"success": false, "code": code, "message": msg}
}
