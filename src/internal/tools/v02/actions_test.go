package v02

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/novaai/novaai-mcp/internal/adapter"
	"github.com/novaai/novaai-mcp/internal/config"
)

// 本文件机械地锁住"工具契约与实现一致"这条不变式。
//
// 背景：mcp 层**不做** inputSchema 校验，arguments 原样交给 handler。
// 因此 schema 里的 enum 只是给客户端看的提示，服务端真正依赖的是
// 每个 handler 自己的 switch 兜底分支。删掉一个假 action 之后，旧客户端
// 仍可能把那个字符串发进来 —— 它必须得到一次干净的失败，而不是 panic，
// 也不能被当成成功。
//
// 权威口径：任何未声明的 action 都不允许"成功"，因为 schema 从没承诺过它。
// 允许因缺少其它必填参数而失败（参数校验先于 action 分派是合理顺序）。

type toolUnderTest struct {
	name    string
	desc    string
	schema  map[string]any
	handler Handler
}

func captureTools(t *testing.T) []toolUnderTest {
	t.Helper()

	cfg := config.Default()
	cfg.Paths.StateDir = t.TempDir()
	cfg.Paths.WorkspaceRoot = t.TempDir()

	deps := &Deps{
		Config:   cfg,
		Adapter:  adapter.NewAOSPAdapter(map[string]string{}),
		StateDir: cfg.Paths.StateDir,
		Version:  "test",
	}

	var out []toolUnderTest
	RegisterAllV02Tools(func(name, title, desc string, schema map[string]any, h Handler) {
		out = append(out, toolUnderTest{name: name, desc: desc, schema: schema, handler: h})
	}, deps)
	return out
}

// declaredActions 取出 schema 里 action 字段声明的枚举值。
// 没有 action 字段（单动作工具，如 novaai_shell）时返回 nil。
func declaredActions(schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return nil
	}
	action, _ := props["action"].(map[string]any)
	if action == nil {
		return nil
	}
	raw, _ := action["enum"].([]string)
	return raw
}

// panicError 把 handler 的 panic 与正常 `return nil, err` 区分开。
//
// 这个区分是必要的：novaai_config get 在 config.json 缺失时会正常返回
// 一个 error（mcp 层会把它转成 isError 结果），那是设计内的失败路径，
// 不是本文件要抓的崩溃。
type panicError struct{ value any }

func (e *panicError) Error() string { return fmt.Sprintf("panic: %v", e.value) }

// callTool 调用一次 handler，把 panic 转成 *panicError。
func callTool(ctx context.Context, h Handler, args string) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &panicError{value: r}
		}
	}()
	return h(ctx, json.RawMessage(args))
}

// assertNoPanic 断言一次调用没有 panic。
// 允许 handler 正常返回 error —— 那同样是一次"没有成功"的结果。
func assertNoPanic(t *testing.T, label string, err error) {
	t.Helper()
	var pe *panicError
	if errors.As(err, &pe) {
		t.Fatalf("%s: handler panic: %v", label, pe.value)
	}
}

// assertNotSuccess 断言结果不是"成功"。
// 未声明的输入永远不该被当成成功 —— schema 从没承诺过它。
func assertNotSuccess(t *testing.T, label string, res any, err error) {
	t.Helper()
	if err != nil {
		return // 正常返回的错误也是一次失败，符合预期
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("%s: 返回值不是对象: %#v", label, res)
	}
	if m["success"] == true {
		t.Fatalf("%s: 未声明的输入不应成功，实际: %#v", label, m)
	}
}

// TestUnknownActionIsRejected 是删除假 action 之后的负向回归：
// 每个带 action 枚举的工具收到未声明的 action 时，必须干净失败。
//
// 这条测试直接覆盖"删掉 route/reset/batch/... 之后旧客户端还在发"的场景。
// 修复前有 9 个工具在这里 panic：它们在 switch 里构造 cmd，然后无条件
// 索引 cmd[0]，未知 action 让切片为空。
func TestUnknownActionIsRejected(t *testing.T) {
	const bogus = "__no_such_action__"

	for _, tool := range captureTools(t) {
		if declaredActions(tool.schema) == nil {
			continue
		}
		t.Run(tool.name, func(t *testing.T) {
			res, err := callTool(context.Background(), tool.handler,
				fmt.Sprintf(`{"action":%q}`, bogus))
			assertNoPanic(t, "未知 action", err)
			assertNotSuccess(t, "未知 action", res, err)
		})
	}
}

// TestMissingActionIsRejected 覆盖"完全不带 action"的情况。
// 空字符串同样不能走进任何分支，更不能 panic 或被当成成功。
func TestMissingActionIsRejected(t *testing.T) {
	for _, tool := range captureTools(t) {
		if declaredActions(tool.schema) == nil {
			continue
		}
		t.Run(tool.name, func(t *testing.T) {
			res, err := callTool(context.Background(), tool.handler, `{}`)
			assertNoPanic(t, "缺少 action", err)
			assertNotSuccess(t, "缺少 action", res, err)
		})
	}
}

// TestEveryDeclaredActionIsDispatched 保证 schema 不再承诺 handler 不处理的
// action。这是 scripts/audit_actions.ps1 的 Go 版本：静态脚本靠正则，这里
// 用真实 handler 逐个试，结论更强。
func TestEveryDeclaredActionIsDispatched(t *testing.T) {
	for _, tool := range captureTools(t) {
		actions := declaredActions(tool.schema)
		if actions == nil {
			continue
		}
		t.Run(tool.name, func(t *testing.T) {
			for _, a := range actions {
				res, err := callTool(context.Background(), tool.handler,
					fmt.Sprintf(`{"action":%q}`, a))
				assertNoPanic(t, fmt.Sprintf("action=%q", a), err)
				if err != nil {
					continue // 正常返回的错误：不是"未处理"，见 panicError 注释
				}
				m, ok := res.(map[string]any)
				if !ok {
					t.Fatalf("action=%q 返回值不是对象: %#v", a, res)
				}
				if m["code"] == "UNKNOWN_ACTION" {
					t.Errorf("schema 声明了 action=%q 但 handler 未处理", a)
				}
			}
		})
	}
}

// TestToolsAreWellFormed 是注册表层面的健全性检查：
// 每个工具必须有名字、描述、对象型 schema。
func TestToolsAreWellFormed(t *testing.T) {
	tools := captureTools(t)
	if len(tools) == 0 {
		t.Fatal("没有注册任何工具")
	}

	seen := map[string]bool{}
	for _, tool := range tools {
		if tool.name == "" {
			t.Error("存在空名字的工具")
		}
		if seen[tool.name] {
			t.Errorf("工具名重复: %s", tool.name)
		}
		seen[tool.name] = true

		if tool.desc == "" {
			t.Errorf("工具 %s 没有描述", tool.name)
		}
		if tool.schema == nil {
			t.Errorf("工具 %s 没有 schema", tool.name)
			continue
		}
		if tool.schema["type"] != "object" {
			t.Errorf("工具 %s 的 schema 不是 object", tool.name)
		}
		if _, ok := tool.schema["properties"]; !ok {
			t.Errorf("工具 %s 的 schema 没有 properties", tool.name)
		}
		// required 里出现的名字必须真的在 properties 里。
		props, _ := tool.schema["properties"].(map[string]any)
		required, _ := tool.schema["required"].([]string)
		for _, r := range required {
			if _, ok := props[r]; !ok {
				t.Errorf("工具 %s: required 里的 %q 不在 properties 中", tool.name, r)
			}
		}
	}
}

// TestNoToolDeclaresUnimplementedFollow 锁住 G6：novaai_log 的 schema 不再声明
// `follow`。
//
// 该参数被 handler 读取后**恒定**返回 NOT_IMPLEMENTED —— 一个不可能被满足的
// 参数是骗人的契约。它同时逃过了 scripts/audit_actions.ps1 的"摆设参数"检查：
// 那条检查只比对 json tag 是否存在，不判断读完之后做了什么，所以这类缺陷在
// 正则层面不可判定。见 docs/KNOWN_ISSUES.md 关于该审计边界的说明。
func TestNoToolDeclaresUnimplementedFollow(t *testing.T) {
	for _, tool := range captureTools(t) {
		if tool.name != "novaai_log" {
			continue
		}
		props, _ := tool.schema["properties"].(map[string]any)
		if _, ok := props["follow"]; ok {
			t.Error("novaai_log 仍声明 follow：该参数恒定返回 NOT_IMPLEMENTED，不应出现在 schema 里")
		}
		return
	}
	t.Fatal("未找到 novaai_log 工具")
}
