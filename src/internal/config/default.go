package config

import "path"

// 结构体之外的内部常量。
//
// 它们不再是配置项：方案里配置只保留 §2 列出的旋钮，其余按固定行为处理。
// 取值沿用重构前的默认值，行为不变。
const (
	// MaxRequestBytes 是请求体上限（64MB），超限返回 -32600。
	MaxRequestBytes int64 = 67108864

	// ShutdownGraceSec 是优雅关闭等待时间。
	ShutdownGraceSec = 30

	// DefaultStateDir 是状态目录的兜底路径。
	//
	// 它是**唯一的字面量来源**：默认配置、以及任何需要在配置缺席时说出
	// 这个路径的地方（例如 mcp 的 initialize instructions）都引用它。
	DefaultStateDir = "/data/adb/novaai-mcp"
)

// Default 返回全新安装时写入的配置。
func Default() *Config {
	stateDir := DefaultStateDir

	return &Config{
		StateDir:   stateDir,
		Listen:     "0.0.0.0:5322",
		UnixSocket: path.Join(stateDir, "mcp.sock"),
		// 档位固定为 default：配置里写不出别的值，会话也不携带权限。
		Profile: "default",
		Limits: Limits{
			GlobalQPS:     50,
			ShellQPS:      10,
			MaxConcurrent: 5,
		},
		Audit: AuditConfig{
			Enabled:       true,
			RetentionDays: 7,
			MaxFileBytes:  10485760,
		},
		// shell/script 未显式给 timeoutMs 时的默认超时。
		ShellTimeoutSeconds: 60,
		// 单个工具结果的字节上限。超过就截断 Content 并丢弃
		// structuredContent —— 否则一个 dumpsys 就能撑爆 JSON-RPC 帧。
		ResultPreviewBytes: 1048576,
		// 上游 MCP 聚合。默认空：装完即用不需要任何上游。
		// 用 `[]UpstreamConfig{}` 而不是 nil，保证 JSON 序列化出 `[]`
		// 而不是 `null`（文档示例与 example_test 都按 `[]` 对齐）。
		Upstreams: []UpstreamConfig{},
	}
}
