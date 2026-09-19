package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/novaai/novaai-mcp/internal/pathguard"
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

	// ---- backup ----
	reg("novaai_backup", "备份", "创建、列出、验证、恢复或删除备份",
		objSchema(map[string]any{
			"action":           enumProp("操作", "create", "list", "verify", "restore", "remove"),
			"path":             strProp("备份路径"),
			"sources":          arrProp("源路径数组"),
			"destination":      strProp("目标路径"),
			"sha256":           strProp("期望 SHA-256"),
			"confirmDangerous": boolProp("确认破坏性"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action      string   `json:"action"`
				Path        string   `json:"path"`
				Sources     []string `json:"sources"`
				Destination string   `json:"destination"`
				SHA256      string   `json:"sha256"`
				Confirm     bool     `json:"confirmDangerous"`
			}
			_ = json.Unmarshal(args, &in)

			backupDir := filepath.Join(deps.StateDir, "backups")

			switch in.Action {
			case "create":
				_ = os.MkdirAll(backupDir, 0700)
				name := fmt.Sprintf("backup-%d.tar.gz", time.Now().Unix())
				tgt := filepath.Join(backupDir, name)
				if in.Destination != "" {
					tgt = resolvePath(deps, in.Destination)
				}
				if err := guardPath(tgt, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}

				var srcs []string
				for _, s := range in.Sources {
					srcs = append(srcs, resolvePath(deps, s))
				}
				if len(srcs) == 0 {
					srcs = []string{deps.StateDir}
				}

				cmd := fmt.Sprintf("tar -czf %s %s", shQuote(tgt), quoteAll(srcs))
				_, errOut, code, _ := runSh(ctx, deps, "novaai_backup", cmd, 5*time.Minute)
				if code != 0 {
					return errFail("BACKUP_FAILED", errOut), nil
				}

				sha, _ := computeHash(tgt, "sha256")
				return ok(map[string]any{"path": tgt, "sha256": sha}), nil
			case "list":
				entries, _ := os.ReadDir(backupDir)
				var items []map[string]any
				for _, e := range entries {
					info, _ := e.Info()
					if info != nil {
						items = append(items, map[string]any{
							"name":    e.Name(),
							"size":    info.Size(),
							"modTime": info.ModTime().Format(time.RFC3339),
						})
					}
				}
				return ok(map[string]any{"backups": items}), nil
			case "verify":
				p := in.Path
				if p == "" {
					return errFail("MISSING_PATH", "path 必填"), nil
				}
				p = resolvePath(deps, p)
				actual, err := computeHash(p, "sha256")
				if err != nil {
					return errFail("HASH_FAILED", err.Error()), nil
				}
				match := in.SHA256 == "" || equalFold(actual, in.SHA256)
				return ok(map[string]any{"match": match, "sha256": actual}), nil
			case "restore":
				if !in.Confirm {
					return errFail("NOT_CONFIRMED", "restore 需要 confirmDangerous: true"), nil
				}
				p := resolvePath(deps, in.Path)
				dst := in.Destination
				if dst == "" {
					dst = "/"
				} else {
					dst = resolvePath(deps, dst)
				}
				// dst 默认是 "/"，不能整体拒绝；真正的防线是成员检查：
				// 归档里只要有条目落到分区或模块目录，就拒绝整次恢复。
				if err := verifyTarMembers(ctx, deps, p, dst); err != nil {
					return errFail("UNSAFE_ARCHIVE", err.Error()), nil
				}
				cmd := fmt.Sprintf("tar -xzf %s -C %s", shQuote(p), shQuote(dst))
				_, errOut, code, _ := runSh(ctx, deps, "novaai_backup", cmd, 5*time.Minute)
				if code != 0 {
					return errFail("RESTORE_FAILED", errOut), nil
				}
				return ok(map[string]any{"restored": p, "to": dst}), nil
			case "remove":
				if !in.Confirm {
					return errFail("NOT_CONFIRMED", "remove 需要 confirmDangerous: true"), nil
				}
				p := resolvePath(deps, in.Path)
				if err := guardPath(p, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := os.Remove(p); err != nil {
					return errFail("REMOVE_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"removed": p}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}

// verifyTarMembers 在解压前列出归档成员，拒绝会落到受保护位置的条目。
//
// 这是 restore 的真正防线：只检查 dst 是不够的（dst 默认就是 "/"），
// 必须看归档里到底有什么。绝对路径与 ".." 一律拒绝。
func verifyTarMembers(ctx context.Context, deps *Deps, archive, dst string) error {
	out, errOut, code, _ := runSh(ctx, deps, "novaai_backup",
		fmt.Sprintf("tar -tzf %s", shQuote(archive)), 60*time.Second)
	if code != 0 {
		return fmt.Errorf("无法读取归档成员: %s", errOut)
	}

	for _, line := range splitLines(out) {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "/") {
			return fmt.Errorf("归档包含绝对路径成员: %s", name)
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return fmt.Errorf("归档包含路径穿越成员: %s", name)
			}
		}
		target := filepath.Join(dst, name)
		if d := pathguard.CheckSystemPath(target, false); !d.Allowed {
			return fmt.Errorf("归档成员落在受保护位置: %s（%s）", target, d.Rule)
		}
	}
	return nil
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

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
