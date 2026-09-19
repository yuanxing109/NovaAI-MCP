# NovaAI-MCP 配置文件说明

配置文件路径: `/data/adb/novaai-mcp/config.json`

> **本节是这份契约的唯一定义处。** 此前另有一份 `docs/config.example.json`，
> 它是同一份契约的第二个 owner —— 没有任何测试能保证它与 `internal/config`
> 同步，两份必然漂移。现已合并到这里。
>
> 字段的权威来源是 `internal/config/types.go` 与 `internal/config/default.go`；
> 一份逐键比对的测试（`config/example_test.go`）断言下面的 JSON 与结构体的
> json tag 完全一致，任一侧增删字段都会立刻失败。

---

## 完整配置（可直接复制）

下面的内容与全新安装生成的 `config.json` 等价。`value` 留空是因为
首次启动会随机生成并写入 `{stateDir}/token`。

```json
{
  "schemaVersion": 3,

  "network": {
    "port": 5322,
    "listenLoopback": true,
    "listenLan": false,
    "allowedOrigins": []
  },

  "paths": {
    "stateDir": "/data/adb/novaai-mcp",
    "workDir": "/storage/emulated/0/novaaiAI",
    "workspaceRoot": "/data/adb/novaai-mcp/workspace",
    "auditDir": "/data/adb/novaai-mcp/audit"
  },

  "limits": {
    "maxRequestBytes": 67108864,
    "shellTimeoutSeconds": 60,
    "resultPreviewBytes": 1048576,
    "shutdownGraceSeconds": 30
  },

  "security": {
    "anonymous": false,
    "validateHost": true,
    "validateOrigin": true,
    "allowCors": false,

    "token": {
      "enabled": true,
      "value": "",
      "rotateOnStart": false,
      "allowQueryParam": false
    },

    "unixSocket": {
      "enabled": true,
      "path": "/data/adb/novaai-mcp/mcp.sock",
      "mode": "0660",
      "sepolicyInject": true
    },

    "lan": {
      "enabled": false,
      "allowedCidr": ["192.168.0.0/16", "10.0.0.0/8", "172.16.0.0/12"]
    }
  },

  "profiles": {
    "default": {
      "allowTools": ["*"],
      "denyTools": ["novaai_shell", "novaai_script", "novaai_schedule",
                    "novaai_config", "novaai_root_module", "novaai_systemless"],
      "riskCeiling": 3
    },
    "readonly": {
      "allowTools": ["novaai_status", "novaai_capabilities", "novaai_health_status",
                     "novaai_root_info", "novaai_device_info", "novaai_fs_info",
                     "novaai_fs_read", "novaai_fs_search", "novaai_fs_hash",
                     "novaai_app_list", "novaai_app_info", "novaai_process",
                     "novaai_log", "novaai_skill", "novaai_diagnostics",
                     "novaai_session_status", "novaai_session_list",
                     "novaai_audit_status", "novaai_auth_status"],
      "denyTools": [],
      "riskCeiling": 0
    },
    "reverse": {
      "allowTools": ["novaai_reverse_*", "novaai_hook_*", "novaai_fs_read",
                     "novaai_fs_info", "novaai_app_info", "novaai_app_list",
                     "novaai_process", "novaai_log", "novaai_device_info",
                     "novaai_status", "novaai_session_*"],
      "denyTools": ["novaai_shell", "novaai_script", "novaai_schedule",
                    "novaai_power", "novaai_root_module"],
      "riskCeiling": 3
    },
    "agent_full": {
      "allowTools": ["*"],
      "denyTools": [],
      "riskCeiling": 3
    }
  },

  "sessionBinding": {
    "byTokenHash": {},
    "fallback": "default"
  },

  "audit": {
    "enabled": true,
    "maxFileBytes": 10485760,
    "maxFiles": 20,
    "retentionDays": 30,
    "includeArgs": true,
    "argPreviewBytes": 256,
    "allowlistFields": ["action", "path", "package", "name", "query",
                        "url", "tool", "pattern", "cmd", "command"]
  },

  "rateLimit": {
    "global": { "qps": 50, "burst": 100 },
    "perSession": { "qps": 20, "burst": 40, "maxConcurrentTools": 5 },
    "perTool": {
      "novaai_log":     { "qps": 2,  "burst": 4 },
      "novaai_network": { "qps": 5,  "burst": 10 },
      "novaai_shell":   { "qps": 10, "burst": 20 }
    }
  },

  "session": {
    "idleTimeoutSeconds": 1800,
    "maxSessions": 32,
    "sweepIntervalSeconds": 300
  },

  "uninstall": {
    "purgeInternalState": false,
    "purgeAuditLogs": false,
    "purgeCrashDumps": false,
    "purgeUserData": false
  }
}
```

**关掉限流**：把 `rateLimit` 各层的 `qps` 设为 `0`。`qps <= 0` 表示"该层
不限流"，`maxConcurrentTools: 0` 表示不限并发 —— 不需要删代码。

**本机免 token**：`security.anonymous: true`。它**只对 loopback 生效**，
局域网访问始终强制 token（`lan.enabled` 与 `token.enabled` 的组合由启动
校验强制）。

**会被拒绝启动的组合**见 [security.md](security.md#启动时的组合校验)。

---

## 配置结构

```json
{
  "schemaVersion": 3,
  "network": { ... },
  "paths": { ... },
  "limits": { ... },
  "security": { ... },
  "profiles": { ... },
  "sessionBinding": { ... },
  "audit": { ... },
  "rateLimit": { ... },
  "session": { ... },
  "uninstall": { ... }
}
```

---

## 字段详细说明

### schemaVersion
- **类型**: 整数
- **默认值**: 3
- **说明**: 配置文件格式版本，不要手动修改

---

### network (网络配置)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| port | 整数 | 5322 | MCP 服务监听端口 |
| listenLoopback | 布尔 | true | 是否监听本地回环地址 (127.0.0.1) |
| listenLan | 布尔 | false | 是否监听局域网地址 (0.0.0.0) |
| allowedOrigins | 字符串数组 | [] | 允许的 CORS 来源 |

**示例**:
```json
"network": {
  "port": 5322,
  "listenLoopback": true,
  "listenLan": false,
  "allowedOrigins": []
}
```

---

### paths (路径配置)

子目录（downloads / uploads / artifacts / tmp）不再单独配置：`stateDir` 就是那个旋钮。
它们仍会作为 `stateDir` 下的固定子目录被创建，并由 `pathguard` 视为可写子树。

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| stateDir | 字符串 | /data/adb/novaai-mcp | 状态目录（配置、日志等） |
| workDir | 字符串 | /storage/emulated/0/novaaiAI | 工作目录（用户数据） |
| workspaceRoot | 字符串 | {stateDir}/workspace | 工作空间根目录（相对路径的解析基准） |
| auditDir | 字符串 | {stateDir}/audit | 审计日志目录 |

崩溃转储目录固定为 `{stateDir}/crash`，不可配置：崩溃处理器必须在配置加载
**之前**装好，否则配置解析阶段自身的 panic 没有兜底 —— 一个只能在配置就绪后
才可能生效的字段等于没有这个字段。

**示例**:
```json
"paths": {
  "stateDir": "/data/adb/novaai-mcp",
  "workDir": "/storage/emulated/0/novaaiAI",
  "workspaceRoot": "/data/adb/novaai-mcp/workspace",
  "auditDir": "/data/adb/novaai-mcp/audit"
}
```

---

### limits (限制配置)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| maxRequestBytes | 整数 | 67108864 | 最大请求体大小 (64MB)，超限返回 `-32600` |
| shellTimeoutSeconds | 整数 | 60 | `novaai_shell` / `novaai_script` 未显式指定 `timeoutMs` 时的默认超时 |
| resultPreviewBytes | 整数 | 1048576 | 单个工具结果的字节上限 (1MB)，超限截断并丢弃 `structuredContent` |
| shutdownGraceSeconds | 整数 | 30 | 优雅关闭等待时间 |

**示例**:
```json
"limits": {
  "maxRequestBytes": 67108864,
  "shellTimeoutSeconds": 60,
  "resultPreviewBytes": 1048576,
  "shutdownGraceSeconds": 30
}
```

---

### security (安全配置)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| anonymous | 布尔 | false | 是否允许匿名访问 |
| validateHost | 布尔 | true | 是否验证 Host 头 |
| validateOrigin | 布尔 | true | 是否验证 Origin 头 |
| allowCors | 布尔 | false | 是否允许 CORS |

**示例**:
```json
"security": {
  "anonymous": false,
  "validateHost": true,
  "validateOrigin": true,
  "allowCors": false
}
```

---

### security.token (Token 认证)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| enabled | 布尔 | true | 是否启用 Token 认证 |
| value | 字符串 | (自动生成) | Token 值 |
| rotateOnStart | 布尔 | false | 启动时是否轮换 Token |
| allowQueryParam | 布尔 | false | 是否允许通过 URL 参数传递 Token |

**示例**:
```json
"token": {
  "enabled": true,
  "value": "your-secret-token-here",
  "rotateOnStart": false,
  "allowQueryParam": false
}
```

---

### security.unixSocket (Unix Socket)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| enabled | 布尔 | true | 是否启用 Unix Socket |
| path | 字符串 | {stateDir}/mcp.sock | Socket 文件路径 |
| mode | 字符串 | 0660 | Socket 文件权限 |
| sepolicyInject | 布尔 | true | 是否注入 SELinux 策略 |

**示例**:
```json
"unixSocket": {
  "enabled": true,
  "path": "/data/adb/novaai-mcp/mcp.sock",
  "mode": "0660",
  "sepolicyInject": true
}
```

---

### security.lan (局域网访问)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| enabled | 布尔 | false | 是否允许局域网访问 |
| allowedCidr | 字符串数组 | ["192.168.0.0/16", "10.0.0.0/8", "172.16.0.0/12"] | 允许的 IP 段 |

**示例**:
```json
"lan": {
  "enabled": false,
  "allowedCidr": ["192.168.0.0/16", "10.0.0.0/8"]
}
```

---

### profiles (权限配置)

每个 profile 定义了一组允许/拒绝的工具列表。**profile 在每次 `tools/call` 时强制校验**：
先按 `denyTools` 拒绝，再按 `allowTools` 允许，同时要求工具风险等级不超过 `riskCeiling`。
校验失败返回 JSON-RPC 错误 `-32003`，并写入一条 `profile_denied` 审计记录。

具体使用哪个 profile，由 [sessionBinding](#sessionbinding-会话绑定) 决定。

| 字段 | 类型 | 说明 |
|------|------|------|
| allowTools | 字符串数组 | 允许的工具列表，支持 glob（`*` 表示全部） |
| denyTools | 字符串数组 | 拒绝的工具列表，优先级高于 allowTools |
| riskCeiling | 整数 | 允许的最高风险等级 (0-3) |

**风险等级**:
- 0: 只读操作
- 1: 普通写操作
- 2: 修改设备状态
- 3: 破坏性操作

**示例**:
```json
"profiles": {
  "default": {
    "allowTools": ["*"],
    "denyTools": ["novaai_shell", "novaai_script", "novaai_schedule", "novaai_config",
                  "novaai_root_module", "novaai_systemless"],
    "riskCeiling": 3
  },
  "readonly": {
    "allowTools": ["novaai_status", "novaai_fs_info", "novaai_app_list"],
    "denyTools": [],
    "riskCeiling": 0
  }
}
```

> `default` 默认不含 `novaai_shell` / `novaai_script` / `novaai_schedule`：
> 这三个都能到达任意命令执行（`novaai_schedule` 是 create 写脚本 + run 用 `sh` 执行），
> 通用 shell 能绕过所有工具级防护，所以默认交给 `agent_full`。
> 需要时把 token 绑到 `agent_full`，详见 [docs/security.md](security.md)。

---

### sessionBinding (会话绑定)

决定每个请求使用哪个 profile。服务端在鉴权通过后，对**本次请求携带的 token** 计算
`sha256` 十六进制摘要，在 `byTokenHash` 里查表；查不到则回退到 `fallback`。
无 token 的入口（unix socket、`anonymous: true` 时的 loopback）同样走 `fallback`。

这样可以给不同客户端分配不同权限，例如为只读客户端单独发一个 token：

```sh
printf %s "$(cat /data/adb/novaai-mcp/token)" | sha256sum
```

| 字段 | 类型 | 说明 |
|------|------|------|
| byTokenHash | 对象 | token 的 sha256 十六进制摘要 → profile 名称 |
| fallback | 字符串 | 查不到时使用的 profile 名称 |

**示例**:
```json
"sessionBinding": {
  "byTokenHash": {
    "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08": "readonly"
  },
  "fallback": "default"
}
```

---

### audit (审计配置)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| enabled | 布尔 | true | 是否启用审计 |
| maxFileBytes | 整数 | 10485760 | 单个审计文件最大大小 (10MB) |
| maxFiles | 整数 | 20 | 最大审计文件数 |
| retentionDays | 整数 | 30 | 审计日志保留天数 |
| includeArgs | 布尔 | true | 是否记录工具参数 |
| argPreviewBytes | 整数 | 256 | 参数预览最大字节数 |
| allowlistFields | 字符串数组 | [...] | 允许记录的字段列表（其余字段一律脱敏） |

**示例**:
```json
"audit": {
  "enabled": true,
  "maxFileBytes": 10485760,
  "maxFiles": 20,
  "retentionDays": 30,
  "includeArgs": true,
  "argPreviewBytes": 256,
  "allowlistFields": ["action", "path", "package", "name"]
}
```

> 只有 `allowlist` 一种脱敏实现，没有可关闭脱敏的开关。

---

### rateLimit (频率限制)

| 字段 | 类型 | 说明 |
|------|------|------|
| global | 对象 | 全局限制 |
| perSession | 对象 | 第二层限制，**按客户端身份（token 哈希）计数** |
| perTool | 对象 | 每工具限制 |

> `perSession` 这个名字里的 "session" 指**客户端身份**，不是 MCP 的
> `Mcp-Session-Id`。后者由客户端自行携带，换一个或不带就能拿到一个全新的
> 满额桶，用它做限流键等于没有限流。因此服务端用 token 的 `sha256` 作为键；
> 无 token 的入口（unix socket、匿名 loopback）共用一个 `local` 桶。

**示例**:
```json
"rateLimit": {
  "global": {"qps": 50, "burst": 100},
  "perSession": {
    "qps": 20,
    "burst": 40,
    "maxConcurrentTools": 5
  },
  "perTool": {
    "novaai_shell": {"qps": 10, "burst": 20}
  }
}
```

---

### session (会话配置)

只有携带有效 `Mcp-Session-Id` 的请求，以及包含 `initialize` 的请求，才会占用
会话名额。其余请求走无状态路径，不登记会话 —— 否则不实现会话的客户端每发一个
请求就会消耗一个名额，很快撞上 `maxSessions` 并持续收到 `-32014`。

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| idleTimeoutSeconds | 整数 | 1800 | 会话空闲超时 (30分钟) |
| maxSessions | 整数 | 32 | 最大会话数 |
| sweepIntervalSeconds | 整数 | 300 | 清理间隔 (5分钟) |

**示例**:
```json
"session": {
  "idleTimeoutSeconds": 1800,
  "maxSessions": 32,
  "sweepIntervalSeconds": 300
}
```

---

### uninstall (卸载配置)

由 `uninstall.sh` 读取，服务端进程本身不使用。**四个开关默认全部为 false**，
即卸载时默认不删除任何数据；要删必须显式写 `true`。

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| purgeInternalState | 布尔 | false | 卸载时是否清理内部状态目录（是超集：会连带审计/崩溃/日志一起删除） |
| purgeAuditLogs | 布尔 | false | 卸载时是否清理审计日志（仅在保留内部状态目录时生效） |
| purgeCrashDumps | 布尔 | false | 卸载时是否清理崩溃转储（仅在保留内部状态目录时生效） |
| purgeUserData | 布尔 | false | 卸载时是否清理用户数据目录（`/storage/emulated/0/novaaiAI`） |

**示例**:
```json
"uninstall": {
  "purgeInternalState": false,
  "purgeAuditLogs": false,
  "purgeCrashDumps": false,
  "purgeUserData": false
}
```

---

## 完整配置示例

```json
{
  "schemaVersion": 3,
  "network": {
    "port": 5322,
    "listenLoopback": true,
    "listenLan": false,
    "allowedOrigins": []
  },
  "paths": {
    "stateDir": "/data/adb/novaai-mcp",
    "workDir": "/storage/emulated/0/novaaiAI",
    "workspaceRoot": "/data/adb/novaai-mcp/workspace",
    "auditDir": "/data/adb/novaai-mcp/audit"
  },
  "limits": {
    "maxRequestBytes": 67108864,
    "shellTimeoutSeconds": 60,
    "resultPreviewBytes": 1048576,
    "shutdownGraceSeconds": 30
  },
  "security": {
    "anonymous": false,
    "validateHost": true,
    "validateOrigin": true,
    "allowCors": false,
    "token": {
      "enabled": true,
      "value": "your-secret-token",
      "rotateOnStart": false,
      "allowQueryParam": false
    },
    "unixSocket": {
      "enabled": true,
      "path": "/data/adb/novaai-mcp/mcp.sock",
      "mode": "0660",
      "sepolicyInject": true
    },
    "lan": {
      "enabled": false,
      "allowedCidr": ["192.168.0.0/16", "10.0.0.0/8"]
    }
  },
  "profiles": {
    "default": {
      "allowTools": ["*"],
      "denyTools": ["novaai_shell", "novaai_script", "novaai_schedule", "novaai_config",
                    "novaai_root_module", "novaai_systemless"],
      "riskCeiling": 3
    },
    "readonly": {
      "allowTools": ["novaai_status", "novaai_fs_info"],
      "denyTools": [],
      "riskCeiling": 0
    }
  },
  "sessionBinding": {
    "byTokenHash": {},
    "fallback": "default"
  },
  "audit": {
    "enabled": true,
    "maxFileBytes": 10485760,
    "maxFiles": 20,
    "retentionDays": 30,
    "includeArgs": true,
    "argPreviewBytes": 256,
    "allowlistFields": ["action", "path", "package"]
  },
  "rateLimit": {
    "global": {"qps": 50, "burst": 100},
    "perSession": {"qps": 20, "burst": 40, "maxConcurrentTools": 5},
    "perTool": {}
  },
  "session": {
    "idleTimeoutSeconds": 1800,
    "maxSessions": 32,
    "sweepIntervalSeconds": 300
  },
  "uninstall": {
    "purgeInternalState": false,
    "purgeAuditLogs": false,
    "purgeCrashDumps": false,
    "purgeUserData": false
  }
}
```
