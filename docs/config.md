# NovaAI-MCP 配置文件说明

配置文件路径: `/data/adb/novaai-mcp/config.json`

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
  "skill": { ... },
  "uninstall": { ... },
  "capabilities": { ... }
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

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| stateDir | 字符串 | /data/adb/novaai-mcp | 状态目录（配置、日志等） |
| workDir | 字符串 | /storage/emulated/0/novaaiAI | 工作目录（用户数据） |
| workspaceRoot | 字符串 | {stateDir}/workspace | 工作空间根目录 |
| downloadsDir | 字符串 | {stateDir}/downloads | 下载目录 |
| uploadsDir | 字符串 | {stateDir}/uploads | 上传目录 |
| artifactsDir | 字符串 | {stateDir}/artifacts | 产物目录 |
| tempDir | 字符串 | {stateDir}/tmp | 临时目录 |
| auditDir | 字符串 | {stateDir}/audit | 审计日志目录 |
| crashDir | 字符串 | {stateDir}/crash | 崩溃转储目录 |

**示例**:
```json
"paths": {
  "stateDir": "/data/adb/novaai-mcp",
  "workDir": "/storage/emulated/0/novaaiAI",
  "workspaceRoot": "/data/adb/novaai-mcp/workspace",
  "downloadsDir": "/data/adb/novaai-mcp/downloads",
  "uploadsDir": "/data/adb/novaai-mcp/uploads",
  "artifactsDir": "/data/adb/novaai-mcp/artifacts",
  "tempDir": "/data/adb/novaai-mcp/tmp",
  "auditDir": "/data/adb/novaai-mcp/audit",
  "crashDir": "/data/adb/novaai-mcp/crash"
}
```

---

### limits (限制配置)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| maxConnections | 整数 | 128 | 最大并发连接数 |
| maxRequestBytes | 整数 | 67108864 | 最大请求体大小 (64MB) |
| totalTasks | 整数 | 16 | 最大任务数 |
| heavyTasks | 整数 | 2 | 最大重任务数 |
| shellTimeoutSeconds | 整数 | 60 | Shell 命令超时时间 |
| transferChunkBytes | 整数 | 1048576 | 传输分块大小 (1MB) |
| transferMaxBytes | 整数 | 1073741824 | 最大传输大小 (1GB) |
| resultPreviewBytes | 整数 | 262144 | 结果预览大小 (256KB) |
| artifactTtlSeconds | 整数 | 604800 | 产物保留时间 (7天) |
| shutdownGraceSeconds | 整数 | 30 | 优雅关闭等待时间 |
| uploadIdleTtlSeconds | 整数 | 1800 | 上传空闲超时 (30分钟) |
| downloadRetryAttempts | 整数 | 3 | 下载重试次数 |

**示例**:
```json
"limits": {
  "maxConnections": 128,
  "maxRequestBytes": 67108864,
  "totalTasks": 16,
  "heavyTasks": 2,
  "shellTimeoutSeconds": 60,
  "transferChunkBytes": 1048576,
  "transferMaxBytes": 1073741824,
  "resultPreviewBytes": 262144,
  "artifactTtlSeconds": 604800,
  "shutdownGraceSeconds": 30,
  "uploadIdleTtlSeconds": 1800,
  "downloadRetryAttempts": 3
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
| group | 字符串 | shell | Socket 文件所属组 |
| sepolicyInject | 布尔 | true | 是否注入 SELinux 策略 |

**示例**:
```json
"unixSocket": {
  "enabled": true,
  "path": "/data/adb/novaai-mcp/mcp.sock",
  "mode": "0660",
  "group": "shell",
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
    "denyTools": ["novaai_shell", "novaai_script", "novaai_config",
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

> `default` 默认不含 `novaai_shell` / `novaai_script`：通用 shell 能绕过所有
> 工具级防护，所以默认交给 `agent_full`。需要时把 token 绑到 `agent_full`，
> 详见 [docs/security.md](security.md)。

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
| redactMode | 字符串 | allowlist | 脱敏模式 (allowlist/all/none) |
| allowlistFields | 字符串数组 | [...] | 允许记录的字段列表 |

**示例**:
```json
"audit": {
  "enabled": true,
  "maxFileBytes": 10485760,
  "maxFiles": 20,
  "retentionDays": 30,
  "includeArgs": true,
  "argPreviewBytes": 256,
  "redactMode": "allowlist",
  "allowlistFields": ["action", "path", "package", "name"]
}
```

---

### rateLimit (频率限制)

| 字段 | 类型 | 说明 |
|------|------|------|
| global | 对象 | 全局限制 |
| perSession | 对象 | 每会话限制 |
| perTool | 对象 | 每工具限制 |

**示例**:
```json
"rateLimit": {
  "global": {"qps": 50, "burst": 100},
  "perSession": {
    "qps": 20,
    "burst": 40,
    "maxConcurrentTools": 5,
    "totalUploadBytes": 5368709120,
    "totalDownloadBytes": 5368709120
  },
  "perTool": {
    "novaai_shell": {"qps": 10, "burst": 20}
  }
}
```

---

### session (会话配置)

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

### skill (技能配置)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| learnFromRiskOps | 布尔 | false | 是否从高风险操作学习技能 |
| maxLearnedSkills | 整数 | 200 | 最大学习技能数 |

**示例**:
```json
"skill": {
  "learnFromRiskOps": false,
  "maxLearnedSkills": 200
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
    "downloadsDir": "/data/adb/novaai-mcp/downloads",
    "uploadsDir": "/data/adb/novaai-mcp/uploads",
    "artifactsDir": "/data/adb/novaai-mcp/artifacts",
    "tempDir": "/data/adb/novaai-mcp/tmp",
    "auditDir": "/data/adb/novaai-mcp/audit",
    "crashDir": "/data/adb/novaai-mcp/crash"
  },
  "limits": {
    "maxConnections": 128,
    "maxRequestBytes": 67108864,
    "totalTasks": 16,
    "heavyTasks": 2,
    "shellTimeoutSeconds": 60,
    "transferChunkBytes": 1048576,
    "transferMaxBytes": 1073741824,
    "resultPreviewBytes": 262144,
    "artifactTtlSeconds": 604800,
    "shutdownGraceSeconds": 30,
    "uploadIdleTtlSeconds": 1800,
    "downloadRetryAttempts": 3
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
      "group": "shell",
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
      "denyTools": ["novaai_shell", "novaai_script", "novaai_config",
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
    "redactMode": "allowlist",
    "allowlistFields": ["action", "path", "package"]
  },
  "rateLimit": {
    "global": {"qps": 50, "burst": 100},
    "perSession": {"qps": 20, "burst": 40},
    "perTool": {}
  },
  "session": {
    "idleTimeoutSeconds": 1800,
    "maxSessions": 32,
    "sweepIntervalSeconds": 300
  },
  "skill": {
    "learnFromRiskOps": false,
    "maxLearnedSkills": 200
  },
  "uninstall": {
    "purgeInternalState": false,
    "purgeAuditLogs": false,
    "purgeCrashDumps": false,
    "purgeUserData": false
  },
  "capabilities": {}
}
```
