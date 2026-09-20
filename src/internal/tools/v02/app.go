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
	reg("novaai_app_list", "应用列表", "按包名查询普通、系统或指定用户应用",
		objSchema(map[string]any{
			"query":  strProp("搜索关键词"),
			"system": boolProp("仅系统应用"),
			"limit":  intProp("返回上限"),
			"offset": intProp("分页偏移"),
			"user":   intProp("Android 用户 ID"),
		}),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
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
	// 只支持"从本地 APK 路径安装"这一条通路：pm install 单文件。
	// 早期版本声明了 action(apk/split/apks/xapk/session)、package 与
	// background，但 handler 一个都不读 —— 那些是承诺了不存在的安装模式。
	// 多 APK / Split 安装需要 pm install-create|install-write|install-commit
	// 三段式，属于未实现能力，不该出现在 schema 里。
	reg("novaai_app_install", "安装应用", "通过 Package Manager 安装本地 APK",
		objSchema(map[string]any{
			"path":                    strProp("APK 路径"),
			"downgrade":               boolProp("允许降级"),
			"replace":                 boolProp("替换安装"),
			"grantRuntimePermissions": boolProp("自动授权"),
			"user":                    intProp("用户 ID"),
		}, "path"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Path      string `json:"path"`
				Downgrade bool   `json:"downgrade"`
				Replace   bool   `json:"replace"`
				Grant     bool   `json:"grantRuntimePermissions"`
				User      int    `json:"user"`
			}
			_ = json.Unmarshal(args, &in)

			if in.Path == "" {
				return errFail("MISSING_PATH", "path 必填"), nil
			}
			apkPath := resolvePath(deps, in.Path)

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
			return execResult(out, errOut, code), nil
		})
}
