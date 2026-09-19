# NovaAI-MCP MCP 扩展规范 v1

本规范定义 NovaAI-MCP 在标准 MCP 2025-06-18 之上引入的私有扩展字段、
长任务机制、技能系统与错误码约定。任何客户端读取本文档后即可完整对接
所有扩展能力；不实现这些扩展的客户端仍可正常使用标准 `tools/call` 接口。

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
        "novaai_tasks": true,
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
| novaai_tasks | 客户端支持长任务的 taskId 轮询 |
| novaai_skill | 客户端支持技能系统的 match/get |

未声明时，服务端返回的 instructions 会引导客户端使用 fallback 方式处理。

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

---

## 3. 长任务扩展

### 3.1 任务提交

当工具执行时间可能超过 30 秒时，服务端返回 taskId：

```json
{
  "success": true,
  "code": "OK",
  "taskId": "task-20250618-120000-abc123",
  "status": "running",
  "pollIntervalMs": 1000
}
```

### 3.2 任务轮询

客户端使用 `novaai_task` 工具查询任务状态：

```json
{
  "name": "novaai_task",
  "arguments": {
    "action": "get",
    "taskId": "task-20250618-120000-abc123"
  }
}
```

响应：

```json
{
  "success": true,
  "data": {
    "taskId": "task-20250618-120000-abc123",
    "status": "completed",
    "progress": 1.0,
    "result": { ... },
    "artifacts": [
      {
        "name": "output.zip",
        "path": "/data/adb/novaai-mcp/artifacts/task-xxx/output.zip",
        "size": 1024000
      }
    ]
  }
}
```

### 3.3 任务取消

```json
{
  "name": "novaai_task",
  "arguments": {
    "action": "cancel",
    "taskId": "task-20250618-120000-abc123"
  }
}
```

---

## 4. 技能系统扩展

### 4.1 技能匹配

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
      {
        "id": "screenshot",
        "source": "builtin",
        "score": 0.95
      }
    ]
  }
}
```

### 4.2 技能获取

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

---

## 5. 错误码约定

### 5.1 JSON-RPC 标准错误码

| 错误码 | 含义 |
|--------|------|
| -32700 | JSON 解析失败 |
| -32600 | 无效的请求 |
| -32601 | 方法不存在 |
| -32602 | 无效的参数 |
| -32603 | 内部错误 |

### 5.2 NovaAI 自定义错误码

| 错误码 | 含义 |
|--------|------|
| -32001 | 鉴权失败（含 Host/Origin 校验失败） |
| -32009 | 频率限制 |
| -32010 | 并发超限 |
| -32014 | 会话创建失败 |
| -32015 | 工具不存在 |

### 5.3 工具级错误码

工具返回的 `code` 字段：

| code | 含义 |
|------|------|
| OK | 成功 |
| UNKNOWN_ACTION | 未知操作 |
| MISSING_PARAM | 缺少必填参数 |
| EXEC_FAILED | 命令执行失败 |
| NOT_CONFIRMED | 需要确认 |
| NOT_FOUND | 资源未找到 |

---

## 6. 会话管理

### 6.1 会话创建

会话在首次请求时自动创建，通过 `Mcp-Session-Id` 头传递。

### 6.2 会话状态

```json
{
  "name": "novaai_session_status",
  "arguments": {}
}
```

---

## 7. 审计日志

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