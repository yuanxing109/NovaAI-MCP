package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// keyeventTokenRe 是 input keyevent 允许的单个 token：KEYCODE_*、纯数字键码，
// 以及 input 自带的选项开关（--longpress / --doubletap 等）。
// 不含空格、引号、分号、$、反引号、斜杠，因此拼进命令是安全的。
var keyeventTokenRe = regexp.MustCompile(`^(--)?[A-Za-z0-9_]+$`)

func registerSettingTools(reg RegisterFn, deps *Deps) {
	// ---- power ----
	reg("novaai_power", "电源", "重启到系统/Recovery/Bootloader/Fastbootd 或关机",
		objSchema(map[string]any{
			"action":  enumProp("操作", "reboot", "recovery", "bootloader", "fastbootd", "shutdown", "soft_reboot"),
			"delayMs": intProp("延迟毫秒"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				DelayMs int    `json:"delayMs"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "reboot":
				cmd = []string{"reboot"}
			case "recovery":
				cmd = []string{"reboot", "recovery"}
			case "bootloader":
				cmd = []string{"reboot", "bootloader"}
			case "fastbootd":
				cmd = []string{"reboot", "fastboot"}
			case "shutdown":
				cmd = []string{"reboot", "-p"}
			case "soft_reboot":
				cmd = []string{"setprop", "ctl.restart", "zygote"}
			default:
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			if in.DelayMs > 0 {
				time.Sleep(time.Duration(in.DelayMs) * time.Millisecond)
			}

			go func() {
				runCmd(context.Background(), deps, "novaai_power",
					cmd[0], cmd[1:], 30*time.Second)
			}()

			return okMsg("电源操作已下发"), nil
		})

	// ---- screen ----
	reg("novaai_screen", "屏幕操作", "截图、录屏、前台 Activity、唤醒或休眠",
		objSchema(map[string]any{
			"action":  enumProp("操作", "screenshot", "record", "foreground", "wake", "sleep"),
			"path":    strProp("输出路径"),
			"seconds": intProp("录屏秒数"),
			"bitRate": intProp("码率"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Path    string `json:"path"`
				Seconds int    `json:"seconds"`
				BitRate int    `json:"bitRate"`
			}
			_ = json.Unmarshal(args, &in)

			switch in.Action {
			case "screenshot":
				p := in.Path
				if p == "" {
					p = fmt.Sprintf("%s/screenshot-%d.png",
						deps.Config.WorkspaceRoot(), time.Now().Unix())
				} else {
					p = resolvePath(deps, p)
				}
				_, errOut, code, _ := runSh(ctx, deps, "novaai_screen",
					fmt.Sprintf("screencap -p %s", shQuote(p)), 15*time.Second)
				if code != 0 {
					return errFail("SCREENCAP_FAILED", errOut), nil
				}
				return ok(map[string]any{"path": p}), nil
			case "record":
				p := in.Path
				if p == "" {
					p = fmt.Sprintf("%s/screenrecord-%d.mp4",
						deps.Config.WorkspaceRoot(), time.Now().Unix())
				} else {
					p = resolvePath(deps, p)
				}
				secs := in.Seconds
				if secs <= 0 {
					secs = 10
				}
				rate := in.BitRate
				if rate <= 0 {
					rate = 4000000
				}
				go func() {
					runSh(context.Background(), deps, "novaai_screen",
						fmt.Sprintf("screenrecord --bit-rate %d --time-limit %d %s", rate, secs, shQuote(p)),
						time.Duration(secs+10)*time.Second)
				}()
				return ok(map[string]any{"path": p, "seconds": secs}), nil
			case "foreground":
				out, _, _, _ := runSh(ctx, deps, "novaai_screen",
					"dumpsys window | grep mCurrentFocus", 10*time.Second)
				return ok(map[string]any{"foreground": strings.TrimSpace(out)}), nil
			case "wake":
				runSh(ctx, deps, "novaai_screen",
					"input keyevent KEYCODE_WAKEUP", 5*time.Second)
				return okMsg("已唤醒"), nil
			case "sleep":
				runSh(ctx, deps, "novaai_screen",
					"input keyevent KEYCODE_SLEEP", 5*time.Second)
				return okMsg("已休眠"), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- input ----
	reg("novaai_input", "输入操作", "点击、滑动、输入文本、按键与状态栏操作",
		objSchema(map[string]any{
			"action":     enumProp("操作", "tap", "swipe", "text", "keyevent", "statusbar"),
			"x":          intProp("X 坐标"),
			"y":          intProp("Y 坐标"),
			"x1":         intProp("起点 X"),
			"y1":         intProp("起点 Y"),
			"x2":         intProp("终点 X"),
			"y2":         intProp("终点 Y"),
			"text":       strProp("输入文本"),
			"key":        strProp("按键名"),
			"durationMs": intProp("持续时间"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action     string `json:"action"`
				X          int    `json:"x"`
				Y          int    `json:"y"`
				X1         int    `json:"x1"`
				Y1         int    `json:"y1"`
				X2         int    `json:"x2"`
				Y2         int    `json:"y2"`
				Text       string `json:"text"`
				Key        string `json:"key"`
				DurationMs int    `json:"durationMs"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd string
			switch in.Action {
			case "tap":
				cmd = fmt.Sprintf("input tap %d %d", in.X, in.Y)
			case "swipe":
				if in.DurationMs <= 0 {
					in.DurationMs = 300
				}
				cmd = fmt.Sprintf("input swipe %d %d %d %d %d",
					in.X1, in.Y1, in.X2, in.Y2, in.DurationMs)
			case "text":
				t := strings.ReplaceAll(in.Text, " ", "%s")
				cmd = fmt.Sprintf("input text %s", shQuote(t))
			case "keyevent":
				// 按键名本身不含空格；含空格说明调用方在传多个 token
				// （例如 "--longpress KEYCODE_HOME"）。逐个 token 白名单校验后
				// 用空格重拼：只有通过白名单的 token 能进入命令，所以安全，
				// 同时不必牺牲 input 的多 token 用法。
				key := strings.TrimSpace(in.Key)
				if key == "" {
					return errFail("MISSING_PARAM", "keyevent 需要 key"), nil
				}
				tokens := strings.Fields(key)
				for _, tk := range tokens {
					if !keyeventTokenRe.MatchString(tk) {
						return errFail("INVALID_PARAM", "非法按键名: "+tk), nil
					}
				}
				cmd = "input keyevent " + strings.Join(tokens, " ")
			case "statusbar":
				cmd = "service call statusbar 1"
			default:
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			out, errOut, code, _ := runSh(ctx, deps, "novaai_input", cmd, 15*time.Second)
			if code != 0 {
				return errFail("INPUT_FAILED", errOut), nil
			}
			return ok(map[string]any{"output": out}), nil
		})
}
