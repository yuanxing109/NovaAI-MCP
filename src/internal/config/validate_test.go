package config

import (
	"strings"
	"testing"
)

// 本文件锁住 Validate 的**组合检查**与 profile 引用完整性。
//
// 这些检查的共同点：每个开关单独看都合法，只有合在一起才危险，
// 因此不可能靠"检查每个字段的取值"发现。它们曾经全部缺失。

func validBase() *Config {
	return Default()
}

func TestValidateAcceptsDefault(t *testing.T) {
	if err := Validate(validBase()); err != nil {
		t.Fatalf("默认配置必须通过校验，实际: %v", err)
	}
}

// ---- 危险组合 ----

func TestValidateRejectsAnonymousWithOpenOrigin(t *testing.T) {
	cfg := validBase()
	cfg.Security.Anonymous = true
	cfg.Security.ValidateOrigin = false
	err := Validate(cfg)
	if err == nil {
		t.Fatal("anonymous + validateOrigin=false 必须被拒绝")
	}
	if !strings.Contains(err.Error(), "anonymous") {
		t.Errorf("错误信息应点名 anonymous，实际: %v", err)
	}
}

func TestValidateRejectsAnonymousWithOpenHost(t *testing.T) {
	cfg := validBase()
	cfg.Security.Anonymous = true
	cfg.Security.ValidateHost = false
	if err := Validate(cfg); err == nil {
		t.Fatal("anonymous + validateHost=false 必须被拒绝")
	}
}

func TestValidateRejectsCorsWithOpenOrigin(t *testing.T) {
	cfg := validBase()
	cfg.Security.AllowCORS = true
	cfg.Security.ValidateOrigin = false
	if err := Validate(cfg); err == nil {
		t.Fatal("allowCors + validateOrigin=false 必须被拒绝")
	}
}

// 单独关掉 Origin 校验（不开 anonymous、不开 CORS）是允许的：
// 那是"原生客户端 + 自建前端"的常见组合，危害面显著小于上面三种。
// 这条用来防止把检查写宽成"validateOrigin=false 一律拒绝"。
func TestValidateAllowsOriginCheckOffAlone(t *testing.T) {
	cfg := validBase()
	cfg.Security.ValidateOrigin = false
	if err := Validate(cfg); err != nil {
		t.Fatalf("单独关闭 Origin 校验应当允许，实际: %v", err)
	}
}

// ---- profile 引用完整性 ----

func TestValidateRejectsDanglingFallback(t *testing.T) {
	cfg := validBase()
	cfg.SessionBinding.Fallback = "redonly" // 典型 typo
	err := Validate(cfg)
	if err == nil {
		t.Fatal("fallback 指向不存在的 profile 必须被拒绝")
	}
	if !strings.Contains(err.Error(), "redonly") {
		t.Errorf("错误信息应点名出错的 profile，实际: %v", err)
	}
}

func TestValidateRejectsDanglingTokenBinding(t *testing.T) {
	cfg := validBase()
	cfg.SessionBinding.ByTokenHash = map[string]string{
		"deadbeefdeadbeefdeadbeefdeadbeef": "no_such_profile",
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("byTokenHash 指向不存在的 profile 必须被拒绝")
	}
}

func TestValidateAcceptsValidTokenBinding(t *testing.T) {
	cfg := validBase()
	cfg.SessionBinding.ByTokenHash = map[string]string{
		"deadbeefdeadbeefdeadbeefdeadbeef": "readonly",
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("指向已存在 profile 的绑定应当通过，实际: %v", err)
	}
}

func TestValidateRejectsMissingDefaultProfile(t *testing.T) {
	cfg := validBase()
	delete(cfg.Profiles, "default")
	if err := Validate(cfg); err == nil {
		t.Fatal("缺少 default profile 必须被拒绝（它是回退目标）")
	}
}

func TestValidateRejectsEmptyAllowTools(t *testing.T) {
	cfg := validBase()
	cfg.Profiles["broken"] = Profile{AllowTools: []string{}, RiskCeiling: 3}
	if err := Validate(cfg); err == nil {
		t.Fatal("allowTools 为空的 profile 必须被拒绝")
	}
}

// 空字符串 fallback 表示"未设置"，由 Store 兜到 default，不算悬空。
func TestValidateAllowsEmptyFallback(t *testing.T) {
	cfg := validBase()
	cfg.SessionBinding.Fallback = ""
	if err := Validate(cfg); err != nil {
		t.Fatalf("空 fallback 应当允许，实际: %v", err)
	}
}
