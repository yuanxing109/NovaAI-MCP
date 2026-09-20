package v02

import (
	"context"
	"encoding/json"

	"github.com/novaai/novaai-mcp/internal/adapter"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/upstream"
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
	// Upstreams 是上游 MCP 聚合注册表，可以为 nil（测试里常见）。
	Upstreams *upstream.Registry
}

// RegisterAllV02Tools 注册 v0.02 的工具。共 28 个（另有 2 个在 tools 包内
// 直注册：novaai_health_status、novaai_upstream_status，合计 30，
// 权威数量以 tools.Registry.Count() 为准）。
//
// 工具清单：
//
//	服务：    novaai_status, capabilities, config, diagnostics
//	文件：    novaai_fs_info, fs_read, fs_write, fs_manage, fs_search, fs_hash
//	归档/传输：novaai_archive, download, transfer_upload, transfer_export
//	执行：    novaai_shell, script
//	应用：    novaai_app_list, app_info, app_install, app_manage
//	系统：    novaai_process, log, screen, input, power
//	Root：    novaai_root_info, root_module, systemless
//
// 被删除的工具（reverse_*、hook_*、skill、schedule、app_permission、
// app_policy、app_export、default_app、notification、service、property、
// setting、display、audio、connectivity、locale_time、input_method、
// developer、accessibility、network、backup、device_info）一律走 novaai_shell。
//
// **上游 MCP 的工具不在这里注册**：它们的数量随用户配置动态变化，
// 由 upstream.Registry.MergedTools() 在 tools/list 时实时合并，
// 不进入本注册表，因此不参与 expectedToolCount 断言。
func RegisterAllV02Tools(reg RegisterFn, deps *Deps) {
	registerStatusTools(reg, deps)
	registerFSTools(reg, deps)
	registerArchiveTools(reg, deps)
	registerExecTools(reg, deps)
	registerAppTools(reg, deps)
	registerRootTools(reg, deps)
	registerSysTools(reg, deps)
	registerSettingTools(reg, deps)
	registerNetLogTools(reg, deps)
}
