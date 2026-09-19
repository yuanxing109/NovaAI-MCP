package tools

// fail 构造统一的业务失败结果。
//
// 约定：工具 Handler 返回 (map, nil) 表示"业务层失败"（例如参数非法），
// 这类结果会作为正常 tools/call 结果回给客户端；
// 返回 (nil, err) 才是协议级错误，会被包成 JSON-RPC error。
func fail(code, msg string) map[string]any {
	return map[string]any{"success": false, "code": code, "message": msg}
}
