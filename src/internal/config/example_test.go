package config

import (
	"encoding/json"
	"errors"
	"fmt"
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
// 配置文档里，但 Go 侧零访问 —— 文档在教用户配置两个不生效的旋钮。
// 两次都是靠人工读代码才发现的，而"按名字全仓计数"的机械检查会假阴性
// （`crashDir` 被别处的 `crash` 掩盖，`capabilities` 被探针脚本里 MCP
// 协议的 `capabilities` 掩盖）。
//
// 这里改成结构化的边界断言，两道：
//   - 示例的键集合必须与结构体的 json tag **完全一致**；
//   - 示例的取值必须与 Default() **完全一致**。
//
// 第二道是本轮新增的。它当场抓到一个真实缺陷：default.go 与 migrate.go
// 用 filepath.Join 拼接 Android 路径，在 Windows 上产出反斜杠，
// 而 pathguard 会把不以 "/" 开头的路径当成相对路径 —— 保护静默失效。
// 只比键集合是看不出来的。

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

// extractJSONBlock 从 config.md 中取出第一个 ```json 围栏里的内容。
//
// 曾经的示例是独立文件 docs/config.example.json，与 config.md 是同一份
// 契约的两个 owner，没有任何机制保证两者同步，必然漂移。现在只有
// config.md 一份，这里把它挖出来仍然做逐键比对 ——
// 少一个 owner，但不减一道闸门。
func extractJSONBlock(raw string) ([]byte, error) {
	const fence = "```json"
	i := strings.Index(raw, fence)
	if i < 0 {
		return nil, errors.New("config.md 里找不到 ```json 代码块")
	}
	rest := raw[i+len(fence):]
	j := strings.Index(rest, "```")
	if j < 0 {
		return nil, errors.New("config.md 里的 ```json 代码块没有闭合")
	}
	return []byte(rest[:j]), nil
}

// loadDocExample 读取并解析 config.md 里的完整配置示例。
func loadDocExample(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "..", "docs", "config.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 config.md 失败: %v", err)
	}
	block, err := extractJSONBlock(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	var example map[string]any
	if err := json.Unmarshal(block, &example); err != nil {
		t.Fatalf("config.md 的示例不是合法 JSON: %v", err)
	}
	return example
}

// TestExampleConfigMatchesStruct 断言 docs/config.md 的完整示例与 Config
// 的 json tag 完全一致。
func TestExampleConfigMatchesStruct(t *testing.T) {
	example := loadDocExample(t)

	var problems []string
	checkShape("config", shapeOf(reflect.TypeOf(Config{})), example, &problems)

	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("%s", p)
	}
	if len(problems) > 0 {
		t.Log("增删配置字段时请同步 docs/config.md 的「完整配置」一节")
	}
}

// TestExampleConfigMatchesDefaults 断言示例的**取值**与 Default() 一致。
//
// 只比键集合是不够的：键都在、值写错（比如 readonly 的 riskCeiling
// 写成 3）同样会误导用户，而 shape 检查看不出来。
func TestExampleConfigMatchesDefaults(t *testing.T) {
	example := loadDocExample(t)

	def := Default()
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("序列化默认配置失败: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("反序列化默认配置失败: %v", err)
	}
	// 示例里 value 留空，表示"首次启动生成"。
	want["security"].(map[string]any)["token"].(map[string]any)["value"] = ""

	diffJSON(t, "config", want, example)
}

// diffJSON 递归比较两个 JSON 值，差异直接报成测试失败。
func diffJSON(t *testing.T, path string, want, got any) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s: 期望对象，实际 %T", path, got)
			return
		}
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			gv, ok := g[k]
			if !ok {
				t.Errorf("%s.%s: 示例里缺少这个键", path, k)
				continue
			}
			diffJSON(t, path+"."+k, w[k], gv)
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			t.Errorf("%s: 期望数组，实际 %T", path, got)
			return
		}
		if len(w) != len(g) {
			t.Errorf("%s: 数组有 %d 个元素，期望 %d", path, len(g), len(w))
			return
		}
		for i := range w {
			diffJSON(t, fmt.Sprintf("%s[%d]", path, i), w[i], g[i])
		}
	default:
		if w != got {
			t.Errorf("%s: 示例值 %v，期望 %v", path, got, w)
		}
	}
}

// TestDocHasNoReservedKeys 是一个更窄的回归锁：
// 曾经被注释为"保留字段"却没有消费者的键，不允许再回来。
//
// 保留字段就是熵：它让文档承诺一个不存在的控制。要加字段就接线。
func TestDocHasNoReservedKeys(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "config.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 config.md 失败: %v", err)
	}
	block, err := extractJSONBlock(string(raw))
	if err != nil {
		t.Fatal(err)
	}

	// "保留字段" / "预留" / "暂未使用" 这类说明都是在给死字段找理由
	banned := []string{"保留字段", "预留字段", "暂未使用", "尚未使用", "未实现"}
	for _, b := range banned {
		if strings.Contains(string(block), b) {
			t.Errorf("配置示例里出现 %q —— 配置字段要么接线，要么删除", b)
		}
	}
}
