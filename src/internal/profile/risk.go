package profile

import (
	"encoding/json"
	"sort"
)

// 风险等级：0 只读 / 1 普通写 / 2 修改设备状态 / 3 破坏性
//
// **这里只允许出现真实注册的工具名。** 曾经它列着 30 来个早已删除的工具
// （novaai_reverse_* / hook_* / skill / network / backup / device_info /
// session_* / auth_status…），而 audit_actions.ps1 没写 exit，所以那些幽灵
// 条目一直没让 CI 报警。现在由 `TestRiskTableMatchesRegistry` 锁住：这张表
// 与 `tools` 注册表必须互为子集。
var baseRiskLevels = map[string]int{
	// 只读
	"novaai_status":          0,
	"novaai_capabilities":    0,
	"novaai_health_status":   0,
	"novaai_upstream_status": 0,
	"novaai_root_info":       0,
	"novaai_fs_info":         0,
	"novaai_fs_read":         0,
	"novaai_fs_search":       0,
	"novaai_fs_hash":         0,
	"novaai_app_list":        0,
	"novaai_app_info":        0,
	"novaai_process":         0,
	"novaai_log":             0,
	"novaai_diagnostics":     0,

	// 普通写
	"novaai_fs_write":        1,
	"novaai_archive":         1,
	"novaai_download":        1,
	"novaai_transfer_upload": 1,
	"novaai_transfer_export": 1,
	"novaai_screen":          1,

	// 修改设备状态
	"novaai_shell":       2,
	"novaai_script":      2,
	"novaai_fs_manage":   2,
	"novaai_app_install": 2,
	"novaai_app_manage":  2,
	"novaai_input":       2,

	// 破坏性
	"novaai_power":       3,
	"novaai_root_module": 3,
	"novaai_systemless":  3,
	"novaai_config":      3,
}

// perActionRisk 按 action 细分某个工具的风险；未列到的 action 用 baseRiskLevels。
//
// 与 baseRiskLevels 一样，键集合受 `TestRiskTableMatchesRegistry` 约束。
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
	"novaai_config": {
		"get": 0, "validate": 0, "export": 0,
		// probe_upstreams 只是探测，不改状态；另外两个会重建注册表 /
		// 重启上游子进程，属于能改变设备运行状态的操作。
		"probe_upstreams":  0,
		"reload_upstreams": 3,
		"restart_upstream": 3,
		"update":           3,
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

// KnownTools 返回两张表里出现过的工具名，供一致性测试与文档生成使用。
//
// 它是这张表的**唯一出口**：测试拿它与 tools 注册表做双向比对，这样
// "删了工具忘了删风险条目"和"加了工具没登记风险"都会当场失败。
func KnownTools() []string {
	set := map[string]bool{}
	for name := range baseRiskLevels {
		set[name] = true
	}
	for name := range perActionRisk {
		set[name] = true
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
