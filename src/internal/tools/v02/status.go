package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func registerStatusTools(reg RegisterFn, deps *Deps) {
	// 无参数单动作工具：早期版本声明了 action("get")，handler 从不读它。
	reg("novaai_status", "服务状态", "读取服务版本、地址、档位与运行时间",
		objSchema(map[string]any{}),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			up := readUptime()
			v := deps.Version
			if v == "" {
				v = "unknown"
			}
			return ok(map[string]any{
				"uptimeSeconds": up,
				"version":       v,
				"goVersion":     runtime.Version(),
				"stateDir":      deps.StateDir,
				"address": map[string]any{
					"tcp":        deps.Config.Listen,
					"mcp":        "/mcp",
					"unixSocket": deps.Config.UnixSocket,
				},
				// 无鉴权：权限边界 = 网络可达性。改 listen 为 127.0.0.1:5322
				// 即可切本地模式。
				"security": map[string]any{
					"auth":    "none",
					"profile": deps.Config.Profile,
				},
				"adapter": deps.Adapter.Name(),
			}), nil
		})

	// 无参数单动作工具：早期版本声明了 action(get/probe)，但 handler
	// 只探测一次、不读 action —— get 与 probe 是同一个行为。
	reg("novaai_capabilities", "能力探测", "探测设备能力与降级原因",
		objSchema(map[string]any{}),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			commands := map[string]string{}
			missing := []string{}
			for _, c := range []string{
				"sh", "getprop", "setprop", "dumpsys", "am", "pm",
				"settings", "logcat", "input", "ip", "ss",
				"screencap", "screenrecord", "ping", "tar", "gzip",
				"chcon", "restorecon", "dmesg",
			} {
				if p, err := lookPath(c); err == nil {
					commands[c] = p
				} else {
					missing = append(missing, c)
				}
			}
			return ok(map[string]any{
				"runtime":  runtime.GOOS + "/" + runtime.GOARCH,
				"commands": commands,
				"missing":  missing,
				"adapter":  deps.Adapter.Name(),
			}), nil
		})

	reg("novaai_config", "服务配置", "读取、验证、原子更新、导出服务配置，或管理上游 MCP 聚合",
		objSchema(map[string]any{
			"action":      enumProp("明确操作", "get", "validate", "update", "export", "probe_upstreams", "restart_upstream", "reload_upstreams"),
			"config":      map[string]any{"type": "object"},
			"destination": map[string]any{"type": "string"},
			"name":        strProp("上游名（probe_upstreams 可选、restart_upstream 必填）"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action      string          `json:"action"`
				Config      json.RawMessage `json:"config"`
				Destination string          `json:"destination"`
				Name        string          `json:"name"`
			}
			_ = json.Unmarshal(args, &in)

			configPath := filepath.Join(deps.StateDir, "config.json")

			switch in.Action {
			case "get":
				raw, err := os.ReadFile(configPath)
				if err != nil {
					return nil, err
				}
				var cfg any
				_ = json.Unmarshal(raw, &cfg)
				return ok(map[string]any{"config": cfg}), nil
			case "validate":
				var cfg any
				if err := json.Unmarshal(in.Config, &cfg); err != nil {
					return errFail("INVALID_JSON", err.Error()), nil
				}
				return ok(map[string]any{"valid": true}), nil
			case "update":
				return updateConfig(configPath, in.Config)
			case "export":
				if in.Destination == "" {
					return errFail("MISSING_DEST", "destination 必填"), nil
				}
				raw, _ := os.ReadFile(configPath)
				target := resolvePath(deps, in.Destination)
				if err := guardPath(target, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := os.WriteFile(target, raw, 0600); err != nil {
					return nil, err
				}
				return ok(map[string]any{"path": target}), nil

			// ---- 上游 MCP 聚合 ----
			//
			// 这三个 action 是 WebUI 的主要入口：WebUI 直接原子改写
			// config.json 的 upstreams 数组，然后调 reload_upstreams
			// 让 daemon 立刻生效（不需要重启模块）。
			case "probe_upstreams":
				return probeUpstreams(ctx, in.Name, deps)
			case "reload_upstreams":
				return reloadUpstreams(ctx, configPath, deps)
			case "restart_upstream":
				return restartUpstream(ctx, in.Name, deps)
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	reg("novaai_diagnostics", "自检诊断", "执行自检或生成完整诊断报告",
		objSchema(map[string]any{
			"action": enumProp("明确操作", "self_test", "collect"),
			"path":   map[string]any{"type": "string"},
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Path   string `json:"path"`
			}
			_ = json.Unmarshal(args, &in)

			if in.Action == "collect" {
				target := resolvePath(deps, in.Path)
				if in.Path == "" {
					target = filepath.Join(deps.Config.WorkspaceRoot(),
						"diagnostics-"+time.Now().Format("20060102-150405")+".txt")
				}
				if err := guardPath(target, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				_ = os.MkdirAll(filepath.Dir(target), 0700)

				var b strings.Builder
				b.WriteString("=== NovaAI-MCP Diagnostics ===\n")
				b.WriteString("time: " + time.Now().Format(time.RFC3339) + "\n")
				b.WriteString("state: " + deps.StateDir + "\n")
				b.WriteString("adapter: " + deps.Adapter.Name() + "\n\n")

				for _, c := range []string{"id", "uname -a", "getprop ro.build.version.release", "getenforce"} {
					out, _, _, _ := runSh(ctx, deps, "novaai_diagnostics", c, 5*time.Second)
					b.WriteString("$ " + c + "\n" + out + "\n")
				}

				_ = os.WriteFile(target, []byte(b.String()), 0600)
				return ok(map[string]any{"path": target}), nil
			}

			if in.Action != "self_test" {
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			return ok(map[string]any{
				"stateDir":      deps.StateDir,
				"adapter":       deps.Adapter.Name(),
				"stateWritable": writable(deps.StateDir),
			}), nil
		})
}

// ---- 内部小工具 ----

func readUptime() int {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	var intPart, fracPart int
	_, _ = fmt.Sscanf(f[0], "%d.%d", &intPart, &fracPart)
	return intPart
}

func lookPath(name string) (string, error) {
	for _, dir := range []string{
		"/system/bin", "/system/xbin", "/system/sbin",
		"/vendor/bin", "/data/adb/ksu/bin", "/data/adb/magisk",
	} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

func writable(dir string) bool {
	testFile := filepath.Join(dir, ".write_test")
	if err := os.WriteFile(testFile, []byte("t"), 0600); err != nil {
		return false
	}
	_ = os.Remove(testFile)
	return true
}
