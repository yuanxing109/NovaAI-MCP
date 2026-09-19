package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func registerAppTools(reg RegisterFn, deps *Deps) {
	// ---- app_list ----
	reg("novaai_app_list", "应用列表", "按显示名或包名查询普通、系统或指定用户应用",
		objSchema(map[string]any{
			"action": enumProp("操作", "list"),
			"query":  strProp("搜索关键词"),
			"system": boolProp("仅系统应用"),
			"limit":  intProp("返回上限"),
			"offset": intProp("分页偏移"),
			"user":   intProp("Android 用户 ID"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Query  string `json:"query"`
				System bool   `json:"system"`
				Limit  int    `json:"limit"`
				Offset int    `json:"offset"`
				User   int    `json:"user"`
			}
			_ = json.Unmarshal(args, &in)

			if in.Limit <= 0 {
				in.Limit = 50
			}

			cmd := "pm list packages"
			if in.System {
				cmd += " -s"
			}
			if in.User > 0 {
				cmd += fmt.Sprintf(" --user %d", in.User)
			}

			out, _, _, err := runSh(ctx, deps, "novaai_app_list", cmd, 15*time.Second)
			if err != nil {
				return errFail("LIST_FAILED", err.Error()), nil
			}

			var pkgs []string
			for _, l := range splitLines(out) {
				p := strings.TrimPrefix(l, "package:")
				if in.Query != "" && !strings.Contains(p, in.Query) {
					continue
				}
				pkgs = append(pkgs, p)
			}

			total := len(pkgs)
			start := in.Offset
			if start > total {
				start = total
			}
			end := start + in.Limit
			if end > total {
				end = total
			}

			return ok(map[string]any{
				"total":    total,
				"offset":   start,
				"limit":    in.Limit,
				"packages": pkgs[start:end],
			}), nil
		})

	// ---- app_info ----
	reg("novaai_app_info", "应用信息", "读取应用、组件与权限详情",
		objSchema(map[string]any{
			"action":  enumProp("操作", "get", "components", "permissions"),
			"package": strProp("包名"),
			"user":    intProp("用户 ID"),
		}, "action", "package"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Package string `json:"package"`
				User    int    `json:"user"`
			}
			_ = json.Unmarshal(args, &in)

			switch in.Action {
			case "get":
				out, _, _, err := runSh(ctx, deps, "novaai_app_info",
					fmt.Sprintf("dumpsys package %s", shQuote(in.Package)), 20*time.Second)
				if err != nil {
					return errFail("INFO_FAILED", err.Error()), nil
				}
				// 提取关键信息
				info := map[string]any{"package": in.Package}
				for _, l := range splitLines(out) {
					l = strings.TrimSpace(l)
					if strings.HasPrefix(l, "versionName=") {
						info["versionName"] = strings.TrimPrefix(l, "versionName=")
					}
					if strings.HasPrefix(l, "versionCode=") {
						info["versionCode"] = strings.TrimPrefix(l, "versionCode=")
					}
					if strings.HasPrefix(l, "codePath=") {
						info["codePath"] = strings.TrimPrefix(l, "codePath=")
					}
					if strings.HasPrefix(l, "dataDir=") {
						info["dataDir"] = strings.TrimPrefix(l, "dataDir=")
					}
					if strings.HasPrefix(l, "primaryCpuAbi=") {
						info["abi"] = strings.TrimPrefix(l, "primaryCpuAbi=")
					}
				}
				return ok(info), nil
			case "components":
				out, _, _, _ := runSh(ctx, deps, "novaai_app_info",
					fmt.Sprintf("dumpsys package %s | grep -E 'Activity|Service|Receiver|Provider'",
						shQuote(in.Package)), 20*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "permissions":
				out, _, _, _ := runSh(ctx, deps, "novaai_app_info",
					fmt.Sprintf("dumpsys package %s | grep -A 200 'requested permissions'",
						shQuote(in.Package)), 20*time.Second)
				return ok(map[string]any{"raw": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- app_install ----
	reg("novaai_app_install", "安装应用", "通过 Package Manager 安装 APK/Split/APKS/XAPK",
		objSchema(map[string]any{
			"action":                  enumProp("操作", "apk", "split", "apks", "xapk", "session"),
			"path":                    strProp("APK 路径"),
			"paths":                   arrProp("多 APK 路径"),
			"package":                 strProp("包名"),
			"downgrade":               boolProp("允许降级"),
			"replace":                 boolProp("替换安装"),
			"grantRuntimePermissions": boolProp("自动授权"),
			"user":                    intProp("用户 ID"),
			"background":              boolProp("后台"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string   `json:"action"`
				Path      string   `json:"path"`
				Paths     []string `json:"paths"`
				Downgrade bool     `json:"downgrade"`
				Replace   bool     `json:"replace"`
				Grant     bool     `json:"grantRuntimePermissions"`
				User      int      `json:"user"`
			}
			_ = json.Unmarshal(args, &in)

			var apkPath string
			if in.Path != "" {
				apkPath = resolvePath(deps, in.Path)
			} else if len(in.Paths) > 0 {
				apkPath = resolvePath(deps, in.Paths[0])
			} else {
				return errFail("MISSING_PATH", "path 必填"), nil
			}

			args2 := []string{"install"}
			if in.Replace {
				args2 = append(args2, "-r")
			}
			if in.Downgrade {
				args2 = append(args2, "-d")
			}
			if in.Grant {
				args2 = append(args2, "-g")
			}
			if in.User > 0 {
				args2 = append(args2, "--user", fmt.Sprint(in.User))
			}
			args2 = append(args2, apkPath)

			out, errOut, code, err := runCmd(ctx, deps, "novaai_app_install", "pm", args2, 3*time.Minute)
			if err != nil || code != 0 {
				return errFail("INSTALL_FAILED", errOut), nil
			}
			return ok(map[string]any{"output": out, "exitCode": code}), nil
		})

	// ---- app_manage ----
	reg("novaai_app_manage", "管理应用", "启动、停止、清缓存、启停、卸载应用",
		objSchema(map[string]any{
			"action":    enumProp("操作", "launch", "stop", "clear_cache", "clear_data", "enable", "disable", "uninstall", "user"),
			"package":   strProp("包名"),
			"component": strProp("组件名"),
			"keepData":  boolProp("卸载保留数据"),
			"user":      intProp("用户 ID"),
		}, "action", "package"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Package   string `json:"package"`
				Component string `json:"component"`
				KeepData  bool   `json:"keepData"`
				User      int    `json:"user"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "launch":
				target := in.Package
				if in.Component != "" {
					target = in.Component
				}
				cmd = []string{"am", "start", "-n", target}
			case "stop":
				cmd = []string{"am", "force-stop", in.Package}
			case "clear_cache":
				cmd = []string{"pm", "clear", in.Package}
			case "clear_data":
				cmd = []string{"pm", "clear", in.Package}
			case "enable":
				cmd = []string{"pm", "enable", in.Package}
			case "disable":
				cmd = []string{"pm", "disable", in.Package}
			case "uninstall":
				if in.KeepData {
					cmd = []string{"pm", "uninstall", "-k", in.Package}
				} else {
					cmd = []string{"pm", "uninstall", in.Package}
				}
			case "user":
				cmd = []string{"pm", "list", "packages", "--user", fmt.Sprint(in.User)}
			default:
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_app_manage",
				cmd[0], cmd[1:], 60*time.Second)
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

	// ---- app_permission ----
	reg("novaai_app_permission", "应用权限", "列出、授予、撤销权限或管理 AppOps",
		objSchema(map[string]any{
			"action":     enumProp("操作", "list", "grant", "revoke", "appops"),
			"package":    strProp("包名"),
			"permission": strProp("权限名"),
			"mode":       strProp("AppOps 模式"),
			"user":       intProp("用户 ID"),
		}, "action", "package"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action     string `json:"action"`
				Package    string `json:"package"`
				Permission string `json:"permission"`
				Mode       string `json:"mode"`
				User       int    `json:"user"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "list":
				cmd = []string{"dumpsys", "package", in.Package}
			case "grant":
				cmd = []string{"pm", "grant", in.Package, in.Permission}
			case "revoke":
				cmd = []string{"pm", "revoke", in.Package, in.Permission}
			case "appops":
				if in.Mode == "" {
					in.Mode = "allow"
				}
				cmd = []string{"appops", "set", in.Package, in.Permission, in.Mode}
			default:
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_app_permission",
				cmd[0], cmd[1:], 30*time.Second)
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

	// ---- app_export ----
	reg("novaai_app_export", "导出应用", "导出 APK、Split 或应用包",
		objSchema(map[string]any{
			"action":      enumProp("操作", "apk", "splits", "bundle"),
			"package":     strProp("包名"),
			"destination": strProp("目标路径"),
			"user":        intProp("用户 ID"),
		}, "action", "package"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action      string `json:"action"`
				Package     string `json:"package"`
				Destination string `json:"destination"`
				User        int    `json:"user"`
			}
			_ = json.Unmarshal(args, &in)

			out, _, _, err := runSh(ctx, deps, "novaai_app_export",
				fmt.Sprintf("pm path %s", shQuote(in.Package)), 15*time.Second)
			if err != nil {
				return errFail("PATH_FAILED", err.Error()), nil
			}

			var paths []string
			for _, l := range splitLines(out) {
				p := strings.TrimPrefix(l, "package:")
				paths = append(paths, p)
			}
			if len(paths) == 0 {
				return errFail("NOT_FOUND", "包未找到"), nil
			}

			var dst string
			if in.Destination != "" {
				dst = resolvePath(deps, in.Destination)
			} else {
				dst = fmt.Sprintf("%s/%s-%d.apk",
					deps.Config.Paths.WorkspaceRoot, in.Package, time.Now().Unix())
			}

			if err := copyFile(paths[0], dst, true); err != nil {
				return errFail("COPY_FAILED", err.Error()), nil
			}

			return ok(map[string]any{
				"paths":       paths,
				"destination": dst,
				"count":       len(paths),
			}), nil
		})

	// ---- app_policy ----
	reg("novaai_app_policy", "应用策略", "管理待机桶、后台与电池优化策略",
		objSchema(map[string]any{
			"action":  enumProp("操作", "get", "standby_bucket", "background", "battery_optimization"),
			"package": strProp("包名"),
			"allowed": boolProp("是否允许"),
			"value":   map[string]any{"type": []string{"string", "integer", "boolean"}},
		}, "action", "package"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Package string `json:"package"`
				Allowed bool   `json:"allowed"`
				Value   any    `json:"value"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"dumpsys", "deviceidle", "whitelist"}
			case "standby_bucket":
				cmd = []string{"am", "get-standby-bucket", in.Package}
			case "background":
				if in.Allowed {
					cmd = []string{"cmd", "deviceidle", "whitelist", "+" + in.Package}
				} else {
					cmd = []string{"cmd", "deviceidle", "whitelist", "-" + in.Package}
				}
			case "battery_optimization":
				if in.Allowed {
					cmd = []string{"cmd", "deviceidle", "whitelist", "+" + in.Package}
				} else {
					cmd = []string{"cmd", "deviceidle", "whitelist", "-" + in.Package}
				}
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_app_policy",
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

	// ---- default_app ----
	reg("novaai_default_app", "默认应用", "读取、设置或清除默认应用",
		objSchema(map[string]any{
			"action":    enumProp("操作", "get", "set", "clear"),
			"role":      strProp("Role 名称"),
			"package":   strProp("包名"),
			"component": strProp("组件名"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Role      string `json:"role"`
				Package   string `json:"package"`
				Component string `json:"component"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"cmd", "role", "get-role-holders", in.Role}
			case "set":
				target := in.Package
				if in.Component != "" {
					target = in.Component
				}
				cmd = []string{"cmd", "role", "add-role-holder", in.Role, target}
			case "clear":
				cmd = []string{"cmd", "role", "remove-role-holder", in.Role, in.Package}
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_default_app",
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

	// ---- notification ----
	reg("novaai_notification", "通知权限", "读取或调整通知与监听器权限",
		objSchema(map[string]any{
			"action":    enumProp("操作", "get", "allow", "deny", "listener"),
			"package":   strProp("包名"),
			"component": strProp("组件名"),
			"allowed":   boolProp("是否允许"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Package   string `json:"package"`
				Component string `json:"component"`
				Allowed   bool   `json:"allowed"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"dumpsys", "notification", "--noredact"}
			case "allow":
				cmd = []string{"cmd", "notification", "allow_listener", in.Component}
			case "deny":
				cmd = []string{"cmd", "notification", "disallow_listener", in.Component}
			case "listener":
				cmd = []string{"settings", "get", "secure", "enabled_notification_listeners"}
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_notification",
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
}
