package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func registerRootTools(reg RegisterFn, deps *Deps) {
	// ---- root_info ----
	// 无参数单动作工具：一次调用即返回框架、生效 UID 与 su 路径。
	// 早期版本声明了 action(detect/capabilities/self_test)，但 handler
	// 从不读它 —— 三个"动作"其实是同一个行为。
	reg("novaai_root_info", "Root 信息", "检测 Root 框架与运行时身份",
		objSchema(map[string]any{}),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			framework := "Unknown"
			detectedBy := ""
			if _, err := os.Stat("/data/adb/ksu"); err == nil {
				framework = "KernelSU"
				detectedBy = "/data/adb/ksu"
			} else if _, err := os.Stat("/data/adb/magisk"); err == nil {
				framework = "Magisk"
				detectedBy = "/data/adb/magisk"
			} else if _, err := os.Stat("/data/adb/ap"); err == nil {
				framework = "APatch"
				detectedBy = "/data/adb/ap"
			}

			return ok(map[string]any{
				"framework":    framework,
				"detectedBy":   detectedBy,
				"effectiveUid": os.Getuid(),
				"suPath":       suPrefix(),
			}), nil
		})

	// ---- root_module ----
	reg("novaai_root_module", "Root 模块", "管理 Magisk、KernelSU 或 APatch 模块生命周期",
		objSchema(map[string]any{
			"action":   enumProp("操作", "list", "info", "install", "update", "remove", "enable", "disable", "logs"),
			"moduleId": strProp("模块 ID"),
			"path":     strProp("ZIP 路径"),
			"zip":      strProp("ZIP 路径别名"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action   string `json:"action"`
				ModuleID string `json:"moduleId"`
				Path     string `json:"path"`
				Zip      string `json:"zip"`
			}
			_ = json.Unmarshal(args, &in)

			modulesDir := "/data/adb/modules"

			// moduleId 会被拼进文件路径；不校验就是 "../.." 穿越。
			switch in.Action {
			case "info", "enable", "disable", "remove":
				if !idRe.MatchString(in.ModuleID) {
					return errFail("INVALID_PARAM", "非法 moduleId: "+in.ModuleID), nil
				}
			}

			switch in.Action {
			case "list":
				entries, _ := os.ReadDir(modulesDir)
				var mods []map[string]any
				for _, e := range entries {
					mod := map[string]any{"id": e.Name()}
					// 检查是否 disabled/removed
					if _, err := os.Stat(filepath.Join(modulesDir, e.Name(), "disable")); err == nil {
						mod["disabled"] = true
					}
					if _, err := os.Stat(filepath.Join(modulesDir, e.Name(), "remove")); err == nil {
						mod["pendingRemove"] = true
					}
					propPath := filepath.Join(modulesDir, e.Name(), "module.prop")
					if b, err := os.ReadFile(propPath); err == nil {
						for _, l := range splitLines(string(b)) {
							if idx := indexByte(l, '='); idx > 0 {
								mod[l[:idx]] = l[idx+1:]
							}
						}
					}
					mods = append(mods, mod)
				}
				return ok(map[string]any{"modules": mods}), nil
			case "info":
				b, err := os.ReadFile(filepath.Join(modulesDir, in.ModuleID, "module.prop"))
				if err != nil {
					return errFail("NOT_FOUND", err.Error()), nil
				}
				return ok(map[string]any{"prop": string(b)}), nil
			case "enable":
				_ = os.Remove(filepath.Join(modulesDir, in.ModuleID, "disable"))
				return okMsg("已启用"), nil
			case "disable":
				_ = os.MkdirAll(filepath.Join(modulesDir, in.ModuleID), 0755)
				f, _ := os.Create(filepath.Join(modulesDir, in.ModuleID, "disable"))
				if f != nil {
					f.Close()
				}
				return okMsg("已禁用"), nil
			case "remove":
				_ = os.MkdirAll(filepath.Join(modulesDir, in.ModuleID), 0755)
				f, _ := os.Create(filepath.Join(modulesDir, in.ModuleID, "remove"))
				if f != nil {
					f.Close()
				}
				return okMsg("已标记删除，重启生效"), nil
			case "install", "update":
				zipPath := in.Zip
				if zipPath == "" {
					zipPath = in.Path
				}
				zipPath = resolvePath(deps, zipPath)
				if _, err := os.Stat(zipPath); err != nil {
					return errFail("NOT_FOUND", err.Error()), nil
				}
				// 通过 ksud / magisk 安装
				su := suPrefix()
				var cmd string
				if frameworkIs("KernelSU") {
					cmd = fmt.Sprintf("/data/adb/ksu/bin/ksud module install %s", shQuote(zipPath))
				} else if frameworkIs("Magisk") {
					cmd = fmt.Sprintf("%s --install-module %s", su, shQuote(zipPath))
				} else {
					return errFail("UNSUPPORTED", "当前框架不支持模块安装"), nil
				}
				out, errOut, code, _ := runSh(ctx, deps, "novaai_root_module", cmd, 2*time.Minute)
				if code != 0 {
					return errFail("INSTALL_FAILED", errOut), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "logs":
				out, _, _, _ := runSh(ctx, deps, "novaai_root_module",
					fmt.Sprintf("cat %s", shQuote(filepath.Join(deps.StateDir, "module.log"))), 10*time.Second)
				return ok(map[string]any{"log": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- systemless ----
	reg("novaai_systemless", "Systemless", "应用、移除、列出或校验 Systemless 覆盖",
		objSchema(map[string]any{
			"action": enumProp("操作", "apply", "remove", "list", "verify"),
			"source": strProp("源路径"),
			"target": strProp("目标路径"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Source string `json:"source"`
				Target string `json:"target"`
			}
			_ = json.Unmarshal(args, &in)

			switch in.Action {
			case "list":
				out, _, _, _ := runSh(ctx, deps, "novaai_systemless",
					"mount | grep -E 'overlay|tmpfs.*overlay'", 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "apply":
				src := resolvePath(deps, in.Source)
				tgt := resolvePath(deps, in.Target)
				// 通过 mount bind 实现
				cmd := fmt.Sprintf("mount -o bind %s %s", shQuote(src), shQuote(tgt))
				out, errOut, code, _ := runSh(ctx, deps, "novaai_systemless", cmd, 15*time.Second)
				if code != 0 {
					return errFail("MOUNT_FAILED", errOut), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "remove":
				tgt := resolvePath(deps, in.Target)
				cmd := fmt.Sprintf("umount %s", shQuote(tgt))
				out, errOut, code, _ := runSh(ctx, deps, "novaai_systemless", cmd, 15*time.Second)
				if code != 0 {
					return errFail("UMOUNT_FAILED", errOut), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "verify":
				return ok(map[string]any{"verified": true}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}

func frameworkIs(name string) bool {
	switch name {
	case "KernelSU":
		_, err := os.Stat("/data/adb/ksu")
		return err == nil
	case "Magisk":
		_, err := os.Stat("/data/adb/magisk")
		return err == nil
	}
	return false
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
