# NovaAI-MCP MCP 扩展规范 v1

本规范定义 NovaAI-MCP 在标准 MCP 2025-06-18 之上引入的私有扩展字段、
技能系统与错误码约定。任何客户端读取本文档后即可完整对接所有扩展能力；
不实现这些扩展的客户端仍可正常使用标准 `tools/call` 接口。

---

## 1. 客户端能力协商

### 1.1 客户端声明

在 `initialize` 请求的 `params.capabilities.experimental` 中声明支持的扩展：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {
      "experimental": {
        "novaai_skill": true
      }
    },
    "clientInfo": {
      "name": "example-client",
      "version": "1.0.0"
    }
  }
}
```

| 字段 | 含义 |
|------|------|
| novaai_skill | 客户端支持技能系统的 match/get |

未声明时，服务端返回的 instructions 会引导客户端使用 fallback 方式处理。

> 曾经声明的 `novaai_tasks`（长任务 taskId 轮询）**已移除**：服务端没有任何
> 工具会产生 taskId，`novaai_task` 工具本身也从未实现。声明一个不存在的能力
> 只会让客户端去轮询一个永远为空的接口。

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

---

## 2. 工具注解扩展

### 2.1 标准注解

所有工具在 tools/list 中返回标准注解：

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
等级表维护在服务端，部分工具还会按 `action` 参数细分。

| 等级 | 含义 | 示例 |
|------|------|------|
| 0 | 只读，无副作用 | novaai_status, novaai_fs_info |
| 1 | 低风险写操作 | novaai_fs_write, novaai_archive |
| 2 | 中风险操作 | novaai_shell, novaai_fs_manage |
| 3 | 高风险操作 | novaai_power, novaai_root_module |

风险等级**参与调用准入**：当前 profile 的 `riskCeiling` 是允许的最高等级，
超过即拒绝；`allowTools` / `denyTools` 决定工具名是否放行（`denyTools` 优先）。
拒绝时返回 JSON-RPC 错误 `-32003` 并写入 `profile_denied` 审计。
profile 的选取见 [config.md](config.md) 的 profiles 与 sessionBinding 两节。

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
`工具 X 内部 panic`，把真正原因藏起来。修复前有 9 个 handler 属于这种情况，
详见 [KNOWN_ISSUES.md](KNOWN_ISSUES.md) 第 5c 节。

---

## 3. 技能系统扩展

技能来自**内置**目录（随模块安装的 `skills/*.md`）。不存在"自动学习"：
服务端没有写入 learned 技能的代码路径，`novaai_skill` 也不再有 `skillSource`
参数或 `source` 字段 —— 只会有内置技能一种来源。

匹配是**大小写不敏感的子串包含**，不是语义检索；结果不返回相似度分数。

### 3.1 技能匹配

```json
{
  "name": "novaai_skill",
  "arguments": {
    "action": "match",
    "query": "如何截屏",
    "limit": 3
  }
}
```

响应：

```json
{
  "success": true,
  "data": {
    "matches": [
      { "id": "screenshot" }
    ]
  }
}
```

### 3.2 技能获取

```json
{
  "name": "novaai_skill",
  "arguments": {
    "action": "get",
    "id": "screenshot"
  }
}
```

响应：

```json
{
  "success": true,
  "data": {
    "id": "screenshot",
    "content": "# 截屏技能\n\n使用 novaai_screen 工具..."
  }
}
```

### 3.3 可用操作

| action | 说明 |
|--------|------|
| match | 按查询文本对内置技能做子串匹配，最多返回 `limit` 条 |
| get | 读取指定技能的正文 |
| list | 列出全部内置技能（`id` + `path`） |
| stats | 返回内置技能总数 |

---

## 4. 错误码约定

### 4.1 JSON-RPC 标准错误码

| 错误码 | 含义 |
|--------|------|
| -32700 | JSON 解析失败 |
| -32600 | 无效的请求（含请求体超过 `limits.maxRequestBytes`） |
| -32601 | 方法不存在 |
| -32602 | 无效的参数 |
| -32603 | 内部错误 |

### 4.2 NovaAI 自定义错误码

| 错误码 | 含义 |
|--------|------|
| -32001 | 鉴权失败（含 Host/Origin 校验失败） |
| -32009 | 频率限制 |
| -32010 | 并发超限 |
| -32014 | 会话创建失败（会话数达到 `session.maxSessions`） |
| -32015 | 工具不存在 |

### 4.3 工具级错误码

工具返回的 `code` 字段：

| code | 含义 |
|------|------|
| OK | 成功 |
| UNKNOWN_ACTION | 未知操作 |
| MISSING_PARAM | 缺少必填参数 |
| EXEC_FAILED | 命令执行失败 |
| NOT_CONFIRMED | 需要确认 |
| NOT_FOUND | 资源未找到 |

> **结果截断不通过 code 表达。** 当单个工具结果超过 `limits.resultPreviewBytes`
> 时，服务端把 `content` 截断到上限（不切断 UTF-8 字符），追加一行
> `[结果已截断：原始 N 字节，上限 M 字节。请用更精确的参数缩小范围。]`，
> 并**丢弃 `structuredContent`**。只截 `content` 而保留完整的结构化副本
> 等于没省流量，还会让两者不一致。`isError` 保持 `false` —— 截断不是失败。

---

## 5. 会话管理

### 5.1 会话分配

| 请求 | 行为 |
|------|------|
| 包含 `initialize` | 创建会话，响应头返回 `Mcp-Session-Id` |
| 携带有效 `Mcp-Session-Id` | 复用该会话 |
| 其余请求 | **无状态**，不登记会话、不占用名额 |

最后一行是关键：早期实现给每个不带 sid 的请求都建一个新会话，导致不实现
会话的客户端每发一个请求就消耗一个 `session.maxSessions` 名额，约 32 次
之后开始持续收到 `-32014`，直到空闲超时（默认 30 分钟）才恢复。

### 5.2 会话观测

```json
{
  "name": "novaai_session_list",
  "arguments": {}
}
```

返回当前活跃会话的列表。`novaai_session_status` 返回当前请求所在会话的详情
（无状态请求返回 `stateless: true`）。

---

## 6. 审计日志

所有工具调用都会记录审计日志，格式：

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

审计日志存储在 `/data/adb/novaai-mcp/audit/` 目录下。
