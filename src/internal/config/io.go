package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var mu sync.RWMutex
var current *Config

func Load(path string) (*Config, error) {
	mu.Lock()
	defer mu.Unlock()

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// 以默认值打底再反序列化：JSON 里缺失的字段天然保留默认值。
	//
	// 这里刻意不再用 reflect 事后"补零值"。旧实现有三个必然错误：
	//   1. map 分支对不存在的键调用 MapIndex(key).IsZero()，
	//      而 MapIndex 未命中时返回的是无效 Value，直接 panic；
	//   2. slice 分支把用户显式写的 [] 也当成"未设置"并还原成默认值，
	//      导致 denyTools: [] 会复活默认黑名单；
	//   3. bool 分支完全不合并，于是 validateHost 这类默认为 true 的
	//      开关在用户没写时静默变成 false。
	// 以默认值为基底可以一次性消除这三类问题。
	cfg := *Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	current = &cfg
	return &cfg, nil
}

func WriteAtomic(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Current() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

func Set(c *Config) {
	mu.Lock()
	defer mu.Unlock()
	current = c
}

func Validate(cfg *Config) error {
	if cfg.SchemaVersion != 3 {
		return fmt.Errorf("unsupported schemaVersion: %d", cfg.SchemaVersion)
	}
	if cfg.Network.Port < 1 || cfg.Network.Port > 65535 {
		return fmt.Errorf("invalid port: %d", cfg.Network.Port)
	}
	if cfg.Security.LAN.Enabled && !cfg.Security.Token.Enabled {
		return fmt.Errorf("LAN 开启时必须启用 token")
	}
	if cfg.Session.MaxSessions <= 0 {
		return fmt.Errorf("maxSessions 必须 > 0")
	}
	return nil
}
