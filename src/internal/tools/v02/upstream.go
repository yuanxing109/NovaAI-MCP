package v02

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/novaai/novaai-mcp/internal/config"
)

// 本文件实现 novaai_config 的三个上游 action，以及 update 的合并校验。
//
// 为什么这些逻辑放在工具层而不是 upstream 包：它们都是**配置文件的读写**
// 与对注册表的编排，属于"工具"的职责；upstream 包只管连接、状态与路由，
// 不认识 config.json 的路径。

// updateConfig 处理 `action=update`。
//
// 与早期实现的差别（有意收紧）：**先合并、再校验、最后写**。
//
//   - 合并：客户端常只发一个字段（`{"listen":"127.0.0.1:5322"}`）。
//     直接落盘会让 config.json 变成一个缺字段的半截文件，daemon 下次
//     启动会在 Validate 处失败 —— 一次"改端口"变成"服务起不来"。
//   - 校验：写进去的东西必须能通过启动闸门。写一个装不上去的配置
//     比拒绝这次调用糟得多，因为失败会推迟到重启时才暴露。
//   - 落盘：写的是**合并后的完整配置**，不是客户端发来的片段。
//     这样 config.json 永远是一份完整、可直接读的契约。
//
// 注意只写文件、不重载：`listen` 这类字段仍然需要重启 supervisor 生效。
// 只有上游配置可以用 reload_upstreams 立即生效。
func updateConfig(configPath string, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return errFail("MISSING_CONFIG", "config 参数必填"), nil
	}

	merged := config.Default()
	if err := json.Unmarshal(raw, merged); err != nil {
		return errFail("INVALID_JSON", err.Error()), nil
	}
	if err := config.Validate(merged); err != nil {
		return errFail("INVALID_CONFIG", err.Error()), nil
	}
	if err := config.WriteAtomic(configPath, merged); err != nil {
		return nil, err
	}
	return okMsg("配置已更新（完整合并后写入），重启 supervisor 生效；上游配置可改用 reload_upstreams 立即生效"), nil
}

// probeUpstreams 重新探测上游。
//
// name 为空时探测全部；给了 name 就只探测那一个 —— WebUI 的每行「探测」
// 按钮需要后者，否则点一个上游会连带把别的都探一遍（对 stdio 上游意味着
// 无谓地等一个 timeout）。
func probeUpstreams(ctx context.Context, name string, deps *Deps) (any, error) {
	if deps.Upstreams == nil {
		return errFail("UPSTREAM_UNAVAILABLE", "上游聚合未启用"), nil
	}
	if name == "" {
		deps.Upstreams.ProbeAll(ctx)
	} else {
		if !deps.Upstreams.Has(name) {
			return errFail("NOT_FOUND", fmt.Sprintf("未知上游: %s", name)), nil
		}
		if err := deps.Upstreams.Probe(ctx, name); err != nil {
			return errFail("UPSTREAM_PROBE_FAILED", err.Error()), nil
		}
	}
	return ok(map[string]any{
		"upstreams": deps.Upstreams.Status(),
		"tools":     deps.Upstreams.ToolCount(),
	}), nil
}

// reloadUpstreams 从磁盘重读配置并重建上游注册表。
//
// 这是 WebUI 的主通路：WebUI 无法调 Go 内部 API，它直接原子改写
// config.json 的 upstreams 数组，然后调这个 action 让 daemon 立刻跟上。
//
// 用 config.Load（含 Validate）而不是直接读文件：WebUI 写坏配置文件时，
// 这里必须拒绝并保留**当前可用**的注册表，而不是把一个非法配置装进去。
func reloadUpstreams(ctx context.Context, configPath string, deps *Deps) (any, error) {
	if deps.Upstreams == nil {
		return errFail("UPSTREAM_UNAVAILABLE", "上游聚合未启用"), nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return errFail("INVALID_CONFIG", "重新加载失败，已保留原有上游： "+err.Error()), nil
	}
	deps.Upstreams.Reload(cfg)
	deps.Upstreams.ProbeAll(ctx)
	return ok(map[string]any{
		"upstreams": deps.Upstreams.Status(),
		"tools":     deps.Upstreams.ToolCount(),
	}), nil
}

// restartUpstream 重启指定上游（关掉旧连接后强制重探）。
func restartUpstream(ctx context.Context, name string, deps *Deps) (any, error) {
	if name == "" {
		return errFail("MISSING_PARAM", "name 必填"), nil
	}
	if deps.Upstreams == nil {
		return errFail("UPSTREAM_UNAVAILABLE", "上游聚合未启用"), nil
	}
	if !deps.Upstreams.Has(name) {
		return errFail("NOT_FOUND", fmt.Sprintf("未知上游: %s", name)), nil
	}
	if err := deps.Upstreams.Restart(ctx, name); err != nil {
		return errFail("UPSTREAM_RESTART_FAILED", err.Error()), nil
	}
	for _, st := range deps.Upstreams.Status() {
		if st.Name == name {
			return ok(st), nil
		}
	}
	return errFail("NOT_FOUND", fmt.Sprintf("未知上游: %s", name)), nil
}
