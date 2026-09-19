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
			LegacySSE:      true,
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
			Anonymous:       false,
			OnLinkOnly:      true,
			ValidateHost:    true,
			ValidateOrigin:  true,
			AllowCORS:       false,
			DropFrontendUID: 2000,
			Token: TokenConf{
				Enabled:         true,
				Value:           token,
				RotateOnStart:   false,
				AllowQueryParam: false,
			},
			UnixSocket: UnixSocket{
				Enabled:           true,
				Path:              filepath.Join(stateDir, "mcp.sock"),
				Mode:              "0660",
				Group:             "shell",
				SepolicyInject:    true,
				PeerUIDRecordOnly: true,
			},
			LAN: LANConf{
				Enabled:     false,
				AllowedCIDR: []string{"192.168.0.0/16", "10.0.0.0/8", "172.16.0.0/12"},
			},
		},
		Profiles: map[string]Profile{
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
		},
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
