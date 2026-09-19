# NovaAI-MCP 错误码表

本文档列出 NovaAI-MCP 服务端返回的所有错误码及其含义。

---

## 1. JSON-RPC 标准错误码

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32700 | Parse error | JSON 解析失败 |
| -32600 | Invalid Request | 无效的 JSON-RPC 请求 |
| -32601 | Method not found | 请求的方法不存在 |
| -32602 | Invalid params | 参数格式错误 |
| -32603 | Internal error | JSON-RPC 内部错误 |

---

## 2. NovaAI 自定义错误码

### 2.1 认证与会话

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32001 | 鉴权失败 | token 无效或未提供；也用于 Host/Origin 校验失败、LAN 未放行 |
| -32014 | 会话创建失败 | 活跃会话数达到 `session.maxSessions`。只有带 `Mcp-Session-Id` 或含 `initialize` 的请求会占用名额，无状态请求不受影响 |

### 2.2 工具执行

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32015 | 工具不存在 | 请求的工具名未注册 |
| -32602 | Invalid params | `tools/call` 的 params 无法解析 |

> 注意：**工具自身的执行失败不再走 JSON-RPC error**。
> 按 MCP 规范，这类失败放在 `result.isError = true` 里返回，
> 否则严格客户端会把它当成协议故障。详见第 4 节。

### 2.3 权限校验

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32003 | profile 拒绝 | 工具不在当前 profile 的允许范围内，或风险等级超过 `riskCeiling` |

当前请求使用哪个 profile 由 `sessionBinding` 按 token 解析，见
[config.md](config.md) 的 sessionBinding 一节。被拒绝的调用会写入一条
`profile_denied` 审计记录。

### 2.4 频率限制

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32009 | 频率限制 | 触发全局 / 客户端身份 / 工具级令牌桶限流 |
| -32010 | 并发超限 | 同一客户端的并发工具调用数超过 `rateLimit.perSession.maxConcurrentTools` |

第二层限流按**客户端身份**计数：服务端对本次请求携带的 token 取 `sha256`
作为键（无 token 的入口共用一个 `local` 桶）。**不是**按 `Mcp-Session-Id`
计数——那个头由客户端自行携带，换一个或不带就能拿到一个全新的满额桶。
`perSession` 这个名字是历史遗留，语义见 [config.md](config.md) 的 rateLimit 一节。

---

## 3. 工具级错误码

工具返回的 `code` 字段（非 JSON-RPC 错误码）：

### 3.1 通用错误码

| code | 含义 |
|------|------|
| OK | 成功 |
| UNKNOWN_ACTION | 未知的操作类型 |
| MISSING_PARAM | 缺少必填参数 |
| INVALID_JSON | JSON 格式错误 |
| NOT_FOUND | 资源未找到 |
| NOT_CONFIRMED | 需要 confirmDangerous: true |
| NOT_IMPLEMENTED | 功能未实现 |

### 3.2 文件系统错误码

| code | 含义 |
|------|------|
| STAT_FAILED | 获取文件信息失败 |
| LIST_FAILED | 列出目录失败 |
| OPEN_FAILED | 打开文件失败 |
| READ_FAILED | 读取文件失败 |
| WRITE_FAILED | 写入文件失败 |
| COPY_FAILED | 复制文件失败 |
| MOVE_FAILED | 移动文件失败 |
| REMOVE_FAILED | 删除文件失败 |
| MKDIR_FAILED | 创建目录失败 |
| CHMOD_FAILED | 修改权限失败 |
| CHOWN_FAILED | 修改所有者失败 |
| SYMLINK_FAILED | 创建符号链接失败 |
| HARDLINK_FAILED | 创建硬链接失败 |
| CHCON_FAILED | 修改 SELinux context 失败 |
| INVALID_MODE | 无效的权限模式 |
| INVALID_BASE64 | 无效的 Base64 编码 |
| EXISTS | 文件已存在 |

### 3.3 命令执行错误码

| code | 含义 |
|------|------|
| EXEC_FAILED | 命令执行失败 |
| MISSING_COMMAND | 缺少 command 参数 |
| MISSING_SCRIPT | 缺少 script 参数 |
| SYNTAX_ERROR | 脚本语法错误 |
| INVALID_UID | 无效的 UID |
| 命令超时 | 命令执行超时 |

### 3.4 应用管理错误码

| code | 含义 |
|------|------|
| LIST_FAILED | 列出应用失败 |
| INFO_FAILED | 获取应用信息失败 |
| INSTALL_FAILED | 安装应用失败 |
| PATH_FAILED | 获取应用路径失败 |
| MISSING_PATH | 缺少路径参数 |

### 3.5 归档错误码

| code | 含义 |
|------|------|
| NO_7Z | 未找到 7z 命令 |
| ARCHIVE_FAILED | 归档操作失败 |
| MISSING_DEST | 缺少目标路径 |

### 3.6 下载错误码

| code | 含义 |
|------|------|
| DOWNLOAD_FAILED | 下载失败 |
| SHA256_MISMATCH | SHA-256 校验不匹配 |
| SIZE_MISMATCH | 文件大小不匹配 |

### 3.7 备份错误码

| code | 含义 |
|------|------|
| BACKUP_FAILED | 备份失败 |
| RESTORE_FAILED | 恢复失败 |
| HASH_FAILED | 计算哈希失败 |

### 3.8 Root 模块错误码

| code | 含义 |
|------|------|
| UNSUPPORTED | 当前框架不支持 |
| INSTALL_FAILED | 模块安装失败 |

### 3.9 系统操作错误码

| code | 含义 |
|------|------|
| MISSING_PID | 缺少进程 ID |
| KILL_FAILED | 发送信号失败 |
| RENICE_FAILED | 调整优先级失败 |
| SETPROP_FAILED | 设置属性失败 |
| MISSING_KEY | 缺少属性名 |
| MISSING_CONTEXT | 缺少 SELinux context |

### 3.10 屏幕操作错误码

| code | 含义 |
|------|------|
| SCREENCAP_FAILED | 截屏失败 |
| INPUT_FAILED | 输入操作失败 |

### 3.11 无障碍服务错误码

| code | 含义 |
|------|------|
| ENABLE_FAILED | 启用服务失败 |
| DISABLE_FAILED | 禁用服务失败 |

---

## 4. 错误响应示例

### 4.1 JSON-RPC 错误（协议层）

用于请求本身不合法的情况：解析失败、方法不存在、工具名未注册、
鉴权失败、限流拒绝。此时**没有** `result` 字段。

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32001,
    "message": "鉴权失败"
  }
}
```

### 4.2 工具执行失败（结果层）

工具内部报错时，仍然返回正常的 `result`，但 `isError` 为 `true`，
原因放在 `content[0].text`：

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      { "type": "text", "text": "工具 novaai_shell 内部 panic: runtime error: index out of range" }
    ],
    "isError": true
  }
}
```

### 4.3 工具业务失败（成功返回，但业务不成功）

参数非法、资源不存在这类"可预期的业务失败"由工具自己表达：
JSON-RPC 层面是成功，`content[0].text` 是结果的 JSON 序列化，
其中 `success: false`：

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\n  \"code\": \"NOT_FOUND\",\n  \"message\": \"文件不存在\",\n  \"success\": false\n}"
      }
    ],
    "structuredContent": {
      "success": false,
      "code": "NOT_FOUND",
      "message": "文件不存在"
    }
  }
}
```

判断顺序建议：先看 `error` 是否存在 → 再看 `result.isError` →
最后看 `content[0].text` 里的 `success`。