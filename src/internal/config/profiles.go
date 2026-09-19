package config

// DefaultProfiles 是内置 profile 的唯一定义处。
//
// config.Default()（全新安装）与 migrate.fillMissingV3Fields（旧配置升级）
// 都从这里取。此前两处各有一份，迁移路径那份更宽松——default 的
// denyTools 为空——于是"从旧版本迁移上来"的机器比"全新安装"的机器
// 多出 novaai_shell 权限，而 shell 是 pathguard 与 antibrick 的万能绕过。
//
// 修改默认权限时只改这里，两处入口自动一致。
func DefaultProfiles() map[string]Profile {
	return map[string]Profile{
		"default": {
			AllowTools: []string{"*"},
			// 通用 shell 是万能绕过：只要它可达，pathguard 与 antibrick
			// 就只是建议。默认把它交给 agent_full。
			// 另挡"自我管理"类工具：不让模型改服务配置或动 root 模块。
			DenyTools: []string{
				"novaai_shell", "novaai_script",
				"novaai_config", "novaai_root_module", "novaai_systemless",
			},
			RiskCeiling: 3,
		},
		"readonly": {
			AllowTools: []string{
				"novaai_status", "novaai_capabilities", "novaai_health_status",
				"novaai_root_info",
				"novaai_device_info", "novaai_fs_info", "novaai_fs_read",
				"novaai_fs_search", "novaai_fs_hash", "novaai_app_list",
				"novaai_app_info", "novaai_process", "novaai_log",
				"novaai_task", "novaai_skill", "novaai_diagnostics",
				"novaai_session_status", "novaai_session_list",
				"novaai_audit_status", "novaai_auth_status",
			},
			DenyTools:   []string{},
			RiskCeiling: 0,
		},
		"reverse": {
			AllowTools: []string{
				"novaai_reverse_*", "novaai_hook_*", "novaai_fs_read", "novaai_fs_info",
				"novaai_app_info", "novaai_app_list", "novaai_process", "novaai_log",
				"novaai_device_info", "novaai_status", "novaai_session_*",
			},
			DenyTools: []string{
				"novaai_shell", "novaai_script", "novaai_power", "novaai_root_module",
			},
			RiskCeiling: 3,
		},
		"agent_full": {
			AllowTools:  []string{"*"},
			DenyTools:   []string{},
			RiskCeiling: 3,
		},
	}
}
