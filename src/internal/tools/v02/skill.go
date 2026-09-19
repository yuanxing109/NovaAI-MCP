package v02

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// registerSkillTools 注册技能工具。
//
// 只有内置技能。服务没有"自动学习"机制：stateDir/skills/learned 从来没有任何
// 代码写入过。因此早期版本声明的 skillSource（all/builtin/learned）与 forget
// 动作都是在承诺不存在的能力 —— forget 只允许删 learned 下的文件，而 learned
// 永远是空的，所以它必然失败。
func registerSkillTools(reg RegisterFn, deps *Deps) {
	reg("novaai_skill", "技能系统", "按关键词匹配并读取内置技能文档",
		objSchema(map[string]any{
			"action": enumProp("操作", "match", "get", "list", "stats"),
			"query":  strProp("搜索查询"),
			"id":     strProp("技能 ID"),
			"limit":  intProp("返回上限"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Query  string `json:"query"`
				ID     string `json:"id"`
				Limit  int    `json:"limit"`
			}
			_ = json.Unmarshal(args, &in)

			skillsDir := filepath.Join(deps.StateDir, "skills")

			switch in.Action {
			case "list":
				entries, _ := os.ReadDir(skillsDir)
				var items []map[string]any
				for _, e := range entries {
					if !strings.HasSuffix(e.Name(), ".md") {
						continue
					}
					items = append(items, map[string]any{
						"id":   strings.TrimSuffix(e.Name(), ".md"),
						"path": filepath.Join(skillsDir, e.Name()),
					})
				}
				return ok(map[string]any{"skills": items}), nil
			case "get":
				if in.ID == "" {
					return errFail("MISSING_ID", "id 必填"), nil
				}
				if !idRe.MatchString(in.ID) {
					return errFail("INVALID_PARAM", "非法技能 ID: "+in.ID), nil
				}
				b, err := os.ReadFile(filepath.Join(skillsDir, in.ID+".md"))
				if err != nil {
					return errFail("NOT_FOUND", in.ID), nil
				}
				return ok(map[string]any{"id": in.ID, "content": string(b)}), nil
			case "match":
				if in.Query == "" {
					return errFail("MISSING_QUERY", "query 必填"), nil
				}
				if in.Limit <= 0 {
					in.Limit = 3
				}
				entries, _ := os.ReadDir(skillsDir)
				var hits []map[string]any
				for _, e := range entries {
					if !strings.HasSuffix(e.Name(), ".md") {
						continue
					}
					b, err := os.ReadFile(filepath.Join(skillsDir, e.Name()))
					if err != nil {
						continue
					}
					if strings.Contains(strings.ToLower(string(b)), strings.ToLower(in.Query)) {
						hits = append(hits, map[string]any{
							"id": strings.TrimSuffix(e.Name(), ".md"),
						})
					}
					if len(hits) >= in.Limit {
						break
					}
				}
				return ok(map[string]any{"matches": hits}), nil
			case "stats":
				entries, _ := os.ReadDir(skillsDir)
				count := 0
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".md") {
						count++
					}
				}
				return ok(map[string]any{"total": count}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}
