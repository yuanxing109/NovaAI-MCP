package config

type Config struct {
	SchemaVersion  int                `json:"schemaVersion"`
	Network        Network            `json:"network"`
	Paths          Paths              `json:"paths"`
	Limits         Limits             `json:"limits"`
	Security       Security           `json:"security"`
	Profiles       map[string]Profile `json:"profiles"`
	SessionBinding SessionBinding     `json:"sessionBinding"`
	Audit          AuditConfig        `json:"audit"`
	RateLimit      RateLimitConfig    `json:"rateLimit"`
	Session        SessionConfig      `json:"session"`
	Uninstall      UninstallConfig    `json:"uninstall"`
}

type Network struct {
	Port           int      `json:"port"`
	ListenLoopback bool     `json:"listenLoopback"`
	ListenLAN      bool     `json:"listenLan"`
	AllowedOrigins []string `json:"allowedOrigins"`
}

// Paths 只保留真正被读取的目录。
//
// 子目录（downloads/uploads/artifacts/tmp）不再单独配置：stateDir 就是那个
// 旋钮，四个可覆盖的子目录只会和 pathguard 的角色白名单产生同步负担。
//
// crashDir 同样已移除，但理由是硬的：崩溃处理器必须在配置加载**之前**就
// 装好（见 main.go 的顺序），否则配置解析阶段自身的 panic 没有兜底。
// 一个只能在配置就绪后才可能生效的字段等于没有这个字段，所以崩溃目录固定
// 为 {stateDir}/crash。
type Paths struct {
	StateDir      string `json:"stateDir"`
	WorkDir       string `json:"workDir"`
	WorkspaceRoot string `json:"workspaceRoot"`
	AuditDir      string `json:"auditDir"`
}

type Limits struct {
	MaxRequestBytes    int64 `json:"maxRequestBytes"`
	ShellTimeoutSec    int   `json:"shellTimeoutSeconds"`
	ResultPreviewBytes int64 `json:"resultPreviewBytes"`
	ShutdownGraceSec   int   `json:"shutdownGraceSeconds"`
}

type Security struct {
	Anonymous      bool       `json:"anonymous"`
	ValidateHost   bool       `json:"validateHost"`
	ValidateOrigin bool       `json:"validateOrigin"`
	AllowCORS      bool       `json:"allowCors"`
	Token          TokenConf  `json:"token"`
	UnixSocket     UnixSocket `json:"unixSocket"`
	LAN            LANConf    `json:"lan"`
}

type TokenConf struct {
	Enabled         bool   `json:"enabled"`
	Value           string `json:"value"`
	RotateOnStart   bool   `json:"rotateOnStart"`
	AllowQueryParam bool   `json:"allowQueryParam"`
}

type UnixSocket struct {
	Enabled        bool   `json:"enabled"`
	Path           string `json:"path"`
	Mode           string `json:"mode"`
	SepolicyInject bool   `json:"sepolicyInject"`
}

type LANConf struct {
	Enabled     bool     `json:"enabled"`
	AllowedCIDR []string `json:"allowedCidr"`
}

type Profile struct {
	AllowTools  []string `json:"allowTools"`
	DenyTools   []string `json:"denyTools"`
	RiskCeiling int      `json:"riskCeiling"`
}

type SessionBinding struct {
	ByTokenHash map[string]string `json:"byTokenHash"`
	Fallback    string            `json:"fallback"`
}

// AuditConfig 只保留已实现的行为。
//
// redactMode 已移除：只有 allowlist 一种脱敏实现，保留 none/all 这类
// 未实现的枚举只会让人以为关掉脱敏是可行的。
type AuditConfig struct {
	Enabled         bool     `json:"enabled"`
	MaxFileBytes    int64    `json:"maxFileBytes"`
	MaxFiles        int      `json:"maxFiles"`
	RetentionDays   int      `json:"retentionDays"`
	IncludeArgs     bool     `json:"includeArgs"`
	ArgPreviewBytes int      `json:"argPreviewBytes"`
	AllowlistFields []string `json:"allowlistFields"`
}

type RateLimitConfig struct {
	Global     RateBucket            `json:"global"`
	PerSession SessionRateLimit      `json:"perSession"`
	PerTool    map[string]RateBucket `json:"perTool"`
}

type RateBucket struct {
	QPS   float64 `json:"qps"`
	Burst float64 `json:"burst"`
}

// SessionRateLimit 的第二层桶按**客户端身份**（token 哈希）而不是
// Mcp-Session-Id 计数，见 internal/ratelimit 的包注释。
type SessionRateLimit struct {
	QPS                float64 `json:"qps"`
	Burst              float64 `json:"burst"`
	MaxConcurrentTools int     `json:"maxConcurrentTools"`
}

type SessionConfig struct {
	IdleTimeoutSeconds   int `json:"idleTimeoutSeconds"`
	MaxSessions          int `json:"maxSessions"`
	SweepIntervalSeconds int `json:"sweepIntervalSeconds"`
}

type UninstallConfig struct {
	PurgeInternalState bool `json:"purgeInternalState"`
	PurgeAuditLogs     bool `json:"purgeAuditLogs"`
	PurgeCrashDumps    bool `json:"purgeCrashDumps"`
	PurgeUserData      bool `json:"purgeUserData"`
}
