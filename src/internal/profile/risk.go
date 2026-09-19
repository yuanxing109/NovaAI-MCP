package profile

import (
	"encoding/json"
)

// 风险等级：0 只读 / 1 普通写 / 2 修改设备状态 / 3 破坏性
var baseRiskLevels = map[string]int{
	"novaai_status":         0,
	"novaai_capabilities":   0,
	"novaai_health_status":  0,
	"novaai_root_info":      0,
	"novaai_device_info":    0,
	"novaai_fs_info":        0,
	"novaai_fs_read":        0,
	"novaai_fs_search":      0,
	"novaai_fs_hash":        0,
	"novaai_app_list":       0,
	"novaai_app_info":       0,
	"novaai_process":        0,
	"novaai_log":            0,
	"novaai_skill":          0,
	"novaai_diagnostics":    0,
	"novaai_audit_status":   0,
	"novaai_auth_status":    0,
	"novaai_session_status": 0,
	"novaai_session_list":   0,

	"novaai_fs_write":        1,
	"novaai_archive":         1,
	"novaai_download":        1,
	"novaai_transfer_upload": 1,
	"novaai_transfer_export": 1,
	"novaai_app_export":      1,
	"novaai_screen":          1,
	"novaai_reverse_apk":     1,
	"novaai_reverse_dex":     1,
	"novaai_reverse_smali":   1,
	"novaai_reverse_strings": 1,
	"novaai_reverse_binary":  1,

	"novaai_shell":                 2,
	"novaai_script":                2,
	"novaai_fs_manage":             2,
	"novaai_hook_frida":            2,
	"novaai_hook_xposed":           2,
	"novaai_reverse_install_tools": 2,
	"novaai_app_install":           2,
	"novaai_app_manage":            2,
	"novaai_app_permission":        2,
	"novaai_app_policy":            2,
	"novaai_default_app":           2,
	"novaai_notification":          2,
	"novaai_property":              2,
	"novaai_setting":               2,
	"novaai_service":               2,
	"novaai_input":                 2,
	"novaai_display":               2,
	"novaai_audio":                 2,
	"novaai_connectivity":          2,
	"novaai_locale_time":           2,
	"novaai_input_method":          2,
	"novaai_developer":             2,
	"novaai_accessibility":         2,
	"novaai_network":               2,
	"novaai_schedule":              2,
	"novaai_backup":                2,

	"novaai_power":       3,
	"novaai_root_module": 3,
	"novaai_systemless":  3,
	"novaai_config":      3,
}

var perActionRisk = map[string]map[string]int{
	"novaai_process": {
		"list": 0, "info": 0, "fds": 0,
		"signal": 2, "kill": 2, "renice": 2,
	},
	"novaai_log": {
		"logcat": 0, "kernel": 0, "dmesg": 0, "module": 0, "mcp": 0,
		"stream": 1, "clear": 3,
	},
	"novaai_screen": {
		"screenshot": 1, "foreground": 1, "record": 2, "wake": 2, "sleep": 2,
	},
	"novaai_backup": {
		"list": 0, "verify": 0,
		"create": 1, "restore": 3, "remove": 3,
	},
	"novaai_config": {
		"get": 0, "validate": 0, "export": 0,
		"update": 3,
	},
	// 只列真实存在的 action。早期这里还写着 web_extract / browser_capture /
	// feed_parse / cookie_* / ws_* —— 那些 action 从未实现过。
	"novaai_network": {
		"interfaces": 0, "routes": 0, "dns": 0, "ping": 0,
		"resolve": 0, "ports": 0, "connections": 0, "wifi": 0,
		"proxy": 0, "connectivity": 0,
		"http": 2,
	},
}

// ResolveRisk 按工具名 + 参数推断实际风险等级
func ResolveRisk(tool string, args json.RawMessage) int {
	if fn, ok := perActionRisk[tool]; ok {
		var a struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(args, &a)
		if lvl, ok := fn[a.Action]; ok {
			return lvl
		}
	}
	if lvl, ok := baseRiskLevels[tool]; ok {
		return lvl
	}
	return 1
}
