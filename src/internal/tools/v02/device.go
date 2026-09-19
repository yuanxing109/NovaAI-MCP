package v02

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

func registerDeviceTools(reg RegisterFn, deps *Deps) {
	// ---- device_info ----
	reg("novaai_device_info", "设备信息", "读取设备、电池、存储与传感器信息",
		objSchema(map[string]any{
			"action": enumProp("操作", "get", "battery", "storage", "sensors"),
			"path":   strProp("路径"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Path   string `json:"path"`
			}
			_ = json.Unmarshal(args, &in)

			switch in.Action {
			case "get":
				props := []string{
					"ro.product.model", "ro.product.brand", "ro.product.manufacturer",
					"ro.build.version.release", "ro.build.version.sdk",
					"ro.product.cpu.abi", "ro.product.cpu.abilist",
					"ro.build.flavor", "ro.build.fingerprint",
				}
				out := map[string]string{}
				for _, p := range props {
					v, _, _, _ := runCmd(ctx, deps, "novaai_device_info", "getprop",
						[]string{p}, 5*time.Second)
					out[p] = strings.TrimSpace(v)
				}
				return ok(out), nil
			case "battery":
				out, _, _, _ := runCmd(ctx, deps, "novaai_device_info", "dumpsys",
					[]string{"battery"}, 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "storage":
				out, _, _, _ := runSh(ctx, deps, "novaai_device_info", "df -h", 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "sensors":
				out, _, _, _ := runCmd(ctx, deps, "novaai_device_info", "dumpsys",
					[]string{"sensorservice"}, 15*time.Second)
				return ok(map[string]any{"raw": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- schedule ----
	//
	// 注意：这不是调度器。它只把脚本存到 stateDir/schedules/ 并按需手动执行，
	// 服务本身没有任何定时触发机制。action 与描述必须如实反映这一点 ——
	// 早期版本声明了 cron/every/at 并把它们写成脚本注释，但没有任何代码读它们，
	// 于是"创建了定时任务"变成了一个不会被触发的空承诺。
	reg("novaai_schedule", "脚本任务", "保存、列出、删除或手动运行命名脚本（本服务不自动触发）",
		objSchema(map[string]any{
			"action":     enumProp("操作", "list", "create", "remove", "run"),
			"scheduleId": strProp("任务 ID"),
			"command":    strProp("脚本内容"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action     string `json:"action"`
				ScheduleID string `json:"scheduleId"`
				Command    string `json:"command"`
			}
			_ = json.Unmarshal(args, &in)

			scheduleDir := deps.StateDir + "/schedules"

			switch in.Action {
			case "list":
				entries, _ := os.ReadDir(scheduleDir)
				var items []map[string]any
				for _, e := range entries {
					items = append(items, map[string]any{"id": e.Name()})
				}
				return ok(map[string]any{"schedules": items}), nil
			case "create":
				if in.Command == "" {
					return errFail("MISSING_COMMAND", "command 必填"), nil
				}
				id := in.ScheduleID
				if id == "" {
					id = time.Now().Format("20060102-150405")
				} else if !idRe.MatchString(id) {
					return errFail("INVALID_PARAM", "非法 scheduleId: "+id), nil
				}
				_ = os.MkdirAll(scheduleDir, 0700)
				path := scheduleDir + "/" + id + ".sh"
				_ = os.WriteFile(path, []byte(in.Command), 0700)
				return ok(map[string]any{"id": id, "path": path}), nil
			case "remove":
				if !idRe.MatchString(in.ScheduleID) {
					return errFail("INVALID_PARAM", "非法 scheduleId: "+in.ScheduleID), nil
				}
				path := scheduleDir + "/" + in.ScheduleID + ".sh"
				_ = os.Remove(path)
				return okMsg("已删除"), nil
			case "run":
				if !idRe.MatchString(in.ScheduleID) {
					return errFail("INVALID_PARAM", "非法 scheduleId: "+in.ScheduleID), nil
				}
				path := scheduleDir + "/" + in.ScheduleID + ".sh"
				out, errOut, code, _ := runSh(ctx, deps, "novaai_schedule",
					"sh "+shQuote(path), 60*time.Second)
				return ok(map[string]any{
					"output": out, "stderr": errOut, "exitCode": code,
				}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
