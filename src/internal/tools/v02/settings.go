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
	// ---- display ----
	reg("novaai_display", "显示设置", "读取或调整亮度、尺寸、密度与旋转",
		objSchema(map[string]any{
			"action": enumProp("操作", "get", "brightness", "size", "density", "rotation"),
			"value":  strProp("值"),
			"auto":   boolProp("自动模式"),
			"reset":  boolProp("恢复默认"),
			"scale":  strProp("缩放比例"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Value  string `json:"value"`
				Auto   bool   `json:"auto"`
				Reset  bool   `json:"reset"`
				Scale  string `json:"scale"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"wm", "size"}
			case "brightness":
				if in.Auto {
					cmd = []string{"settings", "put", "system", "screen_brightness_mode", "1"}
				} else {
					cmd = []string{"settings", "put", "system", "screen_brightness", in.Value}
				}
			case "size":
				cmd = []string{"wm", "size", in.Value}
			case "density":
				cmd = []string{"wm", "density", in.Value}
			case "rotation":
				cmd = []string{"settings", "put", "system", "user_rotation", in.Value}
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_display",
				cmd[0], cmd[1:], 15*time.Second)
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

	// ---- audio ----
	reg("novaai_audio", "音频设置", "读取或调整音量、静音与音频路由",
		objSchema(map[string]any{
			"action": enumProp("操作", "get", "volume", "mute", "route"),
			"stream": intProp("音频流编号"),
			"level":  intProp("音量级别"),
			"muted":  boolProp("是否静音"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Stream int    `json:"stream"`
				Level  int    `json:"level"`
				Muted  bool   `json:"muted"`
			}
			_ = json.Unmarshal(args, &in)

			if in.Stream == 0 {
				in.Stream = 3
			}

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"media", "volume", "--show", "--stream", fmt.Sprint(in.Stream)}
			case "volume":
				cmd = []string{"media", "volume", "--set", fmt.Sprint(in.Level), "--stream", fmt.Sprint(in.Stream)}
			case "mute":
				if in.Muted {
					cmd = []string{"media", "volume", "--set", "0", "--stream", fmt.Sprint(in.Stream)}
				} else {
					cmd = []string{"media", "volume", "--set", "10", "--stream", fmt.Sprint(in.Stream)}
				}
			case "route":
				return errFail("NOT_IMPLEMENTED", "音频路由控制需 ROM 支持"), nil
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_audio",
				cmd[0], cmd[1:], 15*time.Second)
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

	// ---- connectivity ----
	reg("novaai_connectivity", "连接性", "读取或调整 Wi-Fi、移动数据、飞行模式、蓝牙与 NFC",
		objSchema(map[string]any{
			"action":  enumProp("操作", "get", "wifi", "mobile_data", "airplane_mode", "bluetooth", "nfc"),
			"enabled": boolProp("是否启用"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Enabled bool   `json:"enabled"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"dumpsys", "connectivity"}
			case "wifi":
				if in.Enabled {
					cmd = []string{"svc", "wifi", "enable"}
				} else {
					cmd = []string{"svc", "wifi", "disable"}
				}
			case "mobile_data":
				if in.Enabled {
					cmd = []string{"svc", "data", "enable"}
				} else {
					cmd = []string{"svc", "data", "disable"}
				}
			case "airplane_mode":
				if in.Enabled {
					cmd = []string{"cmd", "connectivity", "airplane-mode", "enable"}
				} else {
					cmd = []string{"cmd", "connectivity", "airplane-mode", "disable"}
				}
			case "bluetooth":
				if in.Enabled {
					cmd = []string{"svc", "bluetooth", "enable"}
				} else {
					cmd = []string{"svc", "bluetooth", "disable"}
				}
			case "nfc":
				if in.Enabled {
					cmd = []string{"svc", "nfc", "enable"}
				} else {
					cmd = []string{"svc", "nfc", "disable"}
				}
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_connectivity",
				cmd[0], cmd[1:], 15*time.Second)
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

	// ---- locale_time ----
	reg("novaai_locale_time", "语言时间", "读取或调整语言区域、时区与时间格式",
		objSchema(map[string]any{
			"action":   enumProp("操作", "get", "locale", "timezone", "time_format"),
			"locale":   strProp("BCP-47 语言标签"),
			"timezone": strProp("IANA 时区"),
			"value":    strProp("设置值"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action   string `json:"action"`
				Locale   string `json:"locale"`
				Timezone string `json:"timezone"`
				Value    string `json:"value"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"getprop", "persist.sys.locale"}
			case "locale":
				cmd = []string{"setprop", "persist.sys.locale", in.Locale}
			case "timezone":
				cmd = []string{"setprop", "persist.sys.timezone", in.Timezone}
			case "time_format":
				cmd = []string{"settings", "put", "system", "time_12_24", in.Value}
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_locale_time",
				cmd[0], cmd[1:], 10*time.Second)
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

	// ---- input_method ----
	reg("novaai_input_method", "输入法", "列出、读取、选择、启用或禁用输入法",
		objSchema(map[string]any{
			"action":    enumProp("操作", "list", "get", "set", "enable", "disable"),
			"component": strProp("组件名"),
			"id":        strProp("输入法 ID"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Component string `json:"component"`
				ID        string `json:"id"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "list":
				cmd = []string{"ime", "list", "-a", "-s"}
			case "get":
				cmd = []string{"settings", "get", "secure", "default_input_method"}
			case "set":
				cmd = []string{"ime", "set", in.ID}
			case "enable":
				cmd = []string{"ime", "enable", in.Component}
			case "disable":
				cmd = []string{"ime", "disable", in.Component}
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_input_method",
				cmd[0], cmd[1:], 15*time.Second)
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

	// ---- developer ----
	reg("novaai_developer", "开发者选项", "读取或调整 ADB、常亮、动画及模拟位置设置",
		objSchema(map[string]any{
			"action":  enumProp("操作", "get", "adb", "stay_awake", "animation", "mock_location"),
			"enabled": boolProp("是否启用"),
			"scale":   strProp("动画缩放比例"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Enabled bool   `json:"enabled"`
				Scale   string `json:"scale"`
			}
			_ = json.Unmarshal(args, &in)

			var cmd []string
			switch in.Action {
			case "get":
				cmd = []string{"settings", "list", "global"}
			case "adb":
				if in.Enabled {
					cmd = []string{"settings", "put", "global", "adb_enabled", "1"}
				} else {
					cmd = []string{"settings", "put", "global", "adb_enabled", "0"}
				}
			case "stay_awake":
				if in.Enabled {
					cmd = []string{"settings", "put", "global", "stay_on_while_plugged_in", "3"}
				} else {
					cmd = []string{"settings", "put", "global", "stay_on_while_plugged_in", "0"}
				}
			case "animation":
				if in.Scale == "" {
					in.Scale = "1.0"
				}
				out1, _, _, _ := runSh(ctx, deps, "novaai_developer",
					fmt.Sprintf("settings put global window_animation_scale %s && settings put global transition_animation_scale %s && settings put global animator_duration_scale %s",
						shQuote(in.Scale), shQuote(in.Scale), shQuote(in.Scale)), 10*time.Second)
				return ok(map[string]any{"output": out1}), nil
			case "mock_location":
				if in.Enabled {
					cmd = []string{"appops", "set", "android", "android:mock_location", "allow"}
				} else {
					cmd = []string{"appops", "set", "android", "android:mock_location", "deny"}
				}
			}

			out, errOut, code, err := runCmd(ctx, deps, "novaai_developer",
				cmd[0], cmd[1:], 15*time.Second)
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

	// ---- power ----
	reg("novaai_power", "电源", "重启到系统/Recovery/Bootloader/Fastbootd 或关机",
		objSchema(map[string]any{
			"action":           enumProp("操作", "reboot", "recovery", "bootloader", "fastbootd", "shutdown", "soft_reboot"),
			"confirmDangerous": boolProp("确认破坏性"),
			"delayMs":          intProp("延迟毫秒"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Confirm bool   `json:"confirmDangerous"`
				DelayMs int    `json:"delayMs"`
			}
			_ = json.Unmarshal(args, &in)

			if !in.Confirm {
				return errFail("NOT_CONFIRMED", "需要 confirmDangerous: true"), nil
			}

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
			if len(cmd) == 0 {
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
			"action":     enumProp("操作", "screenshot", "record", "foreground", "wake", "sleep"),
			"path":       strProp("输出路径"),
			"seconds":    intProp("录屏秒数"),
			"bitRate":    intProp("码率"),
			"background": boolProp("后台"),
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
						deps.Config.Paths.WorkspaceRoot, time.Now().Unix())
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
						deps.Config.Paths.WorkspaceRoot, time.Now().Unix())
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

	// ---- accessibility ----
	reg("novaai_accessibility", "无障碍服务", "列出、读取、启用或禁用无障碍服务",
		objSchema(map[string]any{
			"action":    enumProp("操作", "list", "get", "enable", "disable"),
			"component": strProp("组件名"),
			"id":        strProp("服务 ID"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Component string `json:"component"`
				ID        string `json:"id"`
			}
			_ = json.Unmarshal(args, &in)

			switch in.Action {
			case "list":
				out, _, _, _ := runSh(ctx, deps, "novaai_accessibility",
					"settings get secure enabled_accessibility_services", 10*time.Second)
				return ok(map[string]any{"enabled": strings.TrimSpace(out)}), nil
			case "get":
				out, _, _, _ := runSh(ctx, deps, "novaai_accessibility",
					"dumpsys accessibility", 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "enable":
				target := in.Component
				if target == "" {
					target = in.ID
				}
				// 第二个 %s 必须"先闭合双引号再拼单引号"：
				//   "$current:"'value'  —— shell 相邻片段拼接，得到 $current: 加字面量 value。
				// 若直接写 "$current:%s" + shQuote，单引号会落进双引号内部变成字面字符，
				// 设置值会被污染成 $current:'value'。
				cmd := fmt.Sprintf(
					"current=$(settings get secure enabled_accessibility_services); "+
						"if [ \"$current\" = \"null\" ] || [ -z \"$current\" ]; then "+
						"settings put secure enabled_accessibility_services %s; "+
						"else settings put secure enabled_accessibility_services \"$current:\"%s; fi; "+
						"settings put secure accessibility_enabled 1",
					shQuote(target), shQuote(target))
				out, errOut, code, _ := runSh(ctx, deps, "novaai_accessibility", cmd, 15*time.Second)
				if code != 0 {
					return errFail("ENABLE_FAILED", errOut), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "disable":
				target := in.Component
				if target == "" {
					target = in.ID
				}
				// sed 的定界符是 |，先转义 \ 和 | 防止脚本被值破坏，再把整个脚本 shQuote。
				// 直接 shQuote(target) 会得到 's|value||g'，value 里的 | 会提前结束 s 命令。
				sedScript := shQuote("s|" + strings.NewReplacer(`\`, `\\`, `|`, `\|`).Replace(target) + "||g")
				cmd := fmt.Sprintf(
					"current=$(settings get secure enabled_accessibility_services); "+
						"new=$(echo \"$current\" | sed %s | sed 's|::|:|g' | sed 's|^:||;s|:$||'); "+
						"settings put secure enabled_accessibility_services \"$new\"",
					sedScript)
				out, errOut, code, _ := runSh(ctx, deps, "novaai_accessibility", cmd, 15*time.Second)
				if code != 0 {
					return errFail("DISABLE_FAILED", errOut), nil
				}
				return ok(map[string]any{"output": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}
