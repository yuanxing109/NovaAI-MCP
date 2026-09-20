# NovaAI-MCP 配置文件说明

配置文件路径: `/data/adb/novaai-mcp/config.json`

> **本节是这份契约的唯一定义处。** 字段的权威来源是 `internal/config/types.go`
> 与 `internal/config/default.go`；一份逐键比对的测试（`config/example_test.go`）
> 断言下面的 JSON 与结构体的 json tag **完全一致**、取值与 `Default()` **完全一致**，
> 任一侧增删字段或改值都会立刻失败。

---

## 完整配置（可直接复制）

下面的内容与全新安装生成的 `config.json` 等价。

```json
{
  "stateDir": "/data/adb/novaai-mcp",
  "listen": "0.0.0.0:5322",
  "unixSocket": "/data/adb/novaai-mcp/mcp.sock",
  "profile": "default",

  "limits": {
    "globalQps": 50,
    "shellQps": 10,
    "maxConcurrent": 5
  },

  "audit": {
    "enabled": true,
    "retentionDays": 7,
    "maxFileBytes": 10485760
  },

  "shellTimeoutSeconds": 60,
  "resultPreviewBytes": 1048576,

  "upstreams": []
}
```

就这么多。**没有鉴权配置，没有 LAN 开关，没有多档位。**

- **局域网直连**：默认 `listen` 就是 `0.0.0.0:5322`，同网段设备直接访问。
- **切回本地模式**：把 `listen` 改成 `127.0.0.1:5322` 即可，**无需改代码**。
- **关掉限流**：把 `limits` 里对应的 qps 设为 `0` 表示该层不限流，
  `maxConcurrent: 0` 表示不限并发。
- **关掉审计**：`audit.enabled: false`。
- **接上游 MCP**：往 `upstreams` 数组里加条目，或用 KernelSU WebUI 加
  （WebUI 就是改这个数组）。字段与语义见 [upstream.md](upstream.md)。

权限边界与残余风险见 [security.md](security.md)。

---

## 配置结构

```json
{
  "stateDir": "string",
  "listen": "string",
  "unixSocket": "string",
  "profile": "string",
  "limits": {
    "globalQps": 0,
    "shellQps": 0,
    "maxConcurrent": 0
  },
  "audit": {
    "enabled": true,
    "retentionDays": 0,
    "maxFileBytes": 0
  },
  "shellTimeoutSeconds": 0,
  "resultPreviewBytes": 0,
  "upstreams": []
}
```

---

## 字段详细说明

### stateDir

| 类型 | 默认值 | 说明 |
|------|--------|------|
| 字符串 | `/data/adb/novaai-mcp` | 状态目录 |

子目录不再单独配置：`stateDir` 就是那个旋钮。`workspace/` 与 `audit/` 由它
派生（`{stateDir}/workspace`、`{stateDir}/audit`）；`crash/` 同样固定为
`{stateDir}/crash`，不可配置 —— 崩溃处理器必须在配置加载**之前**装好，
一个只能在配置就绪后才可能生效的字段等于没有这个字段。

整个 `stateDir` 受 `pathguard` 保护（`config.json` / `audit/` 不能被通用工具
改写），只有 `workspace` `tmp` `downloads` `uploads` `artifacts` `backups`
`schedules` `skills` 这几个子树是 agent 的合法工作区。

### listen

| 类型 | 默认值 | 说明 |
|------|--------|------|
| 字符串 | `0.0.0.0:5322` | TCP 监听地址（`host:port`） |

**这是唯一的入口开关。** 主机部分必须是 IP 字面量或 `localhost`，
否则启动校验直接拒绝。

| 值 | 效果 |
|----|------|
| `0.0.0.0:5322` | 局域网直连（默认） |
| `127.0.0.1:5322` | 仅本机 / 数据线（`adb forward`） |

运行时也可以改：`novaai_config` 的 `update` action 允许修改包括 `listen`
在内的任意配置字段。监听地址属于运行时配置，不属于 `pathguard` 的保护范围 ——
这是有意为之，详见 [security.md](security.md)。

### unixSocket

| 类型 | 默认值 | 说明 |
|------|--------|------|
| 字符串 | `{stateDir}/mcp.sock` | Unix socket 路径 |

权限固定为 `0660`（未 `chgrp`，仅 root 可连），并尝试注入 SELinux 标签。
设为空串可关闭。

### profile

| 类型 | 默认值 | 说明 |
|------|--------|------|
| 字符串 | `default` | 权限档位名 |

**目前只能写 `default`。** 写别的值会在启动校验被拒绝。

档位定义在 `internal/config/profiles.go`，当前只有一种形状：

```json
{ "allowTools": ["*"], "denyTools": [], "riskCeiling": 3 }
```

即放行全部工具、无黑名单、风险上限为最高级 3。这是刻意的：本服务面向
单用户自有设备，"装完即用、含 shell"是明确诉求；权限边界落在
**网络可达性**上，而不是档位。

> `tools/call` 的闸门仍然存在（工具存在性 → 档位判定 → 限流 → 执行），
> 只是当前档位恒为"全放行"。要收窄时改这一处即可。

### limits

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| globalQps | 数字 | 50 | 全局限流（所有工具共享） |
| shellQps | 数字 | 10 | `novaai_shell` / `novaai_script` 的独立限流 |
| maxConcurrent | 整数 | 5 | 同时在执行的工具调用上限；`0` 表示不限 |

`qps <= 0` 表示该层不限流。

> 没有"按客户端身份"的层：所有来源本来就是同一个身份（无 token、
> 不分来源），多一个维度只是多一处可被误读的状态。

### audit

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| enabled | 布尔 | true | 是否启用审计 |
| retentionDays | 整数 | 7 | 日志保留天数 |
| maxFileBytes | 整数 | 10485760 | 单个日志文件上限 (10MB)，超过则轮转 |

审计为 JSONL，按日命名（`audit-YYYY-MM-DD.jsonl`），文件权限 `0600`、
目录 `0700`。同一天内超过单文件上限时加序号后缀（`audit-2026-09-20.1.jsonl`）。

参数按固定白名单脱敏：`action` `path` `package` `name` `query` `url`
`tool` `pattern` `cmd` `command` 原样记录（截断到 256 字节），其余参数
一律记为 `***`。**没有可关闭脱敏的开关。**

记录的事件：`initialize` `tool_call` `rate_limited` `concurrency_limited`
`host_rejected` `origin_rejected` `session_create` `session_close`
`session_idle_expire`。

### shellTimeoutSeconds

| 类型 | 默认值 | 说明 |
|------|--------|------|
| 整数 | 60 | shell 系工具未指定 `timeoutMs` 时的默认超时 |

这是默认超时的**唯一来源**（`internal/tools/v02/helpers.go` 的
`shellTimeout`）。超时会杀掉整个进程组，不留子进程。

### resultPreviewBytes

| 类型 | 默认值 | 说明 |
|------|--------|------|
| 整数 | 1048576 | 单个工具结果的字节上限 (1MB)；`0` 表示不限制 |

超过上限时截断 `content`（不切断 UTF-8 字符）、追加一行截断说明，
并**丢弃 `structuredContent`** —— 只截 `content` 而保留完整的结构化副本，
帧大小一点没省，还会让两者不一致。

### upstreams

| 类型 | 默认值 | 说明 |
|------|--------|------|
| 数组 | `[]` | 上游 MCP 服务列表 |

每一项描述一个上游 MCP 服务；它的工具会以 `{name}__{tool}` 合并进本服务的
`tools/list`。**默认空数组**：装完即用不需要任何上游。

推荐用 KernelSU WebUI 增删（它做的就是原子改写这一个数组）。手动写时字段
语义、状态模型、路由规则、启动配置见 **[upstream.md](upstream.md)**。

> 上游**状态**（running / stopped / error / disabled）不落盘，每次启动重新
> 探测。所以这里只有配置，没有状态字段。

---

## 固定的行为（不是配置项）

| 行为 | 值 | 位置 |
|------|----|------|
| 请求体上限 | 64 MiB（超限返回 `-32600`） | `config.MaxRequestBytes` |
| 优雅关闭等待 | 30 秒 | `config.ShutdownGraceSec` |
| 会话空闲超时 | 30 分钟 | `internal/session` |
| 最大会话数 | 32 | `internal/session` |
| socket 文件权限 | `0660` | `internal/shutdown` |
| 参数脱敏白名单 | 见上 | `internal/audit/redact.go` |

它们被刻意排除在配置之外：单用户自有设备场景下，多一个旋钮只多一处
需要同步的心智负担。需要改就直接改常量。

---

## 非法配置

`config.Validate` 会在启动时拒绝：

| 情况 | 错误 |
|------|------|
| `stateDir` 为空 | `stateDir 不能为空` |
| `listen` 不是 `host:port` / 端口越界 / 主机是域名 | `listen ...` |
| `profile` 不是 `default` | `profile 只能是 "default"` |
| `shellTimeoutSeconds <= 0` | `shellTimeoutSeconds 必须 > 0` |
| `resultPreviewBytes < 0` | `resultPreviewBytes 不能为负` |
| 上游 `name` 为空 / 含非法字符 / **含 `__`** / 重复 | `upstreams[N] (...).name ...` |
| 上游 `type` 不是 `http` / `stdio` | `upstreams[N] (...).type 只能是 ...` |
| http 上游缺 `url`，或 url 不是 http(s):// | `upstreams[N] (...).url ...` |
| stdio 上游缺 `command` | `upstreams[N] (...).command ...` |
| `riskCeiling` 不在 0..3 | `upstreams[N] (...).riskCeiling ...` |
| `denyTools` 含非法工具名 | `upstreams[N] (...).denyTools 含非法工具名 ...` |
| `launch` 配置不完整（intent 缺 package、缺 action/activity；command 缺 command） | `upstreams[N] (...).launch ...` |

上游校验在**启动时**就把关，不留到运行时：`name` 是工具名前缀，一个写错的
名字会让一整批工具出现在 `tools/list` 里却永远路由不到。

旧配置里的未知键（`security`、`network`、`profiles`、`sessionBinding`、
`rateLimit`、`session`、`paths`、`skill`、`capabilities`…）会被
`json.Unmarshal` **静默忽略**，不需要手工清理。详见 [migration.md](migration.md)。

> 唯一例外是 `uninstall` 段：它**不被 Go 读取，但被 `uninstall.sh` 用 `grep`
> 读取**（`purgeInternalState` / `purgeAuditLogs` / `purgeCrashDumps` /
> `purgeUserData`，默认全 `false` 即保留数据）。想控制卸载行为就必须手工
> 写这一段 —— 它是本仓库里唯一"配置键不在 `Config` 结构体里"的合法存在。
