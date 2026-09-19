package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 标准路径常量
const (
	reverseBaseDir = "/sdcard/novaaiAI/reverse"
	apksDir        = reverseBaseDir + "/apks"
	decompiledDir  = reverseBaseDir + "/decompiled"
	dexDir         = reverseBaseDir + "/dex"
	stringsDir     = reverseBaseDir + "/strings"
	hooksDir       = reverseBaseDir + "/hooks"
)

// Termux 环境变量前缀
const termuxEnvPrefix = `PREFIX=/data/data/com.termux/files/usr HOME=/data/data/com.termux/files/home PATH=$PREFIX/bin:$PREFIX/bin/applets LD_LIBRARY_PATH=$PREFIX/lib`

// apktoolJarPath 返回随模块分发的 apktool.jar 的绝对路径。
//
// jar 与 daemon 二进制同在模块内：<mod>/bin/<abi>/novaaimcpd 与
// <mod>/bin/tools/*.jar；wrapper 脚本（bin/wrappers/apktool）读的也是这一份。
// 路径从可执行文件位置推导，避免在 Go 里再写死一遍模块 ID。
//
// 旧实现读状态目录 /data/adb/novaai-mcp/tools/apktool.jar —— 那是安装时复制
// 出来的第二份副本，同一批 jar 在设备上存两遍（约 31 MiB）且可能版本漂移。
// 见 docs/KNOWN_ISSUES.md。
func apktoolJarPath() string {
	exe, err := os.Executable()
	if err != nil {
		// 不设回退路径：os.Executable 在 Linux/Android 上读 /proc/self/exe，
		// 失败属异常情况。返回相对名会让 java 报出明确的
		// "unable to access jarfile"，好过静默读到一份陈旧副本。
		return "apktool.jar"
	}
	return filepath.Join(filepath.Dir(filepath.Dir(exe)), "tools", "apktool.jar")
}

// ensureDirs 确保目录存在
func ensureDirs() {
	for _, dir := range []string{apksDir, decompiledDir, dexDir, stringsDir, hooksDir} {
		os.MkdirAll(dir, 0755)
	}
}

func registerReverseTools(reg RegisterFn, deps *Deps) {
	// 初始化目录
	ensureDirs()

	// reverse_apk
	reg("novaai_reverse_apk", "APK逆向", "提取、反编译、分析APK文件\n输出目录: /sdcard/novaaiAI/reverse/",
		objSchema(map[string]any{
			"action":  enumProp("操作", "extract", "decompile", "info", "manifest"),
			"package": strProp("包名"),
			"path":    strProp("APK路径"),
			"output":  strProp("输出目录（默认: /sdcard/novaaiAI/reverse/decompiled/包名）"),
			"tool":    enumProp("工具", "apktool", "jadx", "auto"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Package string `json:"package"`
				Path    string `json:"path"`
				Output  string `json:"output"`
				Tool    string `json:"tool"`
			}
			_ = json.Unmarshal(args, &in)
			apkPath := in.Path
			if apkPath == "" && in.Package != "" {
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_apk", fmt.Sprintf("pm path %s", shQuote(in.Package)), 15*time.Second)
				if out != "" {
					apkPath = strings.TrimPrefix(strings.TrimSpace(out), "package:")
				}
			}
			if apkPath == "" {
				return errFail("NO_APK", "需要指定 package 或 path"), nil
			}
			output := in.Output
			if output == "" {
				output = filepath.Join(decompiledDir, filepath.Base(apkPath))
			}
			switch in.Action {
			case "extract":
				_ = os.MkdirAll(apksDir, 0755)
				dest := filepath.Join(apksDir, filepath.Base(apkPath))
				out, stderr, code, err := runSh(ctx, deps, "novaai_reverse_apk", fmt.Sprintf("cp %s %s", shQuote(apkPath), shQuote(dest)), 30*time.Second)
				if err != nil || code != 0 {
					return errFail("EXTRACT_FAILED", stderr), nil
				}
				return ok(map[string]any{"path": dest, "output": out}), nil
			case "decompile":
				tool := in.Tool
				if tool == "auto" {
					tool = "apktool"
				}
				_ = os.MkdirAll(output, 0755)
				var cmd string
				switch tool {
				case "apktool":
					cmd = fmt.Sprintf("%s /data/data/com.termux/files/usr/bin/java -jar %s d -s -f -o %s %s", termuxEnvPrefix, shQuote(apktoolJarPath()), shQuote(output), shQuote(apkPath))
				case "jadx":
					cmd = fmt.Sprintf("%s /data/data/com.termux/files/usr/bin/java -jar /data/data/com.termux/files/usr/share/java/jadx-1.5.5-all.jar -d %s %s", termuxEnvPrefix, shQuote(output), shQuote(apkPath))
				default:
					return errFail("UNKNOWN_TOOL", tool), nil
				}
				out, stderr, code, err := runSh(ctx, deps, "novaai_reverse_apk", cmd, 5*time.Minute)
				if err != nil || code != 0 {
					return errFail("DECOMPILE_FAILED", stderr), nil
				}
				return ok(map[string]any{"tool": tool, "output": output, "log": out}), nil
			case "info":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_apk", fmt.Sprintf("dumpsys package %s 2>/dev/null | head -50", shQuote(in.Package)), 30*time.Second)
				return ok(map[string]any{"path": apkPath, "info": out}), nil
			case "manifest":
				_ = os.MkdirAll(output, 0755)
				manifestPath := filepath.Join(output, "AndroidManifest.xml")
				out, stderr, code, err := runSh(ctx, deps, "novaai_reverse_apk", fmt.Sprintf("aapt dump xmltree %s AndroidManifest.xml > %s 2>/dev/null", shQuote(apkPath), shQuote(manifestPath)), 30*time.Second)
				if err != nil || code != 0 {
					return errFail("MANIFEST_FAILED", stderr), nil
				}
				return ok(map[string]any{"path": manifestPath, "output": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// reverse_dex
	reg("novaai_reverse_dex", "DEX分析", "分析DEX文件结构和类信息\n输出目录: /sdcard/novaaiAI/reverse/dex/",
		objSchema(map[string]any{
			"action":  enumProp("操作", "list", "classes", "methods", "strings"),
			"path":    strProp("DEX或APK路径"),
			"class":   strProp("类名"),
			"pattern": strProp("搜索模式"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Path    string `json:"path"`
				Class   string `json:"class"`
				Pattern string `json:"pattern"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Path == "" {
				return errFail("NO_PATH", "需要指定 path"), nil
			}
			switch in.Action {
			case "list":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_dex", fmt.Sprintf("unzip -l %s 2>/dev/null | grep '\\.dex'", shQuote(in.Path)), 15*time.Second)
				return ok(map[string]any{"files": splitLines(out)}), nil
			case "classes":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_dex", fmt.Sprintf("dexdump %s 2>/dev/null | grep 'Class descriptor' | head -100", shQuote(in.Path)), 30*time.Second)
				return ok(map[string]any{"classes": splitLines(out)}), nil
			case "methods":
				if in.Class == "" {
					return errFail("NO_CLASS", "需要指定 class"), nil
				}
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_dex", fmt.Sprintf("dexdump %s 2>/dev/null | grep -A 50 %s | grep 'name' | head -50", shQuote(in.Path), shQuote("Class descriptor.*"+in.Class)), 30*time.Second)
				return ok(map[string]any{"methods": splitLines(out)}), nil
			case "strings":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_dex", fmt.Sprintf("strings %s 2>/dev/null | head -100", shQuote(in.Path)), 30*time.Second)
				return ok(map[string]any{"strings": splitLines(out)}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// reverse_smali
	reg("novaai_reverse_smali", "Smali分析", "查看和分析Smali代码\n输出目录: /sdcard/novaaiAI/reverse/decompiled/",
		objSchema(map[string]any{
			"action":  enumProp("操作", "disassemble", "assemble", "search"),
			"path":    strProp("DEX或APK路径"),
			"class":   strProp("类名"),
			"pattern": strProp("搜索模式"),
			"output":  strProp("输出目录（默认: /sdcard/novaaiAI/reverse/decompiled/）"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Path    string `json:"path"`
				Class   string `json:"class"`
				Pattern string `json:"pattern"`
				Output  string `json:"output"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Path == "" {
				return errFail("NO_PATH", "需要指定 path"), nil
			}
			output := in.Output
			if output == "" {
				output = filepath.Join(decompiledDir, filepath.Base(in.Path))
			}
			switch in.Action {
			case "disassemble":
				_ = os.MkdirAll(output, 0755)
				out, stderr, code, err := runSh(ctx, deps, "novaai_reverse_smali", fmt.Sprintf("%s /data/data/com.termux/files/usr/bin/java -jar %s d -f -o %s %s", termuxEnvPrefix, shQuote(apktoolJarPath()), shQuote(output), shQuote(in.Path)), 2*time.Minute)
				if err != nil || code != 0 {
					return errFail("DISASM_FAILED", stderr), nil
				}
				return ok(map[string]any{"output": output, "log": out}), nil
			case "assemble":
				dexPath := filepath.Join(output, "classes.dex")
				out, stderr, code, err := runSh(ctx, deps, "novaai_reverse_smali", fmt.Sprintf("%s /data/data/com.termux/files/usr/bin/java -jar %s b -f -o %s %s", termuxEnvPrefix, shQuote(apktoolJarPath()), shQuote(dexPath), shQuote(in.Path)), 2*time.Minute)
				if err != nil || code != 0 {
					return errFail("ASM_FAILED", stderr), nil
				}
				return ok(map[string]any{"path": dexPath, "output": out}), nil
			case "search":
				if in.Pattern == "" {
					return errFail("NO_PATTERN", "需要指定 pattern"), nil
				}
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_smali", fmt.Sprintf("grep -r %s %s 2>/dev/null | head -50", shQuote(in.Pattern), shQuote(in.Path)), 30*time.Second)
				return ok(map[string]any{"results": splitLines(out)}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// hook_frida
	reg("novaai_hook_frida", "Frida Hook", "动态Hook和调试应用\n输出目录: /sdcard/novaaiAI/reverse/hooks/",
		objSchema(map[string]any{
			"action":  enumProp("操作", "list", "attach", "spawn", "script", "kill"),
			"package": strProp("包名"),
			"pid":     intProp("进程ID"),
			"script":  strProp("Frida脚本"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Package string `json:"package"`
				PID     int    `json:"pid"`
				Script  string `json:"script"`
			}
			_ = json.Unmarshal(args, &in)
			fridaPath, err := lookPath("frida")
			if err != nil {
				return errFail("NO_FRIDA", "需要安装 frida: pip install frida-tools"), nil
			}
			switch in.Action {
			case "list":
				out, _, _, _ := runSh(ctx, deps, "novaai_hook_frida", fmt.Sprintf("%s-ps -U 2>/dev/null || %s-ps", fridaPath, fridaPath), 30*time.Second)
				return ok(map[string]any{"processes": splitLines(out)}), nil
			case "attach":
				if in.Package == "" && in.PID == 0 {
					return errFail("NO_TARGET", "需要指定 package 或 pid"), nil
				}
				var cmd string
				if in.Package != "" {
					cmd = fmt.Sprintf("%s -U %s", fridaPath, shQuote(in.Package))
				} else {
					cmd = fmt.Sprintf("%s -p %d", fridaPath, in.PID)
				}
				out, stderr, code, _ := runSh(ctx, deps, "novaai_hook_frida", cmd, 15*time.Second)
				if code != 0 {
					return errFail("ATTACH_FAILED", stderr), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "spawn":
				if in.Package == "" {
					return errFail("NO_PACKAGE", "需要指定 package"), nil
				}
				out, stderr, code, _ := runSh(ctx, deps, "novaai_hook_frida", fmt.Sprintf("%s -U -f %s --no-pause", fridaPath, shQuote(in.Package)), 15*time.Second)
				if code != 0 {
					return errFail("SPAWN_FAILED", stderr), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "script":
				if in.Package == "" {
					return errFail("NO_PACKAGE", "需要指定 package"), nil
				}
				if in.Script == "" {
					return errFail("NO_SCRIPT", "需要指定 script"), nil
				}
				scriptPath := filepath.Join(hooksDir, "hook.js")
				_ = os.MkdirAll(filepath.Dir(scriptPath), 0755)
				_ = os.WriteFile(scriptPath, []byte(in.Script), 0644)
				out, stderr, code, _ := runSh(ctx, deps, "novaai_hook_frida", fmt.Sprintf("%s -U -l %s %s", fridaPath, shQuote(scriptPath), shQuote(in.Package)), 30*time.Second)
				if code != 0 {
					return errFail("SCRIPT_FAILED", stderr), nil
				}
				return ok(map[string]any{"output": out}), nil
			case "kill":
				if in.Package == "" && in.PID == 0 {
					return errFail("NO_TARGET", "需要指定 package 或 pid"), nil
				}
				var cmd string
				if in.Package != "" {
					cmd = fmt.Sprintf("%s-kill -U %s 2>/dev/null", fridaPath, shQuote(in.Package))
				} else {
					cmd = fmt.Sprintf("kill %d", in.PID)
				}
				out, stderr, code, _ := runSh(ctx, deps, "novaai_hook_frida", cmd, 15*time.Second)
				if code != 0 {
					return errFail("KILL_FAILED", stderr), nil
				}
				return ok(map[string]any{"output": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// hook_xposed
	reg("novaai_hook_xposed", "Xposed管理", "管理Xposed/LSPosed模块",
		objSchema(map[string]any{
			"action":  enumProp("操作", "list", "enable", "disable", "status"),
			"module":  strProp("模块包名"),
			"package": strProp("目标应用包名"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Module  string `json:"module"`
				Package string `json:"package"`
			}
			_ = json.Unmarshal(args, &in)
			switch in.Action {
			case "list":
				modulesDir := "/data/adb/modules"
				entries, _ := os.ReadDir(modulesDir)
				var modules []map[string]any
				for _, e := range entries {
					mod := map[string]any{"id": e.Name(), "disabled": false}
					if _, err := os.Stat(filepath.Join(modulesDir, e.Name(), "disable")); err == nil {
						mod["disabled"] = true
					}
					modules = append(modules, mod)
				}
				return ok(map[string]any{"modules": modules}), nil
			case "enable":
				if in.Module == "" {
					return errFail("NO_MODULE", "需要指定 module"), nil
				}
				if !idRe.MatchString(in.Module) {
					return errFail("INVALID_PARAM", "非法 module: "+in.Module), nil
				}
				_ = os.Remove(filepath.Join("/data/adb/modules", in.Module, "disable"))
				return okMsg("模块已启用，重启生效"), nil
			case "disable":
				if in.Module == "" {
					return errFail("NO_MODULE", "需要指定 module"), nil
				}
				if !idRe.MatchString(in.Module) {
					return errFail("INVALID_PARAM", "非法 module: "+in.Module), nil
				}
				f, _ := os.Create(filepath.Join("/data/adb/modules", in.Module, "disable"))
				if f != nil {
					f.Close()
				}
				return okMsg("模块已禁用，重启生效"), nil
			case "status":
				lsExists := false
				if _, err := os.Stat("/data/adb/lsposed"); err == nil {
					lsExists = true
				}
				return ok(map[string]any{"lsposed": lsExists}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// reverse_strings
	reg("novaai_reverse_strings", "字符串提取", "从二进制文件中提取字符串\n输出目录: /sdcard/novaaiAI/reverse/strings/",
		objSchema(map[string]any{
			"action":  enumProp("操作", "extract", "search"),
			"path":    strProp("文件路径"),
			"pattern": strProp("搜索模式"),
			"min_len": intProp("最小长度"),
			"output":  strProp("输出文件（默认: /sdcard/novaaiAI/reverse/strings/文件名.txt）"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action  string `json:"action"`
				Path    string `json:"path"`
				Pattern string `json:"pattern"`
				MinLen  int    `json:"min_len"`
				Output  string `json:"output"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Path == "" {
				return errFail("NO_PATH", "需要指定 path"), nil
			}
			if in.MinLen == 0 {
				in.MinLen = 4
			}
			switch in.Action {
			case "extract":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_strings", fmt.Sprintf("strings -a %s 2>/dev/null | awk 'length >= %d' | head -200", shQuote(in.Path), in.MinLen), 30*time.Second)
				return ok(map[string]any{"strings": splitLines(out)}), nil
			case "search":
				if in.Pattern == "" {
					return errFail("NO_PATTERN", "需要指定 pattern"), nil
				}
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_strings", fmt.Sprintf("strings -a %s 2>/dev/null | grep -i %s | head -100", shQuote(in.Path), shQuote(in.Pattern)), 30*time.Second)
				return ok(map[string]any{"results": splitLines(out)}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// reverse_binary
	reg("novaai_reverse_binary", "二进制分析", "分析ELF/二进制文件",
		objSchema(map[string]any{
			"action": enumProp("操作", "info", "symbols", "sections", "hex"),
			"path":   strProp("文件路径"),
			"offset": intProp("偏移"),
			"length": intProp("长度"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Length int    `json:"length"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Path == "" {
				return errFail("NO_PATH", "需要指定 path"), nil
			}
			if in.Length == 0 {
				in.Length = 256
			}
			switch in.Action {
			case "info":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_binary", fmt.Sprintf("file %s 2>/dev/null", shQuote(in.Path)), 15*time.Second)
				header, _, _, _ := runSh(ctx, deps, "novaai_reverse_binary", fmt.Sprintf("readelf -h %s 2>/dev/null || echo 'not ELF'", shQuote(in.Path)), 15*time.Second)
				return ok(map[string]any{"file": out, "header": header}), nil
			case "symbols":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_binary", fmt.Sprintf("readelf -s %s 2>/dev/null | head -100 || nm %s 2>/dev/null | head -100", shQuote(in.Path), shQuote(in.Path)), 30*time.Second)
				return ok(map[string]any{"symbols": splitLines(out)}), nil
			case "sections":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_binary", fmt.Sprintf("readelf -S %s 2>/dev/null || objdump -h %s 2>/dev/null", shQuote(in.Path), shQuote(in.Path)), 15*time.Second)
				return ok(map[string]any{"sections": splitLines(out)}), nil
			case "hex":
				out, _, _, _ := runSh(ctx, deps, "novaai_reverse_binary", fmt.Sprintf("xxd -l %d -s %d %s 2>/dev/null || hexdump -C -n %d -s %d %s", in.Length, in.Offset, shQuote(in.Path), in.Length, in.Offset, shQuote(in.Path)), 15*time.Second)
				return ok(map[string]any{"hex": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// reverse_install_tools
	reg("novaai_reverse_install_tools", "安装工具", "安装逆向工具到设备",
		objSchema(map[string]any{
			"action": enumProp("操作", "check", "install_jadx", "install_apktool", "install_frida"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(args, &in)
			switch in.Action {
			case "check":
				tools := map[string]bool{"jadx": false, "apktool": false, "frida": false, "smali": false, "baksmali": false, "dexdump": false, "readelf": false, "xxd": false, "strings": false}
				for tool := range tools {
					if _, err := lookPath(tool); err == nil {
						tools[tool] = true
					}
				}
				return ok(map[string]any{"tools": tools}), nil
			case "install_jadx":
				return ok(map[string]any{"instructions": "pkg install jadx"}), nil
			case "install_apktool":
				return ok(map[string]any{"instructions": "pkg install apktool"}), nil
			case "install_frida":
				return ok(map[string]any{"instructions": "pip install frida-tools"}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}
