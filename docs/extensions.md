# NovaAI-MCP MCP 扩展规范 v1

本规范定义 NovaAI-MCP 在标准 MCP 2025-06-18 之上引入的约定。
实现标准 `tools/call` 的客户端即可完整使用本服务。

> **重构提示**：工具集由 61 个精简到 **30 个本地工具**，另加**动态合并的
> 上游 MCP 工具**（`{上游名}__{工具名}`）。技能系统（`novaai_skill`）、
> 会话观测工具（`novaai_session_*`）、鉴权状态工具（`novaai_auth_status` /
> `novaai_audit_status`）与多档位 profile 已删除。
> 完整工具表见 [README.md](../README.md)，上游聚合见 [upstream.md](upstream.md)。

---

## 1. 能力协商

### 1.1 客户端声明

**当前服务端不识别任何私有扩展能力。**

`initialize` 的 `params.capabilities.experimental` 会被原样接收但不做分支 ——
历史上曾声明 `novaai_skill`（技能系统），该工具已随本轮精简删除；
更早还声明过 `novaai_tasks`（长任务轮询），服务端从未有任何工具产生 taskId。

保留本节的目的是：**客户端不要再声明这些**。声明一个不存在的能力只会让
客户端去调一个永远返回 `-32015` 的工具。

### 1.2 服务端响应

服务端只声明**实际实现**的能力。当前只有 `tools`：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-06-18",
    "capabilities": {
      "tools": { "listChanged": false }
    },
    "serverInfo": {
      "name": "novaai-android-mcp",
      "title": "NovaAI Mobile Control Protocol",
      "version": "0.05",
      "description": "Android Root MCP 服务"
    },
    "instructions": "NovaAI-MCP：Android Root 全能力服务。..."
  }
}
```

`prompts` / `resources` 未实现，因此不再声明 —— 声明了能力却只返回空列表，
会让部分客户端在校验能力后报错。协议版本按客户端请求协商，支持
`2025-06-18` / `2025-03-26` / `2024-11-05`，不支持时回退到 `2025-06-18`。

`serverInfo.version` 的唯一来源是 `internal/mcp/server.go` 的 `ServerVersion`
常量，模块版本号（`module.prop`）另算。

---

## 2. 工具注解扩展

### 2.1 标准注解

所有工具在 tools/list 中返回标准字段：

```json
{
  "name": "novaai_shell",
  "title": "执行命令",
  "description": "以 root、shell 或指定 UID 执行命令",
  "inputSchema": { ... }
}
```

### 2.2 风险等级

风险等级是**服务端内部**的准入依据，不由工具在 `tools/list` 里声明
（`tools/list` 返回的字段只有 `name` / `title` / `description` / `inputSchema`）。
等级表维护在服务端（`internal/profile/risk.go`），部分工具还会按 `action`
参数细分。

| 等级 | 含义 | 示例 |
|------|------|------|
| 0 | 只读，无副作用 | novaai_status, novaai_fs_info |
| 1 | 低风险写操作 | novaai_fs_write, novaai_archive |
| 2 | 中风险操作 | novaai_shell, novaai_fs_manage |
| 3 | 高风险操作 | novaai_power, novaai_root_module |

风险等级与工具名一起交给档位判定。**当前只有 `default` 一个档位**：
`allowTools: ["*"]`、`denyTools: []`、`riskCeiling: 3`，即全部放行。
判定闸门仍然存在（工具存在性 → 档位 → 限流 → 执行），只是档位恒为全放行。
被拒时返回 JSON-RPC 错误 `-32003` 并写入 `profile_denied` 审计
（`default` 下理论不可达）。详见 [security.md](security.md)。

**上游工具**不吃这张表，也不吃 `default` 档位的工具名匹配 —— 它们走
上游自己的 `denyTools` / `riskCeiling`，判定入口是
`upstream.Registry.Authorize`。风险推断（`ResolveRisk`）只是被复用来
给审计记一个等级。详见 [upstream.md](upstream.md)。

> 风险等级**刻意不进入 `tools/list`**。因此任何客户端（包括本项目的
> KernelSU WebUI）都无法从协议层读到它 —— 需要的客户端应当自己维护
> 一张允许清单，而不是让服务端为它改契约。

### 2.3 action 枚举

凡是按 `action` 分派的工具，其 `inputSchema` 都使用 `enum` 声明该参数，
客户端可以据此在调用前发现不支持的操作。

**但服务端不校验 `inputSchema`。** `internal/mcp/server.go` 把 `arguments`
原样交给 handler，不做 enum / required / 类型检查 —— 因此 schema 是**给客户端
看的契约**，不是服务端的准入闸门。真正兜住未知输入的是每个 handler 自己的
`default` 分支，返回 `UNKNOWN_ACTION`。

这条不变式由两处机械保证（不是靠人记得写）：

- `scripts/audit_actions.ps1`：静态检查「schema 声明的 action 都有分派」
  「声明了 action 就必须读 `in.Action`」「读了 action 就必须有兜底分支」
  「schema 参数名必须在 handler 里有同名 json tag」；
- `src/internal/tools/v02/actions_test.go`：用真实 handler 逐个试，断言未知
  action 与缺失 action 都"不 panic、不成功"。它比静态检查更强，且不依赖正则。

删除一个 action 之后，旧客户端仍可能发那个字符串。它必须得到一次干净的
`UNKNOWN_ACTION` 失败，而不是 panic —— 后者会被 `safeCall` 的 `recover` 兜成
`工具 X 内部 panic`，把真正原因藏起来。

---

## 3. 已移除的能力（对接提示）

本节留给按**旧文档**对接的客户端，便于定位问题。发送这些工具名会收到
`-32015 工具不存在`；发这些参数会被**静默忽略**（服务端不校验 schema）。

### 3.1 已删除的工具

| 旧工具 | 替代方式 |
|--------|----------|
| `novaai_skill`（match / get / list / stats） | 直接读设备上的 `{stateDir}/skills/*.md`（用 `novaai_fs_read`），见 [security.md](security.md) 的可写子树 |
| `novaai_reverse_*`（apk / dex / smali / strings / binary / install_tools） | `novaai_shell` 调 apktool / jadx / smali |
| `novaai_hook_frida` `novaai_hook_xposed` | `novaai_shell` |
| `novaai_schedule` | `novaai_script` 或 `novaai_shell` |
| `novaai_app_permission` `novaai_app_policy` `novaai_app_export` `novaai_default_app` `novaai_notification` | `novaai_shell` 调 `pm` / `cmd` |
| `novaai_service` `novaai_property` `novaai_setting` `novaai_developer` | `novaai_shell` 调 `service` / `setprop` / `settings` |
| `novaai_display` `novaai_audio` `novaai_connectivity` `novaai_locale_time` `novaai_input_method` `novaai_accessibility` | `novaai_shell` 调 `settings` / `cmd` |
| `novaai_network` `novaai_backup` | `novaai_shell` 调 `curl` / `tar` |
| `novaai_device_info` | `novaai_capabilities` 或 `novaai_shell` 调 `getprop` |
| `novaai_auth_status` `novaai_session_status` `novaai_session_list` `novaai_audit_status` | 功能随之删除（无鉴权、无档位、无会话观测工具）；审计日志直接读 `{stateDir}/audit/` |
| `novaai_task` | 更早一轮已删（其 5 个 action 全部空转） |

### 3.2 已删除的参数

| 参数 | 曾出现的工具 | 说明 |
|------|-------------|------|
| `confirmDangerous` | `novaai_fs_write` `novaai_fs_manage` `novaai_power` 等 | `/sdcard/Android/{data,obb}` 现在是**硬拒绝**，不提供确认放行。确需访问走 `novaai_shell` |
| `background` | `novaai_shell` `novaai_script` `novaai_archive` `novaai_app_install` `novaai_screen` `novaai_diagnostics` | 从未被任何 handler 读取。传 `true` 不会让命令后台执行，只会同步阻塞到超时 |
| `action`（单动作工具上） | `novaai_shell` `novaai_status` `novaai_capabilities` `novaai_root_info` `novaai_app_list` `novaai_app_install` `novaai_transfer_export` | 这些工具声明过但从不分派；现已从 schema 删除 |

### 3.3 已移除的配置段

`security` / `network` / `profiles` / `sessionBinding` / `rateLimit` / `session` /
`paths` / `skill` / `capabilities` —— 全部删除，残留会被静默忽略。
见 [migration.md](migration.md)。

---

## 4. 错误码约定

### 4.1 JSON-RPC 标准错误码

| 错误码 | 含义 |
|--------|------|
| -32700 | JSON 解析失败 |
| -32600 | 无效的请求（含请求体超过 `MaxRequestBytes`，默认 64 MiB） |
| -32601 | 方法不存在 |
| -32602 | 无效的参数 |
| -32603 | 内部错误 |

### 4.2 NovaAI 自定义错误码

| 错误码 | 含义 |
|--------|------|
| -32001 | Host / Origin 校验失败（**不再是鉴权失败** —— 本服务不鉴权） |
| -32003 | 档位不允许该工具（`default` 放行全部，理论不可达） |
| -32009 | 频率限制 |
| -32010 | 并发超限 |
| -32014 | 会话创建失败（会话数达到上限 32） |
| -32015 | 工具不存在 |

### 4.3 工具级错误码

工具结果里与错误相关的三个字段：

| 字段 | 用途 | 稳定性 |
|------|------|--------|
| `code` | **英文**标识符，程序按它分支（如 `PROTECTED_PATH`） | 稳定，**不改**；改了就是破约 |
| `codeName` | 同一 code 的**中文短标签**（如 `路径受保护`） | 稳定；回答"这是哪一类错" |
| `message` | 一次性的详细原因，带具体路径/参数 | 不保证稳定，只供阅读 |

```json
{
  "success": false,
  "code": "PROTECTED_PATH",
  "codeName": "路径受保护",
  "message": "路径受保护（/system）：系统分区"
}
```

**不要按 `codeName` 或 `message` 写分支** —— 按 `code`。中文名是为了让人和
AI 一眼看懂，不是为了匹配。

> **本表不在这里重复。** 完整清单（code + 中文名 + 说明）只有一处：
> [errors.md](errors.md) 第 3 节。它的唯一声明点是
> `src/internal/tools/v02/codes.go` 的 `codeNames`，
> 由 `codes_test.go` 机械比对（少一行、多一行、中文名写错都会 FAIL）。
> 在这里再抄一份就是制造第二个 owner。
>
> 数字错误码（`-32015` 这类，见 4.1/4.2）**不参与这套机制** ——
> 它们来自 JSON-RPC 2.0 与 MCP 生态的约定。

#### 上游工具的失败形式

上游转发失败**不走 JSON-RPC error**，而是 `result.isError = true`，
原因在 `content[0].text`。这与本地工具的执行失败一致 —— 按 MCP 规范，
那属于"工具结果错误"。

唯一的例外是**上游名前缀对不上任何已注册上游**：那是"工具不存在"，
返回 `-32015`。注意 `fake__`（前缀对、工具名为空）也属于这一档 ——
空工具名不是合法路由目标。

> **结果截断不通过 code 表达。** 当单个工具结果（本地或上游）超过
> `resultPreviewBytes` 时，服务端把 `content` 截断到上限（不切断 UTF-8
> 字符），追加一行
> `[结果已截断：原始 N 字节，上限 M 字节。请用更精确的参数缩小范围。]`，
> 并**丢弃 `structuredContent`**。只截 `content` 而保留完整的结构化副本
> 等于没省流量，还会让两者不一致。`isError` 保持原样 —— 截断不是失败。

---

## 5. 会话管理

### 5.1 会话分配

| 请求 | 行为 |
|------|------|
| 包含 `initialize` | 创建会话，响应头返回 `Mcp-Session-Id` |
| 携带有效 `Mcp-Session-Id` | 复用该会话 |
| 其余请求 | **无状态**，不登记会话、不占用名额 |

最后一行是关键：早期实现给每个不带 sid 的请求都建一个新会话，导致不实现
会话的客户端每发一个请求就消耗一个会话名额，约 32 次之后开始持续收到
`-32014`，直到空闲超时（默认 30 分钟）才恢复。

### 5.2 会话不携带权限（重要）

```
// 会话不携带权限。鉴权结果固定为全局 default profile，永不从 session 读取。
// 历史实现曾把 profile 挂在 session 上，仅作观测；现已删除，防止误读。
```

会话对象只有三个字段：`ID` / `CreatedAt` / `LastSeen`。
**任何权限判断都不读会话** —— 会话 id 由客户端携带并能被复用，
如果它参与权限决策，低权限调用方就能继承高权限会话。

### 5.3 会话常量（不可配置）

| 项 | 值 |
|----|----|
| 空闲超时 | 30 分钟 |
| 最大会话数 | 32 |
| 巡检间隔 | 5 分钟 |

它们曾是 `session.*` 配置项，现收敛为常量。

---

## 6. 审计日志

所有工具调用都会记录审计日志，JSONL 按日命名（`audit-YYYY-MM-DD.jsonl`），
目录 `0600`/`0700`，单文件超 10 MB 加序号轮转，保留 7 天。

```json
{
  "ts": "2025-06-18T12:00:00.000000000Z",
  "seq": 1,
  "session": "sess-xxx",
  "event": "tool_call",
  "tool": "novaai_shell",
  "risk": 2,
  "result": "ok",
  "duration_ms": 150
}
```

参数按固定白名单脱敏：`action` `path` `package` `name` `query` `url`
`tool` `pattern` `cmd` `command` 原样记录（截断到 256 字节），其余一律 `***`。
**没有可关闭脱敏的开关**，也没有 `UID` 字段（无鉴权，没有 UID 可记）。

记录的事件：`initialize` `tool_call` `rate_limited` `concurrency_limited`
`host_rejected` `origin_rejected` `session_create` `session_close`
`session_idle_expire`，以及上游聚合的
`upstream_probe` `upstream_call` `upstream_launch` `upstream_restart`
`upstream_reload` `upstream_denied`。

上游事件的 `tool` 字段形如 `upstream:{上游名}`（`upstream_call` 用带前缀的
完整工具名）。`upstream_reload` 的 `detail` 只记**集合差异**
（新增/移除哪些上游），不记整份配置 —— 审计是给人排障用的，
把 `url` / `command` 全量抄进去既长又可能带进敏感串。

审计日志存储在 `/data/adb/novaai-mcp/audit/` 目录下。
