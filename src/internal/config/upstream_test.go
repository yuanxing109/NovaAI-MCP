package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ---- 文档契约校验用的小工具 ----

func itoa(n int) string { return strconv.Itoa(n) }

// jsonTagSet 收集一个结构体所有导出的 json tag 名。
func jsonTagSet(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// checkKeySet 比对文档示例里的键与结构体 tag。
//
// exact=true 时要求完全相等（既不缺也不多）；false 时只要求"不多"
// —— 用于有多种形状的可选子对象（launch 的 intent / command 两种写法）。
func checkKeySet(t *testing.T, path string, got map[string]json.RawMessage, want map[string]bool, exact bool) {
	t.Helper()
	var missing, extra []string
	for k := range want {
		if _, ok := got[k]; !ok && exact {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("%s: 示例缺少结构体里的键 %v", path, missing)
	}
	if len(extra) > 0 {
		t.Errorf("%s: 示例有结构体没有的键 %v", path, extra)
	}
}

// 本文件锁住两件事：
//   1. 上游配置的启动校验（name 唯一且不含 __、type、url/command、launch 完整性…）
//   2. docs/upstream.md 的示例与 UpstreamConfig / LaunchConfig 的 json tag 一致
//
// 第 2 条沿用 example_test.go 对 config.md 的做法：文档里的示例是**契约**，
// 不是散文。没有这道闸门，"文档教用户配一个不存在的字段"这类漂移
// （config.md 的 crashDir / capabilities 就发生过）只能靠人读出来。

func validHTTPUpstream() UpstreamConfig {
	return UpstreamConfig{
		Name: "a", Type: UpstreamTypeHTTP, URL: "http://127.0.0.1:1/mcp", Enabled: true,
	}
}

func TestValidateUpstreams(t *testing.T) {
	// 每个用例的 list 都写成单行，避免多行复合字面量的括号配平出错。
	// 覆盖的是"启动时能不能拦住写坏的配置"，详见 docs/upstream.md。

	http := func(mut func(*UpstreamConfig)) []UpstreamConfig {
		u := validHTTPUpstream()
		if mut != nil {
			mut(&u)
		}
		return []UpstreamConfig{u}
	}

	cases := []struct {
		name    string
		list    []UpstreamConfig
		wantErr string // 空表示期望通过
	}{
		{"空列表", nil, ""},
		{"合法 http", []UpstreamConfig{validHTTPUpstream()}, ""},
		{"合法 stdio", []UpstreamConfig{{Name: "b", Type: UpstreamTypeStdio, Command: "/bin/true", Enabled: true}}, ""},

		{"name 为空", []UpstreamConfig{{Type: UpstreamTypeHTTP, URL: "http://x/mcp"}}, "name 不能为空"},
		{"name 含非法字符", []UpstreamConfig{{Name: "a b", Type: UpstreamTypeHTTP, URL: "http://x/mcp"}}, "只能含字母"},
		{"name 含双下划线", []UpstreamConfig{{Name: "a__b", Type: UpstreamTypeHTTP, URL: "http://x/mcp"}}, "命名空间分隔符"},
		{"name 重复", []UpstreamConfig{validHTTPUpstream(), validHTTPUpstream()}, "重复"},

		{"type 缺失", []UpstreamConfig{{Name: "a"}}, "type 只能是"},
		{"type 未知", []UpstreamConfig{{Name: "a", Type: "grpc"}}, "type 只能是"},
		{"http 缺 url", []UpstreamConfig{{Name: "a", Type: UpstreamTypeHTTP}}, "url 必填"},
		{"http url 协议不对", []UpstreamConfig{{Name: "a", Type: UpstreamTypeHTTP, URL: "ftp://x"}}, "http:// 或 https://"},
		{"stdio 缺 command", []UpstreamConfig{{Name: "a", Type: UpstreamTypeStdio}}, "command 必填"},

		{"riskCeiling 过大", http(func(u *UpstreamConfig) { u.RiskCeiling = 4 }), "riskCeiling 必须在"},
		{"riskCeiling 为负", http(func(u *UpstreamConfig) { u.RiskCeiling = -1 }), "riskCeiling 必须在"},
		{"maxConcurrent 过小", http(func(u *UpstreamConfig) { u.MaxConcurrent = -2 }), "maxConcurrent 不能小于 -1"},
		{"maxConcurrent 不限", http(func(u *UpstreamConfig) { u.MaxConcurrent = -1 }), ""},
		{"maxConcurrent 合法", http(func(u *UpstreamConfig) { u.MaxConcurrent = 2 }), ""},
		{"denyTools 非法", http(func(u *UpstreamConfig) { u.DenyTools = []string{"ok", "bad name"} }), "非法工具名"},

		{"launch intent 缺 package", http(func(u *UpstreamConfig) {
			u.Launch = &LaunchConfig{Type: LaunchIntent, Action: "X"}
		}), "package 必填"},
		{"launch intent 缺 action/activity", http(func(u *UpstreamConfig) {
			u.Launch = &LaunchConfig{Type: LaunchIntent, Package: "p"}
		}), "action 与 activity 至少填一个"},
		{"launch command 缺 command", http(func(u *UpstreamConfig) {
			u.Launch = &LaunchConfig{Type: LaunchCommand}
		}), "command 必填"},
		{"launch type 未知", http(func(u *UpstreamConfig) {
			u.Launch = &LaunchConfig{Type: "systemd"}
		}), "launch.type 只能是"},

		{"launch intent 合法（activity）", http(func(u *UpstreamConfig) {
			u.Launch = &LaunchConfig{Type: LaunchIntent, Package: "p", Activity: "Main"}
		}), ""},
		{"launch command 合法", http(func(u *UpstreamConfig) {
			u.Launch = &LaunchConfig{Type: LaunchCommand, Command: "/bin/true"}
		}), ""},
		{"launch manual 合法", http(func(u *UpstreamConfig) {
			u.Launch = &LaunchConfig{Type: LaunchManual}
		}), ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Default()
			cfg.Upstreams = c.list
			err := Validate(cfg)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望报错（含 %q），实际通过", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("错误信息 %q 不含 %q", err.Error(), c.wantErr)
			}
		})
	}
}

// 错误信息里应带上游名字，否则多上游的配置里定位不到是哪一个写错了。
func TestValidateUpstreamsErrorMentionsName(t *testing.T) {
	cfg := Default()
	cfg.Upstreams = []UpstreamConfig{
		validHTTPUpstream(),
		{Name: "broken_one", Type: UpstreamTypeHTTP}, // 缺 url
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("应报错")
	}
	if !strings.Contains(err.Error(), "broken_one") {
		t.Fatalf("错误信息应指向上游名，实际: %v", err)
	}
}

// 缺省的 riskCeiling 应按"继承默认 3"生效。
func TestEffectiveRiskCeiling(t *testing.T) {
	u := UpstreamConfig{}
	if got := u.EffectiveRiskCeiling(); got != DefaultUpstreamRiskCeiling {
		t.Errorf("缺省 = %d，期望 %d", got, DefaultUpstreamRiskCeiling)
	}
	u.RiskCeiling = 1
	if got := u.EffectiveRiskCeiling(); got != 1 {
		t.Errorf("显式 1 = %d", got)
	}
	u.RiskCeiling = 3
	if got := u.EffectiveRiskCeiling(); got != 3 {
		t.Errorf("显式 3 = %d", got)
	}
}

func TestLaunchTypeDefaultsToManual(t *testing.T) {
	if got := (&UpstreamConfig{}).LaunchType(); got != LaunchManual {
		t.Errorf("缺省 = %q，期望 %q", got, LaunchManual)
	}
	u := UpstreamConfig{Launch: &LaunchConfig{Type: LaunchIntent}}
	if got := u.LaunchType(); got != LaunchIntent {
		t.Errorf("= %q", got)
	}
}

// maxConcurrent 的三档语义：0=缺省（默认 4）、-1=不限、>0=该值。
func TestEffectiveMaxConcurrent(t *testing.T) {
	u := UpstreamConfig{}
	if got := u.EffectiveMaxConcurrent(); got != DefaultUpstreamMaxConcurrent {
		t.Errorf("缺省 = %d，期望 %d", got, DefaultUpstreamMaxConcurrent)
	}
	u.MaxConcurrent = -1
	if got := u.EffectiveMaxConcurrent(); got != -1 {
		t.Errorf("-1 = %d，期望 -1（不限）", got)
	}
	u.MaxConcurrent = 7
	if got := u.EffectiveMaxConcurrent(); got != 7 {
		t.Errorf("显式 7 = %d", got)
	}
}

// 默认配置里的 upstreams 必须是 `[]` 而不是 null —— 文档示例按 `[]` 写，
// 且 WebUI 会直接在这上面做数组运算。
func TestDefaultUpstreamsMarshalsAsEmptyArray(t *testing.T) {
	raw, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"upstreams":[]`) {
		t.Fatalf("默认配置应序列化出 \"upstreams\":[]，实际: %s", raw)
	}
}

// ---- docs/upstream.md 的契约校验 ----

// TestUpstreamDocExampleMatchesStruct 断言 docs/upstream.md 的第一个 ```json
// 块与 UpstreamConfig / LaunchConfig 的 json tag 一致。
//
// 判据分两层：
//   - 顶层对象与每个上游条目的键集合必须**完全相等**（示例是"完整可复制"的，
//     不允许缺键或多键）；
//   - 嵌套的 launch 对象只要求"没有不存在的键"（它有两种形状，
//     强制两种都列全反而会让人以为字段是必填的）。
func TestUpstreamDocExampleMatchesStruct(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "upstream.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 upstream.md 失败: %v", err)
	}
	block, err := extractJSONBlock(string(raw))
	if err != nil {
		t.Fatalf("upstream.md: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(block, &doc); err != nil {
		t.Fatalf("upstream.md 的示例不是合法 JSON 对象: %v", err)
	}

	// 顶层：只应有 upstreams 一个键。
	var top []string
	for k := range doc {
		top = append(top, k)
	}
	if len(top) != 1 || top[0] != "upstreams" {
		t.Fatalf("示例的顶层键 = %v，期望只有 upstreams", top)
	}

	var list []map[string]json.RawMessage
	if err := json.Unmarshal(doc["upstreams"], &list); err != nil {
		t.Fatalf("upstreams 不是数组: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("示例里的 upstreams 不能是空数组 —— 那样什么字段都校验不到")
	}

	wantKeys := jsonTagSet(reflect.TypeOf(UpstreamConfig{}))
	launchKeys := jsonTagSet(reflect.TypeOf(LaunchConfig{}))

	for i, item := range list {
		checkKeySet(t, "upstreams["+itoa(i)+"]", item, wantKeys, true)

		rawLaunch, ok := item["launch"]
		if !ok {
			continue
		}
		var launch map[string]json.RawMessage
		if err := json.Unmarshal(rawLaunch, &launch); err != nil {
			t.Fatalf("upstreams[%d].launch 不是对象: %v", i, err)
		}
		checkKeySet(t, "upstreams["+itoa(i)+"].launch", launch, launchKeys, false)
	}
}

// TestUpstreamDocExampleIsLoadable 断言示例能真的通过 Validate。
//
// 这一步比字段比对更强：它同时验证了取值（type 合法、url 前缀、launch 完整）。
// 文档里贴一段"复制进去就启动失败"的配置，是最难被发现的一类错误。
func TestUpstreamDocExampleIsLoadable(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "upstream.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, err := extractJSONBlock(string(raw))
	if err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	if err := json.Unmarshal(block, cfg); err != nil {
		t.Fatalf("示例无法反序列化进 Config: %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("示例未通过 Validate: %v", err)
	}
	if len(cfg.Upstreams) == 0 {
		t.Fatal("示例没有上游")
	}
}
