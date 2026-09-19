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
	Skill          SkillConfig        `json:"skill"`
	Uninstall      UninstallConfig    `json:"uninstall"`
	Capabilities   map[string]bool    `json:"capabilities"`
}

type Network struct {
	Port           int      `json:"port"`
	ListenLoopback bool     `json:"listenLoopback"`
	ListenLAN      bool     `json:"listenLan"`
	AllowedOrigins []string `json:"allowedOrigins"`
}

type Paths struct {
	StateDir      string `json:"stateDir"`
	WorkDir       string `json:"workDir"`
	WorkspaceRoot string `json:"workspaceRoot"`
	DownloadsDir  string `json:"downloadsDir"`
	UploadsDir    string `json:"uploadsDir"`
	ArtifactsDir  string `json:"artifactsDir"`
	TempDir       string `json:"tempDir"`
	AuditDir      string `json:"auditDir"`
	CrashDir      string `json:"crashDir"`
}

type Limits struct {
	MaxConnections     int   `json:"maxConnections"`
	MaxRequestBytes    int64 `json:"maxRequestBytes"`
	TotalTasks         int   `json:"totalTasks"`
	HeavyTasks         int   `json:"heavyTasks"`
	ShellTimeoutSec    int   `json:"shellTimeoutSeconds"`
	TransferChunkBytes int64 `json:"transferChunkBytes"`
	TransferMaxBytes   int64 `json:"transferMaxBytes"`
	ResultPreviewBytes int64 `json:"resultPreviewBytes"`
	ArtifactTTLSec     int   `json:"artifactTtlSeconds"`
	ShutdownGraceSec   int   `json:"shutdownGraceSeconds"`
	UploadIdleTTLSec   int   `json:"uploadIdleTtlSeconds"`
	DownloadRetries    int   `json:"downloadRetryAttempts"`
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
	Group          string `json:"group"`
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

type AuditConfig struct {
	Enabled          bool     `json:"enabled"`
	MaxFileBytes     int64    `json:"maxFileBytes"`
	MaxFiles         int      `json:"maxFiles"`
	RetentionDays    int      `json:"retentionDays"`
	IncludeArgs      bool     `json:"includeArgs"`
	ArgPreviewBytes  int      `json:"argPreviewBytes"`
	RedactMode       string   `json:"redactMode"`
	AllowlistFields  []string `json:"allowlistFields"`
	SeparateArgsFile bool     `json:"separateArgsFile"`
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

type SessionRateLimit struct {
	QPS                float64 `json:"qps"`
	Burst              float64 `json:"burst"`
	MaxConcurrentTools int     `json:"maxConcurrentTools"`
	TotalUploadBytes   int64   `json:"totalUploadBytes"`
	TotalDownloadBytes int64   `json:"totalDownloadBytes"`
}

type SessionConfig struct {
	IdleTimeoutSeconds   int `json:"idleTimeoutSeconds"`
	MaxSessions          int `json:"maxSessions"`
	SweepIntervalSeconds int `json:"sweepIntervalSeconds"`
}

type SkillConfig struct {
	LearnFromRiskOps bool `json:"learnFromRiskOps"`
	MaxLearnedSkills int  `json:"maxLearnedSkills"`
}

type UninstallConfig struct {
	PurgeInternalState bool `json:"purgeInternalState"`
	PurgeAuditLogs     bool `json:"purgeAuditLogs"`
	PurgeCrashDumps    bool `json:"purgeCrashDumps"`
	PurgeUserData      bool `json:"purgeUserData"`
}
