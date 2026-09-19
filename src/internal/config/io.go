package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Load 读取并校验配置。
//
// 配置不支持热重载：进程启动时加载一次，改动需要重启 daemon。
// 这里刻意不保留"当前配置"的全局副本 —— 曾经有一份 current/Set/Current
// 三元组，但没有任何调用方，只会让人误以为改配置能即时生效。
func Load(path string) (*Config, error) {
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

// Validate 是配置进入进程的唯一闸门。
//
// 这里刻意承载两类检查：
//   - 值域检查（端口、卷数）；
//   - **组合检查**：单个开关都合法、合在一起才危险的配置。
//
// 第二类才是重点。security 的三个开关各自看都没问题，但
// anonymous + validateOrigin=false 意味着一份不需要 token 的 root 工具面，
// 且任何网页都能盲打它（服务端不检查 Content-Type，所以 text/plain 的
// "简单请求"不触发 preflight，请求会被真正执行，攻击者只是读不到响应）。
// allowCors + validateOrigin=false 更糟：CORS 反射任意 Origin，preflight
// 通过后网页就能带上 token 全权访问。这类配置不该等到运行时才发现，
// 它必须在启动时就是非法的。
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

	// ---- 危险组合 ----
	if cfg.Security.Anonymous && !cfg.Security.ValidateHost {
		return fmt.Errorf("anonymous 与 validateHost=false 不能同时开启：" +
			"DNS rebinding 后浏览器与该端口同源，将失去唯一的来源校验")
	}
	if cfg.Security.Anonymous && !cfg.Security.ValidateOrigin {
		return fmt.Errorf("anonymous 与 validateOrigin=false 不能同时开启：" +
			"任意网页可跨源盲打 root 工具")
	}
	if cfg.Security.AllowCORS && !cfg.Security.ValidateOrigin {
		return fmt.Errorf("allowCors 与 validateOrigin=false 不能同时开启：" +
			"CORS 会反射任意 Origin，网页可带 token 全权访问")
	}

	// ---- profile 引用完整性 ----
	//
	// 悬空的 profile 名必须在这里拒绝。运行时 profile.Store.Get 会回退到
	// default（见 internal/profile），但那是兜底不是许可：绑定写错名字是
	// 配置错误，应当让进程起不来，而不是静默换一个权限档位。
	if len(cfg.Profiles) == 0 {
		return fmt.Errorf("profiles 不能为空：至少要有一个 default")
	}
	if _, ok := cfg.Profiles["default"]; !ok {
		return fmt.Errorf("profiles 缺少 default：它是不存在的 profile 名的回退目标")
	}
	if fb := cfg.SessionBinding.Fallback; fb != "" {
		if _, ok := cfg.Profiles[fb]; !ok {
			return fmt.Errorf("sessionBinding.fallback 指向不存在的 profile: %q", fb)
		}
	}
	for hash, name := range cfg.SessionBinding.ByTokenHash {
		if _, ok := cfg.Profiles[name]; !ok {
			return fmt.Errorf("sessionBinding.byTokenHash[%s] 指向不存在的 profile: %q",
				shortHash(hash), name)
		}
	}

	// ---- 默认 profile 的形状 ----
	//
	// AllowTools 为空等于"什么都不允许"，但那是 profile 作者写漏了，
	// 不是一个可用的档位；让它启动失败比让它静默拒绝一切要好定位。
	for name, p := range cfg.Profiles {
		if len(p.AllowTools) == 0 {
			return fmt.Errorf("profile %q 的 allowTools 为空：无工具可用，请显式写出允许列表", name)
		}
	}
	return nil
}

// shortHash 把 token 哈希截短用于错误信息，避免把完整哈希写进日志。
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12] + "…"
}
