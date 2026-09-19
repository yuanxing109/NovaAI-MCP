package config

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
)

func Default() *Config {
	stateDir := "/data/adb/novaai-mcp"
	token := generateToken()

	return &Config{
		SchemaVersion: 3,
		Network: Network{
			Port:           5322,
			ListenLoopback: true,
			ListenLAN:      false,
			AllowedOrigins: []string{},
		},
		Paths: Paths{
			StateDir:      stateDir,
			WorkDir:       "/storage/emulated/0/novaaiAI",
			WorkspaceRoot: filepath.Join(stateDir, "workspace"),
			DownloadsDir:  filepath.Join(stateDir, "downloads"),
			UploadsDir:    filepath.Join(stateDir, "uploads"),
			ArtifactsDir:  filepath.Join(stateDir, "artifacts"),
			TempDir:       filepath.Join(stateDir, "tmp"),
			AuditDir:      filepath.Join(stateDir, "audit"),
			CrashDir:      filepath.Join(stateDir, "crash"),
		},
		Limits: Limits{
			MaxConnections:     128,
			MaxRequestBytes:    67108864,
			TotalTasks:         16,
			HeavyTasks:         2,
			ShellTimeoutSec:    60,
			TransferChunkBytes: 1048576,
			TransferMaxBytes:   1073741824,
			ResultPreviewBytes: 262144,
			ArtifactTTLSec:     604800,
			ShutdownGraceSec:   30,
			UploadIdleTTLSec:   1800,
			DownloadRetries:    3,
		},
		Security: Security{
			Anonymous:      false,
			ValidateHost:   true,
			ValidateOrigin: true,
			AllowCORS:      false,
			Token: TokenConf{
				Enabled:         true,
				Value:           token,
				RotateOnStart:   false,
				AllowQueryParam: false,
			},
			UnixSocket: UnixSocket{
				Enabled:        true,
				Path:           filepath.Join(stateDir, "mcp.sock"),
				Mode:           "0660",
				Group:          "shell",
				SepolicyInject: true,
			},
			LAN: LANConf{
				Enabled:     false,
				AllowedCIDR: []string{"192.168.0.0/16", "10.0.0.0/8", "172.16.0.0/12"},
			},
		},
		Profiles: DefaultProfiles(),
		SessionBinding: SessionBinding{
			ByTokenHash: map[string]string{},
			Fallback:    "default",
		},
		Audit: AuditConfig{
			Enabled:         true,
			MaxFileBytes:    10485760,
			MaxFiles:        20,
			RetentionDays:   30,
			IncludeArgs:     true,
			ArgPreviewBytes: 256,
			RedactMode:      "allowlist",
			AllowlistFields: []string{"action", "path", "package", "name",
				"query", "url", "tool", "pattern", "cmd", "command"},
			SeparateArgsFile: true,
		},
		RateLimit: RateLimitConfig{
			Global: RateBucket{QPS: 50, Burst: 100},
			PerSession: SessionRateLimit{
				QPS: 20, Burst: 40,
				MaxConcurrentTools: 5,
				TotalUploadBytes:   5368709120,
				TotalDownloadBytes: 5368709120,
			},
			PerTool: map[string]RateBucket{
				"novaai_log":     {QPS: 2, Burst: 4},
				"novaai_network": {QPS: 5, Burst: 10},
				"novaai_shell":   {QPS: 10, Burst: 20},
			},
		},
		Session: SessionConfig{
			IdleTimeoutSeconds:   1800,
			MaxSessions:          32,
			SweepIntervalSeconds: 300,
		},
		Skill: SkillConfig{
			LearnFromRiskOps: false,
			MaxLearnedSkills: 200,
		},
		Uninstall: UninstallConfig{
			PurgeInternalState: false,
			PurgeAuditLogs:     false,
			PurgeCrashDumps:    false,
			PurgeUserData:      false,
		},
		Capabilities: map[string]bool{},
	}
}

func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
