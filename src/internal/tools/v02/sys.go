package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func registerSysTools(reg RegisterFn, deps *Deps) {
	// ---- process ----
	reg("novaai_process", "进程管理", "列出或检查进程，发送信号、调整优先级、查看 fd",
		objSchema(map[string]any{
			"action":   enumProp("操作", "list", "info", "signal", "kill", "renice", "fds"),
			"pid":      intProp("进程 ID"),
			"query":    strProp("搜索关键词"),
			"package":  strProp("包名"),
			"signal":   map[string]any{"oneOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}},
			"priority": intProp("nice 值"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action   string `json:"action"`
				PID      int    `json:"pid"`
				Query    string `json:"query"`
				Package  string `json:"package"`
				Signal   any    `json:"signal"`
				Priority int    `json:"priority"`
			}
			_ = json.Unmarshal(args, &in)

			switch in.Action {
			case "list":
				out, _, _, _ := runSh(ctx, deps, "novaai_process",
					"ps -A -o PID,PPID,UID,NAME 2>/dev/null || ps -A", 15*time.Second)
				if in.Query != "" {
					var filtered []string
					for _, l := range splitLines(out) {
						if strings.Contains(l, in.Query) {
							filtered = append(filtered, l)
						}
					}
					return ok(map[string]any{"lines": filtered}), nil
				}
				return ok(map[string]any{"raw": out}), nil
			case "info":
				if in.PID <= 0 {
					return errFail("MISSING_PID", "pid 必填"), nil
				}
				out, _, _, _ := runSh(ctx, deps, "novaai_process",
					fmt.Sprintf("cat /proc/%d/status", in.PID), 10*time.Second)
				return ok(map[string]any{"status": out}), nil
			case "signal", "kill":
				if in.PID <= 0 {
					return errFail("MISSING_PID", "pid 必填"), nil
				}
				var sig string
				switch v := in.Signal.(type) {
				case float64:
					sig = fmt.Sprint(int(v))
				case string:
					sig = v
				default:
					sig = "TERM"
				}
				out, errOut, code, _ := runSh(ctx, deps, "novaai_process",
					fmt.Sprintf("kill -%s %d", shQuote(sig), in.PID), 10*time.Second)
				if code != 0 {
					return errFail("KILL_FAILED", errOut), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "renice":
				if in.PID <= 0 {
					return errFail("MISSING_PID", "pid 必填"), nil
				}
				out, errOut, code, _ := runSh(ctx, deps, "novaai_process",
					fmt.Sprintf("renice %d -p %d", in.Priority, in.PID), 10*time.Second)
				if code != 0 {
					return errFail("RENICE_FAILED", errOut), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "fds":
				if in.PID <= 0 {
					return errFail("MISSING_PID", "pid 必填"), nil
				}
				out, _, _, _ := runSh(ctx, deps, "novaai_process",
					fmt.Sprintf("ls -la /proc/%d/fd 2>/dev/null", in.PID), 10*time.Second)
				return ok(map[string]any{"fds": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}
