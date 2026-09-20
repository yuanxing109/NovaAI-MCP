# NovaAI-MCP 错误码表

本文档列出 NovaAI-MCP 服务端返回的所有错误码及其含义。

> 本表按**当前代码**枚举（`errFail("...")` 的全部取值 + JSON-RPC 层错误码）。
> 随工具精简删除的错误码（`NOT_CONFIRMED`、`UNSAFE_ARCHIVE`、
> `BACKUP_FAILED`、`RESTORE_FAILED`、`SETPROP_FAILED`、`MISSING_KEY`、
> `ENABLE_FAILED`、`DISABLE_FAILED`、`NOT_IMPLEMENTED`）已从表中移除。

---

## 1. JSON-RPC 标准错误码

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32700 | Parse error | JSON 解析失败 |
| -32600 | Invalid Request | 无效的 JSON-RPC 请求；也用于请求体超过 64 MiB |
| -32601 | Method not found | 请求的方法不存在 |
| -32602 | Invalid params | 参数格式错误（`tools/call` 的 params 无法解析） |
| -32603 | Internal error | JSON-RPC 内部错误 |

---

## 2. NovaAI 自定义错误码

### 2.1 请求准入

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32001 | Host / Origin 校验失败 | Host 不是 IP 字面量或 `localhost`，或请求带 `Origin` 头。**本服务不鉴权**，所以这不再是"鉴权失败" |
| -32014 | 会话创建失败 | 活跃会话数达到上限 32。只有带有效 `Mcp-Session-Id` 或含 `initialize` 的请求会占用名额，无状态请求不受影响 |

### 2.2 工具执行

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32015 | 工具不存在 | 请求的工具名未注册，且前缀对不上任何已注册上游 |
| -32003 | 档位拒绝 | 工具不在档位允许范围内，或风险等级超过上限。当前只有 `default` 档位且放行全部本地工具，**理论不可达**；被拒时会写入 `profile_denied` 审计 |

> 注意：**工具自身的执行失败不走 JSON-RPC error**。
> 按 MCP 规范，这类失败放在 `result.isError = true` 里返回，
> 否则严格客户端会把它当成协议故障。上游转发失败同理。详见第 4 节。

### 2.3 频率限制

| 错误码 | 含义 | 说明 |
|--------|------|------|
| -32009 | 频率限制 | 触发全局限流或 shell 系工具限流 |
| -32010 | 并发超限 | 同时在执行的工具调用数超过 `limits.maxConcurrent` |

只有**三层**：全局（`limits.globalQps`）、shell 系（`limits.shellQps`，
只覆盖 `novaai_shell` / `novaai_script`）、并发槽（`limits.maxConcurrent`）。

> 早期版本还有一层"按客户端身份"的限流，键取 token 的 `sha256`。
> 该层已整层删除：本服务无 token、不分来源，所有调用方本来就是同一个身份，
> 多一个维度只是多一处可被误读的状态。

---

## 3. 工具级错误码

工具返回的 `code` 字段（非 JSON-RPC 错误码）。

**`code` 是英文的，也是不变的。** 它给程序匹配用，改了就是破约。
给人和 AI 看的中文名单独放在 `codeName` 里：

```json
{
  "success": false,
  "code": "PROTECTED_PATH",
  "codeName": "路径受保护",
  "message": "路径受保护（/system）：系统分区"
}
```

- `code` —— 稳定的英文标识符，**程序按它分支**；
- `codeName` —— 稳定的中文短标签，**回答"这是哪一类错"**；
- `message` —— 一次性的详细原因，带上具体路径/参数。

数字错误码（`-32015` 这类，见第 1、2 节）**不参与这套机制** ——
它们来自 JSON-RPC 2.0 与 MCP 生态的约定，换了客户端就不认了。
本表由 `src/internal/tools/v02/codes.go` 的 `codeNames` 唯一声明，
`codes_test.go` 会机械比对（本表的 code 与中文名必须与它完全一致）。

### 3.1 通用

| code | 中文名 | 说明 |
|------|--------|------|
| `OK` | 成功 | **会出现**在成功结果里（不是省略）。注意 `success` 与 `code` 不同义：`novaai_shell` 的退出码非 0 时是 `success:false` + `code:"OK"` —— 前者说"命令没跑通"，后者说"这次工具调用本身没出错" |
| `UNKNOWN_ACTION` | 未知操作 | `action` 不在该工具声明的枚举里 |
| `NOT_FOUND` | 资源不存在 | 文件/模块/上游等目标不存在 |
| `EXISTS` | 已存在 | 目标已存在且没有覆盖意图 |
| `UNSUPPORTED` | 框架不支持 | 当前 Root 框架不支持该操作（如模块安装） |

### 3.2 参数

| code | 中文名 | 说明 |
|------|--------|------|
| `MISSING_PARAM` | 缺少参数 | 缺少必填参数（通用） |
| `MISSING_PATH` | 缺少路径 | 缺少 `path` |
| `MISSING_PID` | 缺少进程号 | 缺少 `pid` |
| `MISSING_COMMAND` | 缺少命令 | 缺少 `command` |
| `MISSING_SCRIPT` | 缺少脚本 | 缺少 `script` |
| `MISSING_CONFIG` | 缺少配置 | 缺少 `config` |
| `MISSING_DEST` | 缺少目标路径 | 缺少 `destination` |
| `MISSING_SOURCE` | 缺少来源路径 | 缺少 `source` |
| `MISSING_TARGET` | 缺少链接目标 | 缺少 `target` |
| `MISSING_CONTEXT` | 缺少 SELinux 标签 | 缺少 `context` |
| `INVALID_PARAM` | 参数非法 | 取值非法（如非法 `moduleId`、非法按键名） |
| `INVALID_JSON` | JSON 非法 | 请求里的 JSON 片段解析失败 |
| `INVALID_CONFIG` | 配置非法 | 配置未通过 `config.Validate`（`update` / `reload_upstreams`）。重载失败时**保留原有上游**，不会半途换掉 |
| `INVALID_MODE` | 权限模式非法 | `mode` 不是合法的八进制权限 |
| `INVALID_UID` | UID 非法 | `uid` 缺失或 ≤ 0 |
| `INVALID_BASE64` | Base64 非法 | 声明 `encoding: base64` 但内容解码失败 |

### 3.3 路径保护

| code | 中文名 | 说明 |
|------|--------|------|
| `PROTECTED_PATH` | 路径受保护 | 目标落在受保护位置：分区、`/data/adb/modules`、模块自身 `stateDir`，以及**硬拒绝**的 `/sdcard/Android/{data,obb}`。无确认放行通道 |

### 3.4 文件系统

| code | 中文名 | 说明 |
|------|--------|------|
| `STAT_FAILED` | 读取元数据失败 | `stat` 失败 |
| `LIST_FAILED` | 列举失败 | 列目录 / 列应用失败 |
| `OPEN_FAILED` | 打开失败 | 无法打开文件 |
| `READ_FAILED` | 读取失败 | 读内容失败 |
| `WRITE_FAILED` | 写入失败 | 写内容失败 |
| `TRUNCATE_FAILED` | 截断失败 | 截断到指定长度失败 |
| `TOUCH_FAILED` | 更新时间戳失败 | `touch` 失败 |
| `SEEK_FAILED` | 定位失败 | 偏移定位失败 |
| `COPY_FAILED` | 复制失败 | 复制失败 |
| `MOVE_FAILED` | 移动失败 | 移动/重命名失败 |
| `REMOVE_FAILED` | 删除失败 | 删除失败 |
| `MKDIR_FAILED` | 建目录失败 | 建目录失败 |
| `CHMOD_FAILED` | 改权限失败 | 改权限失败 |
| `CHOWN_FAILED` | 改属主失败 | 改属主失败 |
| `CHCON_FAILED` | 改 SELinux 标签失败 | `chcon` 失败 |
| `SYMLINK_FAILED` | 建符号链接失败 | 建符号链接失败 |
| `HARDLINK_FAILED` | 建硬链接失败 | 建硬链接失败 |
| `HASH_FAILED` | 算哈希失败 | 计算哈希失败 |

### 3.5 命令执行

| code | 中文名 | 说明 |
|------|--------|------|
| `EXEC_FAILED` | 执行失败 | 命令执行失败（含超时；超时会杀整个进程组） |
| `SYNTAX_ERROR` | 脚本语法错误 | 脚本预检失败 |
| `KILL_FAILED` | 发信号失败 | 发送信号失败 |
| `RENICE_FAILED` | 调优先级失败 | 调整优先级失败 |

### 3.6 应用管理

| code | 中文名 | 说明 |
|------|--------|------|
| `INFO_FAILED` | 取信息失败 | 读取应用信息失败 |
| `INSTALL_FAILED` | 安装失败 | `pm install` / 模块安装失败 |

### 3.7 归档与传输

| code | 中文名 | 说明 |
|------|--------|------|
| `NO_7Z` | 找不到 7z | 设备上没有可用的 7z |
| `ARCHIVE_FAILED` | 归档失败 | 压缩/解压/列出/校验失败 |
| `CREATE_FAILED` | 创建上传失败 | 创建分块上传会话失败 |
| `EXPORT_FAILED` | 导出失败 | 导出失败 |

### 3.8 下载

| code | 中文名 | 说明 |
|------|--------|------|
| `DOWNLOAD_FAILED` | 下载失败 | 下载失败 |
| `SHA256_MISMATCH` | SHA-256 不匹配 | 校验和不符 |
| `SIZE_MISMATCH` | 大小不匹配 | 字节数不符 |

### 3.9 Root 与模块

| code | 中文名 | 说明 |
|------|--------|------|
| `MOUNT_FAILED` | 挂载失败 | Systemless 挂载失败 |
| `UMOUNT_FAILED` | 卸载失败 | Systemless 卸载失败 |

### 3.10 屏幕与输入

| code | 中文名 | 说明 |
|------|--------|------|
| `SCREENCAP_FAILED` | 截屏失败 | 截屏/录屏失败 |
| `INPUT_FAILED` | 输入失败 | `input` 注入失败 |

### 3.11 上游 MCP 聚合

| code | 中文名 | 说明 |
|------|--------|------|
| `UPSTREAM_UNAVAILABLE` | 上游聚合未启用 | 服务未装配上游注册表 |
| `UPSTREAM_PROBE_FAILED` | 探测上游失败 | 探测指定上游失败 |
| `UPSTREAM_RESTART_FAILED` | 重启上游失败 | 重启指定上游失败 |

> **上游转发失败不走这里**，也不走 JSON-RPC error。它是
> `result.isError = true` + `content[0].text` 里的一句原因，例如
> `upstream fake 未运行（stopped），请先手动启动`。
>
> 唯一走 JSON-RPC 的是**工具名不存在**：前缀对不上任何已注册上游时返回
> `-32015`。`fake__`（前缀对、工具名为空）也算这一档。

---

## 4. 错误响应示例

### 4.1 JSON-RPC 错误（协议层）

用于请求本身不合法的情况：解析失败、方法不存在、工具名未注册、
Host/Origin 校验失败、限流拒绝。此时**没有** `result` 字段。

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32001,
    "message": "Host 头不被信任"
  }
}
```

### 4.2 工具执行失败（结果层）

工具内部报错（panic 被 `safeCall` 兜住）时，仍然返回正常的 `result`，
但 `isError` 为 `true`，原因放在 `content[0].text`：

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      { "type": "text", "text": "工具 novaai_fs_manage 内部 panic: runtime error: index out of range" }
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
        "text": "{\n  \"code\": \"NOT_FOUND\",\n  \"codeName\": \"资源不存在\",\n  \"message\": \"文件不存在\",\n  \"success\": false\n}"
      }
    ],
    "structuredContent": {
      "success": false,
      "code": "NOT_FOUND",
      "codeName": "资源不存在",
      "message": "文件不存在"
    }
  }
}
```

> 结果超过 `resultPreviewBytes` 时 `structuredContent` 会被丢弃，
> 此时只有 `content`。见 [extensions.md](extensions.md) 第 4.3 节。

判断顺序建议：先看 `error` 是否存在 → 再看 `result.isError` →
最后看 `content[0].text` 里的 `success`。分支一律按 `code`（英文），
**不要按 `codeName` 或 `message`** —— 后两者是给人看的。
