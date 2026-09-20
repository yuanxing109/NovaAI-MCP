package config

import (
	"strings"
	"testing"
)

// 本文件锁住 Validate 的值域检查。
//
// 组合校验（anonymous + !validateOrigin 等）随字段一起删除了：
// 那三个开关已不存在，没有可校验的组合。

func validBase() *Config {
	return Default()
}

func TestValidateAcceptsDefault(t *testing.T) {
	if err := Validate(validBase()); err != nil {
		t.Fatalf("默认配置必须通过校验，实际: %v", err)
	}
}

func TestValidateRejectsEmptyStateDir(t *testing.T) {
	cfg := validBase()
	cfg.StateDir = ""
	if err := Validate(cfg); err == nil {
		t.Fatal("stateDir 为空必须被拒绝")
	}
}

func TestValidateRejectsBadListen(t *testing.T) {
	for _, addr := range []string{"", "5322", "0.0.0.0:0", "0.0.0.0:99999", "example.com:5322"} {
		cfg := validBase()
		cfg.Listen = addr
		if err := Validate(cfg); err == nil {
			t.Errorf("listen=%q 必须被拒绝", addr)
		}
	}
}

func TestValidateAcceptsLoopbackListen(t *testing.T) {
	cfg := validBase()
	cfg.Listen = "127.0.0.1:5322"
	if err := Validate(cfg); err != nil {
		t.Fatalf("本地模式地址应当通过，实际: %v", err)
	}
}

// profile 只能是 default：配置里写不出别的档位。
func TestValidateRejectsNonDefaultProfile(t *testing.T) {
	cfg := validBase()
	cfg.Profile = "readonly"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("非 default 的 profile 必须被拒绝")
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("错误信息应点名 default，实际: %v", err)
	}
}

func TestValidateRejectsBadShellTimeout(t *testing.T) {
	cfg := validBase()
	cfg.ShellTimeoutSeconds = 0
	if err := Validate(cfg); err == nil {
		t.Fatal("shellTimeoutSeconds=0 必须被拒绝")
	}
}

// default 必须放行 shell：装完即用、含 shell 是明确诉求。
func TestDefaultProfileAllowsShell(t *testing.T) {
	cfg := validBase()
	p, ok := DefaultProfiles()[DefaultProfileName]
	if !ok {
		t.Fatalf("默认档位 %s 不存在", DefaultProfileName)
	}
	for _, d := range p.DenyTools {
		if d == "novaai_shell" {
			t.Fatalf("default.denyTools 不应拒绝 shell，实际: %v", p.DenyTools)
		}
	}
	if cfg.Profile != DefaultProfileName {
		t.Fatalf("配置档位 = %q，期望 %q", cfg.Profile, DefaultProfileName)
	}
}
