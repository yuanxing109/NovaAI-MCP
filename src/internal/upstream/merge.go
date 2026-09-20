package upstream

import (
	"fmt"
	"sort"
	"strings"

	"github.com/novaai/novaai-mcp/internal/config"
)

// namespaceSep 是工具名的命名空间分隔符。
//
// 双下划线：单下划线在工具名里太常见（`read_file`），用它做分隔符会让
// "这到底是不是上游工具"变成一个需要查表才能回答的问题。
const namespaceSep = "__"

// FullName 拼出带命名空间的工具名。
//
// "前缀规则"只有这一个实现 —— config 文档、WebUI、路由、测试都引用它，
// 不允许任何地方手写 `name + "__" + tool`。
func FullName(upstreamName, toolName string) string {
	return upstreamName + namespaceSep + toolName
}

// SplitName 把一个带前缀的工具名拆回 (上游名, 裸工具名)。
//
// 先按**已注册的上游名**做最长前缀匹配，而不是简单地找第一个 `__`：
// 配置层禁止 name 含 `__`（config.Validate 把关），所以两种做法在合法
// 配置下等价；但按注册名匹配能让非法名字表现得可解释 —— 要么匹配到某个
// 真实上游，要么直接判为"不是上游工具"，而不是悄悄路由到错的上游。
func (r *Registry) SplitName(full string) (string, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	best := ""
	for _, name := range r.order {
		prefix := name + namespaceSep
		if strings.HasPrefix(full, prefix) && len(name) > len(best) {
			best = name
		}
	}
	if best == "" {
		return "", "", false
	}
	tool := strings.TrimPrefix(full, best+namespaceSep)
	// 空工具名不是合法路由目标。没有这道判定，`fake__` 会被当作
	// "上游 fake 的工具 ''"，一路转发给上游 —— 上游那边的行为无从预期，
	// 而调用方本应得到一次干净的 -32015。
	if tool == "" {
		return "", "", false
	}
	return best, tool, true
}

// MergedTools 返回应当合并进 tools/list 的上游工具（已带命名空间前缀）。
//
// 暴露规则：
//   - disabled 的上游**永不**暴露 —— 用户显式禁用它，就不该再看到它的工具；
//   - running 的上游暴露其全部工具；
//   - stopped / error 的上游默认不暴露，除非配了 exposeWhenStopped。
//     这时暴露的是**上一次成功探测**拿到的列表；从未成功过就无工具可暴露
//     —— 不能凭空编造 schema。
func (r *Registry) MergedTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := []Tool{}
	for _, name := range r.order {
		e := r.entries[name]
		if e == nil || !e.cfg.Enabled || e.status == config.UpstreamDisabled {
			continue
		}
		if e.status != config.UpstreamRunning && !e.cfg.ExposeWhenStopped {
			continue
		}
		for _, t := range e.tools {
			cp := t
			cp.Name = FullName(e.cfg.Name, t.Name)
			if cp.Description != "" {
				cp.Description = fmt.Sprintf("[上游 %s] %s", e.cfg.Name, cp.Description)
			} else {
				cp.Description = fmt.Sprintf("[上游 %s]", e.cfg.Name)
			}
			out = append(out, cp)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ToolCount 返回当前合并进工具面的上游工具数。
func (r *Registry) ToolCount() int {
	return len(r.MergedTools())
}
