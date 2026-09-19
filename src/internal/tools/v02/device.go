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
	reg("novaai_schedule", "定时任务", "管理一次、周期、Cron 及事件触发任务",
		objSchema(map[string]any{
			"action":       enumProp("操作", "list", "create", "update", "remove", "enable", "disable", "run"),
			"scheduleId":   strProp("任务 ID"),
			"name":         strProp("任务名"),
			"command":      strProp("执行命令"),
			"cron":         strProp("五字段 Cron 表达式"),
			"everySeconds": intProp("间隔秒数"),
			"at":           strProp("RFC3339 时间"),
			"targetTool":   strProp("定时任务目标工具名"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action       string `json:"action"`
				ScheduleID   string `json:"scheduleId"`
				Name         string `json:"name"`
				Command      string `json:"command"`
				Cron         string `json:"cron"`
				EverySeconds int    `json:"everySeconds"`
				At           string `json:"at"`
				TargetTool   string `json:"targetTool"`
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
				content := in.Command
				if in.Cron != "" {
					content = "# cron: " + in.Cron + "\n" + content
				} else if in.EverySeconds > 0 {
					content = "# every: " + itoa(in.EverySeconds) + "\n" + content
				} else if in.At != "" {
					content = "# at: " + in.At + "\n" + content
				}
				path := scheduleDir + "/" + id + ".sh"
				_ = os.WriteFile(path, []byte(content), 0700)
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

	// ---- task ----
	reg("novaai_task", "任务管理", "读取、列出、更新、取消任务或读取日志",
		objSchema(map[string]any{
			"action":   enumProp("操作", "get", "list", "update", "cancel", "logs"),
			"taskId":   strProp("任务 ID"),
			"message":  strProp("消息"),
			"progress": map[string]any{"type": "number"},
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action   string  `json:"action"`
				TaskID   string  `json:"taskId"`
				Message  string  `json:"message"`
				Progress float64 `json:"progress"`
			}
			_ = json.Unmarshal(args, &in)

			switch in.Action {
			case "list":
				return ok(map[string]any{"tasks": []any{}}), nil
			case "get":
				return errFail("NOT_FOUND", "任务系统未启动"), nil
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
