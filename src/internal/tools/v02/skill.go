package v02

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func registerSkillTools(reg RegisterFn, deps *Deps) {
	reg("novaai_skill", "技能系统", "渐进匹配、按需读取并管理内置及自动学习技能",
		objSchema(map[string]any{
			"action":      enumProp("操作", "match", "get", "list", "stats", "forget"),
			"query":       strProp("搜索查询"),
			"id":          strProp("技能 ID"),
			"limit":       intProp("返回上限"),
			"offset":      intProp("偏移"),
			"skillSource": enumProp("来源", "all", "builtin", "learned"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action      string `json:"action"`
				Query       string `json:"query"`
				ID          string `json:"id"`
				Limit       int    `json:"limit"`
				Offset      int    `json:"offset"`
				SkillSource string `json:"skillSource"`
			}
			_ = json.Unmarshal(args, &in)

			skillsDir := filepath.Join(deps.StateDir, "skills")
			learnedDir := filepath.Join(skillsDir, "learned")

			switch in.Action {
			case "list":
				var items []map[string]any
				for _, dir := range []string{skillsDir, learnedDir} {
					entries, _ := os.ReadDir(dir)
					for _, e := range entries {
						if !strings.HasSuffix(e.Name(), ".md") {
							continue
						}
						items = append(items, map[string]any{
							"id":     strings.TrimSuffix(e.Name(), ".md"),
							"source": filepath.Base(dir),
							"path":   filepath.Join(dir, e.Name()),
						})
					}
				}
				return ok(map[string]any{"skills": items}), nil
			case "get":
				if in.ID == "" {
					return errFail("MISSING_ID", "id 必填"), nil
				}
				if !idRe.MatchString(in.ID) {
					return errFail("INVALID_PARAM", "非法技能 ID: "+in.ID), nil
				}
				for _, dir := range []string{skillsDir, learnedDir} {
					p := filepath.Join(dir, in.ID+".md")
					if b, err := os.ReadFile(p); err == nil {
						return ok(map[string]any{"id": in.ID, "content": string(b)}), nil
					}
				}
				return errFail("NOT_FOUND", in.ID), nil
			case "match":
				if in.Query == "" {
					return errFail("MISSING_QUERY", "query 必填"), nil
				}
				if in.Limit <= 0 {
					in.Limit = 3
				}
				var hits []map[string]any
				for _, dir := range []string{skillsDir, learnedDir} {
					entries, _ := os.ReadDir(dir)
					for _, e := range entries {
						if !strings.HasSuffix(e.Name(), ".md") {
							continue
						}
						b, err := os.ReadFile(filepath.Join(dir, e.Name()))
						if err != nil {
							continue
						}
						if strings.Contains(strings.ToLower(string(b)), strings.ToLower(in.Query)) {
							hits = append(hits, map[string]any{
								"id":     strings.TrimSuffix(e.Name(), ".md"),
								"source": filepath.Base(dir),
							})
						}
						if len(hits) >= in.Limit {
							break
						}
					}
				}
				return ok(map[string]any{"matches": hits}), nil
			case "stats":
				count := 0
				for _, dir := range []string{skillsDir, learnedDir} {
					entries, _ := os.ReadDir(dir)
					for range entries {
						count++
					}
				}
				return ok(map[string]any{"total": count}), nil
			case "forget":
				if in.ID == "" {
					return errFail("MISSING_ID", "id 必填"), nil
				}
				if !idRe.MatchString(in.ID) {
					return errFail("INVALID_PARAM", "非法技能 ID: "+in.ID), nil
				}
				p := filepath.Join(learnedDir, in.ID+".md")
				if err := guardPath(p, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := os.Remove(p); err != nil {
					return errFail("REMOVE_FAILED", err.Error()), nil
				}
				return okMsg("已删除"), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}
