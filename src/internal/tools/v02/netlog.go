package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func registerNetLogTools(reg RegisterFn, deps *Deps) {
	// ---- log ----
	reg("novaai_log", "日志", "读取 Logcat、内核、dmesg、模块/MCP 日志或日志快照",
		objSchema(map[string]any{
			"action":    enumProp("操作", "logcat", "kernel", "dmesg", "module", "mcp", "stream", "clear"),
			"lines":     intProp("行数"),
			"timeoutMs": intProp("超时毫秒"),
			"target":    strProp("目标"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Lines     int    `json:"lines"`
				TimeoutMs int    `json:"timeoutMs"`
				Target    string `json:"target"`
			}
			_ = json.Unmarshal(args, &in)

			if in.Lines <= 0 {
				in.Lines = 100
			}
			if in.TimeoutMs <= 0 {
				in.TimeoutMs = 15000
			}

			switch in.Action {
			case "logcat":
				cmd := fmt.Sprintf("logcat -d -t %d -b main,crash,system", in.Lines)
				if in.Target != "" {
					cmd = fmt.Sprintf("logcat -d -t %d --pid=%s", in.Lines, shQuote(in.Target))
				}
				out, _, _, _ := runSh(ctx, deps, "novaai_log", cmd,
					time.Duration(in.TimeoutMs)*time.Millisecond)
				return ok(map[string]any{"raw": out}), nil
			case "kernel":
				out, _, _, _ := runSh(ctx, deps, "novaai_log", "dmesg -T | tail -n "+itoa(in.Lines), 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "dmesg":
				out, _, _, _ := runSh(ctx, deps, "novaai_log", "dmesg | tail -n "+itoa(in.Lines), 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "module":
				out, _, _, _ := runSh(ctx, deps, "novaai_log",
					fmt.Sprintf("tail -n %d %s/module.log", in.Lines, shQuote(deps.StateDir)), 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "mcp":
				out, _, _, _ := runSh(ctx, deps, "novaai_log",
					fmt.Sprintf("tail -n %d %s/novaaimcpd.log", in.Lines, shQuote(deps.StateDir)), 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "stream":
				// 一次性读取最近 N 行。持续跟随（follow）需要后台执行能力，
				// 本服务没有实现，因此 schema 不再暴露该参数 —— 一个恒定
				// 返回 NOT_IMPLEMENTED 的参数是骗人的契约。
				out, _, _, _ := runSh(ctx, deps, "novaai_log",
					fmt.Sprintf("logcat -t %d", in.Lines),
					time.Duration(in.TimeoutMs)*time.Millisecond)
				return ok(map[string]any{"raw": out}), nil
			case "clear":
				out, _, _, _ := runSh(ctx, deps, "novaai_log", "logcat -c", 5*time.Second)
				return ok(map[string]any{"output": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}
