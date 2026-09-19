package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// envNameRe 校验环境变量名。变量名无法用单引号包裹（'KEY'=v 不是合法赋值语句），
// 所以只能靠白名单阻断 "KEY; cmd" 这类注入。包级变量，避免每次循环重编译。
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func registerExecTools(reg RegisterFn, deps *Deps) {
	// ---- shell ----
	reg("novaai_shell", "执行命令", "以 root、shell 或指定 UID 执行命令",
		objSchema(map[string]any{
			"command":   strProp("要执行的命令"),
			"cmd":       strProp("command 兼容别名"),
			"identity":  enumProp("身份", "root", "shell", "current", "uid"),
			"uid":       intProp("目标 UID"),
			"cwd":       strProp("工作目录"),
			"env":       map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"stdin":     strProp("标准输入"),
			"timeoutMs": intProp("超时毫秒"),
		}),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Command   string            `json:"command"`
				Cmd       string            `json:"cmd"`
				Identity  string            `json:"identity"`
				UID       int               `json:"uid"`
				Cwd       string            `json:"cwd"`
				Env       map[string]string `json:"env"`
				Stdin     string            `json:"stdin"`
				TimeoutMs int               `json:"timeoutMs"`
			}
			_ = json.Unmarshal(args, &in)

			cmdStr := in.Command
			if cmdStr == "" {
				cmdStr = in.Cmd
			}
			if cmdStr == "" {
				return errFail("MISSING_COMMAND", "command 必填"), nil
			}

			if in.TimeoutMs <= 0 {
				in.TimeoutMs = int(shellTimeout(deps) / time.Millisecond)
			}

			// 身份处理
			var finalCmd string
			switch in.Identity {
			case "root":
				su := suPrefix()
				if su != "" {
					finalCmd = fmt.Sprintf("%s -c %s", su, shQuote(cmdStr))
				} else {
					finalCmd = cmdStr
				}
			case "shell":
				finalCmd = fmt.Sprintf("su 2000 -c %s", shQuote(cmdStr))
			case "uid":
				if in.UID <= 0 {
					return errFail("INVALID_UID", "uid 必填且 > 0"), nil
				}
				finalCmd = fmt.Sprintf("su %d -c %s", in.UID, shQuote(cmdStr))
			default:
				finalCmd = cmdStr
			}

			// cwd 前缀
			if in.Cwd != "" {
				finalCmd = fmt.Sprintf("cd %s && %s", shQuote(in.Cwd), finalCmd)
			}

			// env 前缀
			if len(in.Env) > 0 {
				var envParts string
				for k, v := range in.Env {
					if !envNameRe.MatchString(k) {
						return errFail("INVALID_PARAM", "非法环境变量名: "+k), nil
					}
					envParts += fmt.Sprintf("%s=%s ", k, shQuote(v))
				}
				finalCmd = envParts + finalCmd
			}

			// 硬性规定：shell 不走 Adapter.Preprocess
			out, errOut, code, err := runShRaw(ctx, deps, finalCmd, in.Stdin,
				time.Duration(in.TimeoutMs)*time.Millisecond)
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

	// ---- script ----
	reg("novaai_script", "执行脚本", "验证或执行多行脚本",
		objSchema(map[string]any{
			"action":    enumProp("操作", "validate", "run"),
			"script":    strProp("脚本文本"),
			"content":   strProp("script 兼容别名"),
			"identity":  enumProp("身份", "root", "shell", "current", "uid"),
			"uid":       intProp("目标 UID"),
			"cwd":       strProp("工作目录"),
			"env":       map[string]any{"type": "object"},
			"stdin":     strProp("标准输入"),
			"timeoutMs": intProp("超时毫秒"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string            `json:"action"`
				Script    string            `json:"script"`
				Content   string            `json:"content"`
				Identity  string            `json:"identity"`
				UID       int               `json:"uid"`
				Cwd       string            `json:"cwd"`
				Env       map[string]string `json:"env"`
				Stdin     string            `json:"stdin"`
				TimeoutMs int               `json:"timeoutMs"`
			}
			_ = json.Unmarshal(args, &in)

			script := in.Script
			if script == "" {
				script = in.Content
			}
			if script == "" {
				return errFail("MISSING_SCRIPT", "script 必填"), nil
			}

			if in.Action == "validate" {
				// 用 sh -n 校验语法
				_, errOut, code, _ := runShRaw(ctx, deps, "sh -n <<'__EOS__'\n"+script+"\n__EOS__\n",
					"", 10*time.Second)
				if code != 0 {
					return errFail("SYNTAX_ERROR", errOut), nil
				}
				return ok(map[string]any{"valid": true}), nil
			}
			if in.Action != "run" {
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			if in.TimeoutMs <= 0 {
				in.TimeoutMs = int(shellTimeout(deps) / time.Millisecond)
			}

			var finalScript string
			switch in.Identity {
			case "root":
				su := suPrefix()
				if su != "" {
					finalScript = fmt.Sprintf("%s -c %s", su, shQuote(script))
				} else {
					finalScript = script
				}
			case "shell":
				finalScript = fmt.Sprintf("su 2000 -c %s", shQuote(script))
			case "uid":
				finalScript = fmt.Sprintf("su %d -c %s", in.UID, shQuote(script))
			default:
				finalScript = script
			}

			out, errOut, code, err := runShRaw(ctx, deps, finalScript, in.Stdin,
				time.Duration(in.TimeoutMs)*time.Millisecond)
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
