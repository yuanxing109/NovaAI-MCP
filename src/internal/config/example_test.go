package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 本文件锁住"文档示例与实际结构体同步"这条边界。
//
// 为什么需要它：`paths.crashDir` 与顶层 `capabilities` 都曾长期存在于
// `config.example.json` 与 `config.md`，但 Go 侧零访问 —— 文档在教用户配置
// 两个不生效的旋钮。两次都是靠人工读代码才发现的，而"按名字全仓计数"的
// 机械检查会假阴性（`crashDir` 被别处的 `crash` 掩盖，`capabilities` 被探针
// 脚本里 MCP 协议的 `capabilities` 掩盖）。
//
// 这里改成结构化的边界断言：示例 JSON 的键集合必须与结构体的 json tag
// **完全一致**。任何一侧增删字段都会立刻失败，而不是等下一次人工审计。

// shape 描述一个 JSON 值的期望形状。
//   - keys != nil：对象，键集合必须完全匹配
//   - mapValue != nil：以任意字符串为键的映射，每个值的形状由 mapValue 描述
//   - 两者都为 nil：叶子（标量或数组）
type shape struct {
	keys     map[string]*shape
	mapValue *shape
}

func shapeOf(t reflect.Type) *shape {
	switch t.Kind() {
	case reflect.Ptr:
		return shapeOf(t.Elem())
	case reflect.Map:
		// 只支持 map[string]T（本项目的配置里没有别的键类型）
		if t.Key().Kind() != reflect.String {
			return &shape{}
		}
		return &shape{mapValue: shapeOf(t.Elem())}
	case reflect.Slice, reflect.Array:
		// 数组按叶子处理：元素是标量，示例里不做逐元素校验
		return &shape{}
	case reflect.Struct:
		s := &shape{keys: map[string]*shape{}}
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
			if name == "" {
				continue
			}
			s.keys[name] = shapeOf(f.Type)
		}
		return s
	default:
		return &shape{}
	}
}

// checkShape 递归比对，把差异累积进 problems。
func checkShape(path string, want *shape, got any, problems *[]string) {
	switch {
	case want.mapValue != nil:
		m, ok := got.(map[string]any)
		if !ok {
			*problems = append(*problems, path+": 期望对象（映射）")
			return
		}
		for k, v := range m {
			checkShape(path+"."+k, want.mapValue, v, problems)
		}
	case want.keys != nil:
		m, ok := got.(map[string]any)
		if !ok {
			*problems = append(*problems, path+": 期望对象")
			return
		}
		for k := range m {
			if strings.HasPrefix(k, "_comment") {
				continue
			}
			if _, ok := want.keys[k]; !ok {
				*problems = append(*problems,
					path+"."+k+": 示例里有这个键，但结构体没有对应 json tag")
			}
		}
		for k, sub := range want.keys {
			v, ok := m[k]
			if !ok {
				*problems = append(*problems,
					path+"."+k+": 结构体有 json tag，但示例里缺少这个键")
				continue
			}
			checkShape(path+"."+k, sub, v, problems)
		}
	}
}

// TestExampleConfigMatchesStruct 断言 docs/config.example.json 与 Config
// 的 json tag 完全一致（`_comment*` 说明键除外）。
func TestExampleConfigMatchesStruct(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "config.example.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取示例配置失败: %v", err)
	}

	var example map[string]any
	if err := json.Unmarshal(raw, &example); err != nil {
		t.Fatalf("示例配置不是合法 JSON: %v", err)
	}

	var problems []string
	checkShape("config", shapeOf(reflect.TypeOf(Config{})), example, &problems)

	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("%s", p)
	}
	if len(problems) > 0 {
		t.Logf("示例文件: %s", path)
		t.Log("增删配置字段时请同步 docs/config.example.json 与 docs/config.md")
	}
}

// TestExampleConfigHasNoReservedKeys 是一个更窄的回归锁：
// 曾经被注释为"保留字段"却没有消费者的键，不允许再回来。
//
// 保留字段就是熵：它让文档承诺一个不存在的控制。要加字段就接线。
func TestExampleConfigHasNoReservedKeys(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "config.example.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取示例配置失败: %v", err)
	}

	// "保留字段" / "预留" / "暂未使用" 这类说明都是在给死字段找理由
	banned := []string{"保留字段", "预留字段", "暂未使用", "尚未使用", "未实现"}
	for _, b := range banned {
		if strings.Contains(string(raw), b) {
			t.Errorf("示例配置里出现 %q —— 配置字段要么接线，要么删除", b)
		}
	}
}
