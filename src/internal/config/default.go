package config

import (
	"crypto/rand"
	"encoding/hex"
	"path"
)

func Default() *Config {
	stateDir := "/data/adb/novaai-mcp"
	token := generateToken()

	// 注意用 path.Join 而不是 filepath.Join。
	//
	// 这些是 **Android 的 POSIX 路径**，不管进程跑在哪个平台上都必须保持
	// 正斜杠。filepath.Join 在 Windows 上会把分隔符换成 "\"，于是同一份
	// 配置在开发机上生成的是 `\data\adb\novaai-mcp\workspace`，在设备上
	// 才是 `/data/adb/novaai-mcp/workspace`。前者会被 pathguard 当成
	// 相对路径（Normalize 对不以 "/" 开头的输入原样返回），保护判定静默失效。
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
			WorkspaceRoot: path.Join(stateDir, "workspace"),
			AuditDir:      path.Join(stateDir, "audit"),
		},
		Limits: Limits{
			MaxRequestBytes: 67108864,
			// shell/script 未显式给 timeoutMs 时的默认超时。
			ShellTimeoutSec: 60,
			// 单个工具结果的字节上限。超过就截断 Content 并丢弃
			// structuredContent —— 否则一个 dumpsys 就能撑爆 JSON-RPC 帧。
			ResultPreviewBytes: 1048576,
			ShutdownGraceSec:   30,
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
				Path:           path.Join(stateDir, "mcp.sock"),
				Mode:           "0660",
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
			AllowlistFields: []string{"action", "path", "package", "name",
				"query", "url", "tool", "pattern", "cmd", "command"},
		},
		RateLimit: RateLimitConfig{
			Global: RateBucket{QPS: 50, Burst: 100},
			PerSession: SessionRateLimit{
				QPS: 20, Burst: 40,
				MaxConcurrentTools: 5,
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
		Uninstall: UninstallConfig{
			PurgeInternalState: false,
			PurgeAuditLogs:     false,
			PurgeCrashDumps:    false,
			PurgeUserData:      false,
		},
	}
}

func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
