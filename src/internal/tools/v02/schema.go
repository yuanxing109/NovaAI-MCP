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

// ok / okMsg / errFail / execResult 是**所有**工具结果的四个出口。
//
// 它们都过 withCodeName（见 codes.go）：`code` 保持英文供程序匹配，
// 同时附一个稳定中文名 `codeName` 供人和 AI 阅读。
// 新增出口时必须也过一遍，否则那个出口的结果就没有中文名 ——
// codes_test.go 会扫本包的代码，漏掉的情况会被抓出来。

func ok(data any) map[string]any {
	return withCodeName(map[string]any{"success": true, "code": CodeOK, "data": data})
}

func okMsg(msg string) map[string]any {
	return withCodeName(map[string]any{"success": true, "code": CodeOK, "message": msg})
}

func errFail(code, msg string) map[string]any {
	return withCodeName(map[string]any{"success": false, "code": code, "message": msg})
}

// execResult 构造"命令跑完了"的结果。
//
// 这一个出口替掉了三处手写的同形 map（`app.go` 与 `exec.go` 两处）——
// 它们当初各写一遍 `"code": "OK"`，于是自动中文名接不上。
//
// 注意 success 与 code 在这里**不同义**：`success` 说的是"命令退出码为 0"，
// `code` 说的是"这次工具调用本身没出错"。所以退出码非 0 时会出现
// `success:false` + `code:"OK"`。这是既有语义，此处只是把它收到一处，
// 不改行为。
func execResult(stdout, stderr string, exitCode int) map[string]any {
	return withCodeName(map[string]any{
		"success":  exitCode == 0,
		"code":     CodeOK,
		"stdout":   stdout,
		"stderr":   stderr,
		"exitCode": exitCode,
	})
}
