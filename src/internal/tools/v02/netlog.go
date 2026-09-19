package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func registerNetLogTools(reg RegisterFn, deps *Deps) {
	// ---- network ----
	reg("novaai_network", "网络操作", "网络诊断、持久 HTTP/Cookie、HTML/浏览器捕获、RSS/Atom 与 WebSocket",
		objSchema(map[string]any{
			"action":           strProp("操作"),
			"url":              strProp("URL"),
			"method":           strProp("HTTP 方法"),
			"headers":          map[string]any{"type": "object"},
			"body":             strProp("请求体"),
			"host":             strProp("主机"),
			"port":             intProp("端口"),
			"timeoutMs":        intProp("超时毫秒"),
			"maxResponseBytes": intProp("最大响应字节数"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action           string            `json:"action"`
				URL              string            `json:"url"`
				Method           string            `json:"method"`
				Headers          map[string]string `json:"headers"`
				Body             string            `json:"body"`
				Host             string            `json:"host"`
				Port             int               `json:"port"`
				TimeoutMs        int               `json:"timeoutMs"`
				MaxResponseBytes int               `json:"maxResponseBytes"`
			}
			_ = json.Unmarshal(args, &in)

			if in.TimeoutMs <= 0 {
				in.TimeoutMs = 15000
			}
			if in.MaxResponseBytes <= 0 {
				in.MaxResponseBytes = 1048576
			}

			switch in.Action {
			case "interfaces":
				out, _, _, _ := runSh(ctx, deps, "novaai_network", "ip addr", 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "routes":
				out, _, _, _ := runSh(ctx, deps, "novaai_network", "ip route", 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "dns":
				out, _, _, _ := runSh(ctx, deps, "novaai_network",
					"getprop | grep dns", 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "ping":
				out, _, _, _ := runSh(ctx, deps, "novaai_network",
					fmt.Sprintf("ping -c 3 -W 5 %s", shQuote(in.Host)), 20*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "resolve":
				out, _, _, _ := runSh(ctx, deps, "novaai_network",
					fmt.Sprintf("nslookup %s 2>/dev/null || getprop net.dns1", shQuote(in.Host)), 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "http":
				if in.URL == "" {
					return errFail("MISSING_URL", "url 必填"), nil
				}
				var headerArgs strings.Builder
				for k, v := range in.Headers {
					headerArgs.WriteString(fmt.Sprintf(" -H %s", shQuote(k+": "+v)))
				}
				m := in.Method
				if m == "" {
					m = "GET"
				}
				bodyArg := ""
				if in.Body != "" {
					bodyArg = fmt.Sprintf(" -d %s", shQuote(in.Body))
				}
				cmd := fmt.Sprintf("curl -sS -fL -X %s --max-time %d%s%s %s",
					m, in.TimeoutMs/1000, headerArgs.String(), bodyArg, shQuote(in.URL))
				out, errOut, code, _ := runSh(ctx, deps, "novaai_network", cmd,
					time.Duration(in.TimeoutMs)*time.Millisecond)
				if code != 0 {
					return errFail("HTTP_FAILED", errOut), nil
				}
				if len(out) > in.MaxResponseBytes {
					out = out[:in.MaxResponseBytes]
				}
				return ok(map[string]any{"body": out}), nil
			case "ports":
				out, _, _, _ := runSh(ctx, deps, "novaai_network",
					"ss -tuln 2>/dev/null || netstat -tuln 2>/dev/null", 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "connections":
				out, _, _, _ := runSh(ctx, deps, "novaai_network",
					"ss -tun 2>/dev/null || netstat -tun 2>/dev/null", 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "wifi":
				out, _, _, _ := runSh(ctx, deps, "novaai_network",
					"dumpsys wifi | grep -E 'SSID|BSSID|IP|Signal' | head -20", 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "proxy":
				out, _, _, _ := runSh(ctx, deps, "novaai_network",
					"settings get global http_proxy", 5*time.Second)
				return ok(map[string]any{"proxy": strings.TrimSpace(out)}), nil
			case "connectivity":
				out, _, _, _ := runSh(ctx, deps, "novaai_network",
					"dumpsys connectivity | head -50", 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			}
			return errFail("NOT_IMPLEMENTED", in.Action), nil
		})

	// ---- log ----
	reg("novaai_log", "日志", "读取 Logcat、内核、dmesg、模块/MCP 日志或实时流",
		objSchema(map[string]any{
			"action":     enumProp("操作", "logcat", "kernel", "dmesg", "module", "mcp", "stream", "clear"),
			"lines":      intProp("行数"),
			"follow":     boolProp("持续读取"),
			"timeoutMs":  intProp("超时毫秒"),
			"path":       strProp("文件路径"),
			"target":     strProp("目标"),
			"background": boolProp("后台"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Lines     int    `json:"lines"`
				Follow    bool   `json:"follow"`
				TimeoutMs int    `json:"timeoutMs"`
				Path      string `json:"path"`
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
				if in.Follow {
					return errFail("NEED_BACKGROUND", "follow 模式需 background: true"), nil
				}
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
