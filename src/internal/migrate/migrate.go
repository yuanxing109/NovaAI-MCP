package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/novaai/novaai-mcp/internal/auth"
)

var (
	ErrMigrationLocked = errors.New("迁移已被锁定")
)

const (
	migrateLockName = ".migrate.lock"
	migrateMarkName = ".migrated-v003"
	lockTimeout     = 5 * time.Minute
)

func RunIfNeeded(stateDir, configPath, tokenPath string) error {
	markPath := filepath.Join(stateDir, migrateMarkName)
	if _, err := os.Stat(markPath); err == nil {
		return nil
	}

	lockPath := filepath.Join(stateDir, migrateLockName)
	if info, err := os.Stat(lockPath); err == nil {
		if time.Since(info.ModTime()) < lockTimeout {
			return ErrMigrationLocked
		}
		_ = os.Remove(lockPath)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if os.IsExist(err) {
			return ErrMigrationLocked
		}
		return err
	}
	defer func() {
		f.Close()
		os.Remove(lockPath)
	}()

	if err := migrateConfig(configPath, tokenPath, stateDir); err != nil {
		return err
	}

	return os.WriteFile(markPath,
		[]byte(time.Now().Format(time.RFC3339)), 0600)
}

func migrateConfig(configPath, tokenPath, stateDir string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var meta struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}

	switch meta.SchemaVersion {
	case 3:
		return nil
	case 2, 0:
		return migrateV2ToV3(configPath, raw, tokenPath, stateDir)
	default:
		return fmt.Errorf("不支持的 schemaVersion: %d", meta.SchemaVersion)
	}
}

func migrateV2ToV3(configPath string, raw []byte, tokenPath, stateDir string) error {
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}

	backup := configPath + ".v002.bak"
	if err := os.WriteFile(backup, raw, 0600); err != nil {
		return err
	}

	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		if err := auth.EnsureToken(tokenPath); err != nil {
			return err
		}
	}
	token, _ := auth.LoadToken(tokenPath)

	sec, _ := cfg["security"].(map[string]any)
	if sec == nil {
		sec = map[string]any{}
	}
	sec["anonymous"] = false
	sec["onLinkOnly"] = true
	sec["validateHost"] = true
	sec["validateOrigin"] = true
	sec["allowCors"] = false
	sec["dropFrontendUid"] = 2000
	sec["token"] = map[string]any{
		"enabled":         true,
		"value":           token,
		"rotateOnStart":   false,
		"allowQueryParam": false,
	}
	sec["unixSocket"] = map[string]any{
		"enabled":           true,
		"path":              filepath.Join(stateDir, "mcp.sock"),
		"mode":              "0660",
		"group":             "shell",
		"sepolicyInject":    true,
		"peerUidRecordOnly": true,
	}
	sec["lan"] = map[string]any{
		"enabled":     false,
		"allowedCidr": []string{"192.168.0.0/16", "10.0.0.0/8", "172.16.0.0/12"},
	}
	cfg["security"] = sec
	cfg["schemaVersion"] = 3

	fillMissingV3Fields(cfg, stateDir)

	tmp := configPath + ".tmp"
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

func fillMissingV3Fields(cfg map[string]any, stateDir string) {
	if _, ok := cfg["profiles"]; !ok {
		cfg["profiles"] = map[string]any{
			"default": map[string]any{
				"allowTools":  []string{"*"},
				"denyTools":   []string{},
				"riskCeiling": 1,
			},
		}
	}
	if _, ok := cfg["audit"]; !ok {
		cfg["audit"] = map[string]any{
			"enabled":         true,
			"maxFileBytes":    10485760,
			"maxFiles":        20,
			"retentionDays":   30,
			"argPreviewBytes": 256,
			"redactMode":      "allowlist",
			"allowlistFields": []string{"action", "path", "package", "name"},
		}
	}
	if _, ok := cfg["session"]; !ok {
		cfg["session"] = map[string]any{
			"idleTimeoutSeconds":   1800,
			"maxSessions":          32,
			"sweepIntervalSeconds": 300,
		}
	}
	if _, ok := cfg["rateLimit"]; !ok {
		cfg["rateLimit"] = map[string]any{
			"global":     map[string]any{"qps": 50.0, "burst": 100.0},
			"perSession": map[string]any{"qps": 20.0, "burst": 40.0},
			"perTool":    map[string]any{},
		}
	}
}
