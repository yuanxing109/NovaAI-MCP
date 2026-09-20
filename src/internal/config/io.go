package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Load 读取并校验配置。
//
// 配置不支持热重载：进程启动时加载一次，改动需要重启 daemon。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// 以默认值打底再反序列化：JSON 里缺失的字段天然保留默认值。
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
// 只做值域检查：组合校验（anonymous/token/lan/CORS）随字段一起删除了，
// 因为那些开关已经不存在。
func Validate(cfg *Config) error {
	if cfg.StateDir == "" {
		return fmt.Errorf("stateDir 不能为空")
	}
	if err := validateListen(cfg.Listen); err != nil {
		return err
	}
	if cfg.Profile != DefaultProfileName {
		return fmt.Errorf("profile 只能是 %q，实际 %q", DefaultProfileName, cfg.Profile)
	}
	if cfg.ShellTimeoutSeconds <= 0 {
		return fmt.Errorf("shellTimeoutSeconds 必须 > 0")
	}
	if cfg.ResultPreviewBytes < 0 {
		return fmt.Errorf("resultPreviewBytes 不能为负")
	}
	if err := validateUpstreams(cfg.Upstreams); err != nil {
		return err
	}
	return nil
}

// upstreamNameRe 是上游名的白名单：字母数字与 `-` `_` `.`。
//
// 收紧到白名单而不是黑名单，是因为这个名字会进入工具名
// （`{name}__{tool}`），而工具名要能被客户端安全地当标识符使用。
var upstreamNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// toolNameRe 是上游 denyTools 条目的白名单。
//
// 比 upstreamNameRe 多一个大写与下划线组合的空间：上游工具名可能形如
// `read_file` 或 `ReadFile`。
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// validateUpstreams 校验上游配置。
//
// 这一层必须在启动时把关：上游名是工具名的前缀，一个写错的名字会让
// 一整批工具在 tools/list 里出现却永远路由不到；而 name 里含 `__` 会直接
// 破坏"按第一个 `__` 切分"的路由规则（`docs/upstream.md` 的路由契约）。
func validateUpstreams(list []UpstreamConfig) error {
	seen := map[string]bool{}
	for i, u := range list {
		where := fmt.Sprintf("upstreams[%d]", i)
		if u.Name == "" {
			return fmt.Errorf("%s.name 不能为空", where)
		}
		where = fmt.Sprintf("upstreams[%d] (%s)", i, u.Name)
		if !upstreamNameRe.MatchString(u.Name) {
			return fmt.Errorf("%s.name 只能含字母、数字、'_'、'-'、'.'", where)
		}
		// 双下划线是命名空间分隔符。允许它出现在前缀里会让
		// `a__b__tool` 无法判断是哪个上游的工具。
		if strings.Contains(u.Name, "__") {
			return fmt.Errorf("%s.name 不能包含 %q（它是工具名的命名空间分隔符）",
				where, "__")
		}
		if seen[u.Name] {
			return fmt.Errorf("%s.name 重复", where)
		}
		seen[u.Name] = true

		switch u.Type {
		case UpstreamTypeHTTP:
			if u.URL == "" {
				return fmt.Errorf("%s 类型为 http 时 url 必填", where)
			}
			if !strings.HasPrefix(u.URL, "http://") && !strings.HasPrefix(u.URL, "https://") {
				return fmt.Errorf("%s.url 必须以 http:// 或 https:// 开头", where)
			}
		case UpstreamTypeStdio:
			if u.Command == "" {
				return fmt.Errorf("%s 类型为 stdio 时 command 必填", where)
			}
		default:
			return fmt.Errorf("%s.type 只能是 %q 或 %q，实际 %q",
				where, UpstreamTypeHTTP, UpstreamTypeStdio, u.Type)
		}

		if u.RiskCeiling < 0 || u.RiskCeiling > DefaultUpstreamRiskCeiling {
			return fmt.Errorf("%s.riskCeiling 必须在 0..%d 之间（0 表示继承默认）",
				where, DefaultUpstreamRiskCeiling)
		}
		for _, d := range u.DenyTools {
			if !toolNameRe.MatchString(d) {
				return fmt.Errorf("%s.denyTools 含非法工具名 %q", where, d)
			}
		}
		if err := validateLaunch(where, u); err != nil {
			return err
		}
	}
	return nil
}

// validateLaunch 校验启动方式。缺省（nil）合法，等同 manual。
func validateLaunch(where string, u UpstreamConfig) error {
	switch u.LaunchType() {
	case LaunchManual:
		return nil
	case LaunchIntent:
		if u.Launch.Package == "" {
			return fmt.Errorf("%s.launch 类型为 intent 时 package 必填", where)
		}
		if u.Launch.Action == "" && u.Launch.Activity == "" {
			return fmt.Errorf("%s.launch 类型为 intent 时 action 与 activity 至少填一个", where)
		}
	case LaunchCommand:
		if u.Launch.Command == "" {
			return fmt.Errorf("%s.launch 类型为 command 时 command 必填", where)
		}
	default:
		return fmt.Errorf("%s.launch.type 只能是 %q / %q / %q，实际 %q",
			where, LaunchIntent, LaunchCommand, LaunchManual, u.Launch.Type)
	}
	return nil
}

// validateListen 校验 "host:port" 形式的监听地址。
func validateListen(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen 不是合法的 host:port: %q", addr)
	}
	if host != "" && net.ParseIP(host) == nil && !strings.EqualFold(host, "localhost") {
		return fmt.Errorf("listen 的主机部分必须是 IP 字面量或 localhost: %q", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("listen 端口非法: %q", addr)
	}
	return nil
}
