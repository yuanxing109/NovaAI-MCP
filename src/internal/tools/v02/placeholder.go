package v02

import (
	"context"
	"encoding/json"

	"github.com/novaai/novaai-mcp/internal/adapter"
	"github.com/novaai/novaai-mcp/internal/config"
)

// Handler 与 tools.Handler 签名一致，通过回调避免循环依赖。
type Handler func(ctx context.Context, args json.RawMessage) (any, error)

// RegisterFn 由 tools 包提供，用于将 v0.02 工具注册到主注册表。
type RegisterFn func(name, title, desc string, schema map[string]any, handler Handler)

type Deps struct {
	Config   *config.Config
	Adapter  adapter.Adapter
	StateDir string
	Version  string
	Commit   string
}

// RegisterAllV02Tools 注册 v0.02 的全部工具。
//
// 工具清单（权威数量以 tools.Registry.Count() 为准，这里不再写死数字）：
//
//	服务/状态：    novaai_status, capabilities, config, diagnostics
//	文件：         novaai_fs_info, fs_read, fs_write, fs_manage, fs_search, fs_hash
//	归档/传输：    novaai_archive, download, transfer_upload, transfer_export
//	执行：         novaai_shell, script
//	应用：         novaai_app_list, app_info, app_install, app_manage, app_permission,
//	               app_export, app_policy, default_app, notification
//	Root：         novaai_root_info, root_module, systemless, backup
//	系统：         novaai_process, service, property, setting
//	设置：         novaai_display, audio, connectivity, locale_time, input_method,
//	               developer, power, screen, input, accessibility
//	设备/调度：    novaai_device_info, schedule
//	网络/日志：    novaai_network, log
//	技能：         novaai_skill
func RegisterAllV02Tools(reg RegisterFn, deps *Deps) {
	registerStatusTools(reg, deps)
	registerFSTools(reg, deps)
	registerArchiveTools(reg, deps)
	registerExecTools(reg, deps)
	registerAppTools(reg, deps)
	registerRootTools(reg, deps)
	registerSysTools(reg, deps)
	registerSettingTools(reg, deps)
	registerDeviceTools(reg, deps)
	registerNetLogTools(reg, deps)
	registerSkillTools(reg, deps)
	registerReverseTools(reg, deps)
}
