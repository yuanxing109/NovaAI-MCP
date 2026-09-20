package config

import "path"

// Config 是服务的全部配置。
//
// 单用户、自有设备、装完即用：没有 token，不区分来源，档位固定 default。
// `listen` 是唯一的入口开关 —— 0.0.0.0:5322 局域网直连，
// 改成 127.0.0.1:5322 就是本地模式，无需改代码。
type Config struct {
	StateDir            string           `json:"stateDir"`
	Listen              string           `json:"listen"`
	UnixSocket          string           `json:"unixSocket"`
	Profile             string           `json:"profile"`
	Limits              Limits           `json:"limits"`
	Audit               AuditConfig      `json:"audit"`
	ShellTimeoutSeconds int              `json:"shellTimeoutSeconds"`
	ResultPreviewBytes  int64            `json:"resultPreviewBytes"`
	Upstreams           []UpstreamConfig `json:"upstreams"`
}

// WorkspaceRoot 是相对路径的解析基准，固定为 {stateDir}/workspace。
//
// 用 path.Join 而不是 filepath.Join：这些是 Android 的 POSIX 路径，
// 不管进程跑在哪个平台上都必须保持正斜杠，否则 pathguard 会把
// `\data\adb\novaai-mcp\workspace` 当成相对路径，保护静默失效。
func (c *Config) WorkspaceRoot() string {
	return path.Join(c.StateDir, "workspace")
}

// AuditDir 是审计日志目录，固定为 {stateDir}/audit。
func (c *Config) AuditDir() string {
	return path.Join(c.StateDir, "audit")
}

// Limits 是限流的三个旋钮。qps <= 0 表示该层不限流。
type Limits struct {
	GlobalQPS     float64 `json:"globalQps"`
	ShellQPS      float64 `json:"shellQps"`
	MaxConcurrent int     `json:"maxConcurrent"`
}

// AuditConfig 只保留已实现的行为：按日 JSONL + 单文件上限 + 保留天数。
type AuditConfig struct {
	Enabled       bool  `json:"enabled"`
	RetentionDays int   `json:"retentionDays"`
	MaxFileBytes  int64 `json:"maxFileBytes"`
}

// Profile 定义一组允许/拒绝的工具列表与风险上限。
//
// 现在只有 default 一个档位（见 DefaultProfiles）；类型保留是因为
// profile.Allows 的判定语义没变。
type Profile struct {
	AllowTools  []string `json:"allowTools"`
	DenyTools   []string `json:"denyTools"`
	RiskCeiling int      `json:"riskCeiling"`
}

// ---- 上游 MCP 聚合 ----

// 上游类型。
const (
	UpstreamTypeHTTP  = "http"
	UpstreamTypeStdio = "stdio"
)

// 上游状态。它们是**运行时**状态，不落盘：每次启动重新探测。
//
// 见 docs/upstream.md。这里定义常量是为了让 WebUI / 审计 / 工具输出
// 用同一组字面量，不要在各处手写字符串。
const (
	UpstreamRunning  = "running"
	UpstreamStopped  = "stopped"
	UpstreamError    = "error"
	UpstreamDisabled = "disabled"
)

// 启动方式。
const (
	LaunchIntent  = "intent"
	LaunchCommand = "command"
	LaunchManual  = "manual"
)

// DefaultUpstreamRiskCeiling 是上游缺省的风险上限，与全局 default 档位一致。
const DefaultUpstreamRiskCeiling = 3

// UpstreamConfig 描述一个上游 MCP 服务。
//
// 上游由用户在 WebUI 里增删改，持久化在 config.json 的 upstreams 数组里。
// 它的工具以 `{name}__{tool}` 合并进本服务的 tools/list。
type UpstreamConfig struct {
	// Name 是唯一标识，同时用作工具名前缀。禁止包含 `__`（命名空间分隔符）。
	Name string `json:"name"`
	// Type 只能是 http 或 stdio。
	Type string `json:"type"`
	// URL 仅 http 用；Command + Args 仅 stdio 用。
	URL     string   `json:"url,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// Enabled 缺省即 false（不参与聚合）。fail-closed：漏写不会意外暴露上游。
	Enabled bool `json:"enabled"`
	// RiskCeiling <= 0 表示继承 DefaultUpstreamRiskCeiling。
	//
	// 注意 0 与"缺省"无法区分（Go 的 int 没有 nil），所以 0 一律按继承处理，
	// 即无法用 riskCeiling 表达"只放行 risk 0 的工具"。需要那种粒度请用
	// denyTools。这一条写在 docs/upstream.md 里。
	RiskCeiling int `json:"riskCeiling,omitempty"`
	// DenyTools 按裸名或带前缀名匹配，命中即拒绝。
	DenyTools []string `json:"denyTools,omitempty"`
	// ExposeWhenStopped 为 true 时，未运行的上游工具仍然出现在 tools/list 里，
	// 调用返回 isError。默认 false。
	ExposeWhenStopped bool `json:"exposeWhenStopped,omitempty"`
	// AutoLaunch 为 true 时，调用未运行的上游会先尝试按 Launch 启动。
	AutoLaunch bool `json:"autoLaunch,omitempty"`
	// Launch 缺省等同 {type: "manual"}。
	Launch *LaunchConfig `json:"launch,omitempty"`
}

// LaunchConfig 描述"怎么把一个未运行的上游拉起来"。
type LaunchConfig struct {
	// Type 为 intent / command / manual。
	Type string `json:"type"`
	// intent：package 必填，action 与 activity 二选一。
	Package  string `json:"package,omitempty"`
	Action   string `json:"action,omitempty"`
	Activity string `json:"activity,omitempty"`
	// command：直接 spawn（不走 shell 解释，参数逐个传递）。
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// EffectiveRiskCeiling 返回实际生效的风险上限。
func (u *UpstreamConfig) EffectiveRiskCeiling() int {
	if u.RiskCeiling <= 0 {
		return DefaultUpstreamRiskCeiling
	}
	return u.RiskCeiling
}

// LaunchType 返回实际生效的启动方式，缺省为 manual。
func (u *UpstreamConfig) LaunchType() string {
	if u.Launch == nil || u.Launch.Type == "" {
		return LaunchManual
	}
	return u.Launch.Type
}
