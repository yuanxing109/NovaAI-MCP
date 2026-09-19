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

	// ---- service ----
	reg("novaai_service", "系统服务", "管理 Binder、应用 Service 与 init 服务",
		objSchema(map[string]any{
			"action":      enumProp("操作", "binder_list", "binder_call", "app_start", "app_stop", "init_status", "init_start", "init_stop", "init_restart"),
			"service":     strProp("服务名"),
			"package":     strProp("包名"),
			"component":   strProp("组件名"),
			"transaction": intProp("Binder transaction code"),
			"arguments":   arrProp("参数数组"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action      string   `json:"action"`
				Service     string   `json:"service"`
				Package     string   `json:"package"`
				Component   string   `json:"component"`
				Transaction int      `json:"transaction"`
				Arguments   []string `json:"arguments"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "binder_list":
				cmd = []string{"service", "list"}
			case "binder_call":
				if in.Service == "" || in.Transaction == 0 {
					return errFail("MISSING_PARAM", "service 和 transaction 必填"), nil
				}
				cmd = []string{"service", "call", in.Service, fmt.Sprint(in.Transaction)}
				cmd = append(cmd, in.Arguments...)
			case "app_start":
				cmd = []string{"am", "startservice", "-n", in.Component}
			case "app_stop":
				cmd = []string{"am", "stopservice", "-n", in.Component}
			case "init_status":
				cmd = []string{"getprop", "init.svc." + in.Service}
			case "init_start":
				cmd = []string{"setprop", "ctl.start", in.Service}
			case "init_stop":
				cmd = []string{"setprop", "ctl.stop", in.Service}
			case "init_restart":
				cmd = []string{"setprop", "ctl.restart", in.Service}
			default:
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}
			if len(cmd) == 0 {
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_service",
				cmd[0], cmd[1:], 20*time.Second)
			if err != nil {
				return errFail("EXEC_FAILED", err.Error()), nil
			}
			return map[string]any{
				"success":  code == 0,
				"code":     "OK",
				"stdout":   out,
				"stderr":   errOut,
				"exitCode": code,
			}, nil
		})

	// ---- property ----
	reg("novaai_property", "系统属性", "读取、列出或设置 Android 属性",
		objSchema(map[string]any{
			"action": enumProp("操作", "get", "list", "set"),
			"key":    strProp("属性名"),
			"value":  strProp("属性值"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Key    string `json:"key"`
				Value  string `json:"value"`
			}
			_ = json.Unmarshal(args, &in)

			switch in.Action {
			case "get":
				out, _, _, _ := runCmd(ctx, deps, "novaai_property", "getprop",
					[]string{in.Key}, 10*time.Second)
				return ok(map[string]any{"key": in.Key, "value": strings.TrimSpace(out)}), nil
			case "list":
				out, _, _, _ := runCmd(ctx, deps, "novaai_property", "getprop",
					nil, 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "set":
				if in.Key == "" {
					return errFail("MISSING_KEY", "key 必填"), nil
				}
				_, errOut, code, _ := runCmd(ctx, deps, "novaai_property", "setprop",
					[]string{in.Key, in.Value}, 10*time.Second)
				if code != 0 {
					return errFail("SETPROP_FAILED", errOut), nil
				}
				return okMsg("已设置"), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- setting ----
	reg("novaai_setting", "系统设置", "读取、列出、写入或删除 Android Settings",
		objSchema(map[string]any{
			"action":    enumProp("操作", "get", "list", "put", "delete"),
			"namespace": enumProp("命名空间", "system", "secure", "global"),
			"key":       strProp("设置项名"),
			"value":     strProp("设置值"),
			"user":      intProp("用户 ID"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Namespace string `json:"namespace"`
				Key       string `json:"key"`
				Value     string `json:"value"`
				User      int    `json:"user"`
			}
			_ = json.Unmarshal(args, &in)

			if in.Namespace == "" {
				in.Namespace = "system"
			}

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"settings", "get", in.Namespace, in.Key}
			case "list":
				cmd = []string{"settings", "list", in.Namespace}
			case "put":
				cmd = []string{"settings", "put", in.Namespace, in.Key, in.Value}
			case "delete":
				cmd = []string{"settings", "delete", in.Namespace, in.Key}
			default:
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}
			if len(cmd) == 0 {
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_setting",
				cmd[0], cmd[1:], 15*time.Second)
			if err != nil {
				return errFail("EXEC_FAILED", err.Error()), nil
			}
			return map[string]any{
				"success":  code == 0,
				"code":     "OK",
				"stdout":   strings.TrimSpace(out),
				"stderr":   errOut,
				"exitCode": code,
			}, nil
		})
}
