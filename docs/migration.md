# 配置迁移

说明 `config.json` 的 `schemaVersion` 演进、迁移机制，以及升级到 v0.05 后
旧配置里哪些键会失效。

## schemaVersion 历史

| 版本 | 状态 | 说明 |
|------|------|------|
| 0 / 2 | 可迁移 | 旧格式。缺 `profiles` / `audit` / `session` / `rateLimit` |
| 3 | 当前 | `migrateV2ToV3` 的目标格式，也是 `config.Validate` 唯一接受的版本 |

`config.Validate` 对非 3 的版本直接报错：

```
unsupported schemaVersion: <n>
```

## 迁移机制

入口是 `internal/migrate`，由 `novaaimcpd` 启动时调用 `RunIfNeeded`。

**幂等标记**：`<stateDir>/.migrated-v003`。存在即跳过，内容为完成时间（RFC3339）。

**互斥锁**：`<stateDir>/.migrate.lock`，用 `O_CREATE|O_EXCL` 创建。
若锁文件存在且修改时间在 5 分钟内，返回 `迁移已被锁定` 并放弃启动；
超过 5 分钟视为陈旧锁，自动删除后重试。

**回滚备份**：迁移前把原始内容写入 `config.json.v002.bak`（权限 0600）。
需要回滚时把它覆盖回 `config.json`，并删除 `.migrated-v003`。

**写入方式**：先写 `config.json.tmp`，再 `os.Rename` 原子替换。

## v2 → v3 做了什么

1. 从 token 文件读回 token；若不存在则生成（`auth.EnsureToken`）。
2. 重写 `security` 段：
   - `anonymous=false`、`validateHost=true`、`validateOrigin=true`、`allowCors=false`
   - `token`：以 token 文件的值为准（**不再使用旧配置里的 `security.token`**）
   - `unixSocket`：启用，路径 `<stateDir>/mcp.sock`，权限 0660
   - `lan`：禁用，默认 CIDR 为三段私网
3. `schemaVersion` 置为 3。
4. 补齐缺失的顶层段（`fillMissingV3Fields`）：`profiles` / `audit` /
   `session` / `rateLimit`。**仅在字段整体缺失时补齐**，已存在的段不覆盖。

### 关于 profiles 的补齐

`profiles` 缺失时补入的默认值，与全新安装（`config.Default()`）使用
**同一个定义**（`config.DefaultProfiles()`）。这一点很重要：早期版本里
迁移路径硬编码了一份更宽松的 default profile（`denyTools: []`），
会让"迁移上来的机器"比"全新安装的机器"多出 `novaai_shell` 权限。
现已收敛为单一来源。

如果旧配置里**已经有** `profiles` 段，迁移不会改动它——包括其中可能
存在的宽松规则。升级后请用 `novaai_config` 或直接查看 `config.json`
确认 default profile 的 `denyTools` 是否包含 `novaai_shell`、
`novaai_script`、`novaai_schedule`（后两者与前两者等价，都能到达任意命令执行）。

## 已移除的配置键

配置键的移除分两批。

### 第一批（v0.05 早期）

| 键 | 原文档说明 |
|----|-----------|
| `network.legacySse` | 是否启用旧版 SSE 支持 |
| `security.onLinkOnly` | 仅允许本地连接 |
| `security.dropFrontendUid` | 前端 UID（用于权限降级） |
| `security.unixSocket.peerUidRecordOnly` | 是否仅记录对端 UID |

### 第二批（配置收敛轮）

| 键 | 原因 |
|----|------|
| `limits.maxConnections` | 从未实现连接上限 |
| `limits.totalTasks` `heavyTasks` | 无任务系统 |
| `limits.artifactTtlSeconds` | 无产物存储，也无过期清理 |
| `limits.transferChunkBytes` `transferMaxBytes` | 无服务端分块或大小限制 |
| `limits.uploadIdleTtlSeconds` | 无上传会话存储 |
| `limits.downloadRetryAttempts` | 与每次调用的 `retries` 参数重复 |
| `security.unixSocket.group` | socket 只做 `chcon`，从未 `chgrp` |
| `audit.separateArgsFile` | 从未写出独立的参数文件 |
| `audit.redactMode` | 只实现了 allowlist |
| `paths.downloadsDir` `uploadsDir` `artifactsDir` `tempDir` | 子目录固定挂在 `stateDir` 下 |
| `rateLimit.perSession.totalUploadBytes` `totalDownloadBytes` | 唯一消费者是零调用的死函数 |
| `skill.learnFromRiskOps` `maxLearnedSkills` | 无学习机制（整个 `skill` 段移除） |

### 第三批（第二轮工具契约审计）

| 键 | 原因 |
|----|------|
| `paths.crashDir` | 崩溃处理器必须在配置加载**之前**装好，这个旋钮永远不可能生效；崩溃目录固定为 `{stateDir}/crash` |
| `capabilities` | 顶层 `map[string]bool`，Go 侧零访问、shell 侧零读取；`config.example.json` 自己注释为"保留字段"。能力探测由 `novaai_capabilities` 工具在运行时执行 |

第二批的清单里漏了 `paths.crashDir` 与 `capabilities`。前者和 `downloadsDir`
那四个一样是"只声明不生效"，但多一层硬约束：`main.go` 的顺序是
`prepareStateDir` → `InstallCrashHandlers` → `loadOrMigrateConfig`，改成先读配置
再装处理器，配置解析阶段的 panic 就没有兜底了。`paths.auditDir` 保留可配置，
因为审计日志在配置加载之后才初始化。

两个字段都是**机械检查的假阴性**：按名字做全仓文本计数时，`crashDir` 被别处的
`crash` 子串掩盖，`capabilities` 被探针脚本里 MCP 协议的 `capabilities` 掩盖。
可靠判据是"Go 是否以 `.字段名` 访问过它"，shell 侧再按配置键名单独确认。

**旧配置文件里的这些键会被静默忽略**，不需要手工清理。

原因：Go 的 `json.Unmarshal` 默认忽略结构体中不存在的字段，且这些键
从未影响任何行为。删除它们是文档与结构清理，不改变运行时语义。

两个字段**改为被真正读取**，不再是无消费者的摆设：

- `limits.shellTimeoutSeconds` —— 原为硬编码 60s，现为唯一默认超时来源；
- `limits.resultPreviewBytes` —— 原无任何截断逻辑，默认值由 256KiB 提到 1MiB。

> **行为变化提醒**：`limits.resultPreviewBytes` 现在真的会截断。超过上限的
> 工具结果会被裁剪（不切断 UTF-8 字符）、追加一行截断说明，并丢弃
> `structuredContent`。依赖完整结果的客户端应把它调大，或设为 0 表示不限制。

## 升级检查清单

1. 升级后确认 `<stateDir>/.migrated-v003` 已生成。
2. 用 `novaai_auth_status` 确认当前 token 与 `config.json` 一致
   （v0.05 之前的版本里这两者可能是两个不同的随机值）。
3. 确认 default profile 的 `denyTools` 含 `novaai_shell`、`novaai_script`、
   `novaai_schedule`。
4. 若曾手工配置过本文件列出的已删键，删除它们不会报错，留着也无害。
5. 若客户端在轮询 `novaai_task`，需要改掉：该工具已删除（它的 5 个 action
   全部空转，服务端也没有任何工具会产生 taskId）。
6. 若客户端按旧 schema 发送过以下参数，需要改掉 —— 它们已从 schema 删除，
   服务端会忽略（本服务不做 schema 校验，未知参数不会报错，但也不会有任何效果）：
   - `background`：曾在 `novaai_shell` / `novaai_script` / `novaai_archive` /
     `novaai_app_install` / `novaai_screen` / `novaai_diagnostics` 上声明，
     但没有任何 handler 读取它。传 `true` 不会让命令后台执行，只会同步阻塞。
   - `action`：`novaai_shell` `novaai_status` `novaai_capabilities`
     `novaai_root_info` `novaai_app_list` `novaai_app_install`
     `novaai_app_export` `novaai_transfer_export` 上声明过但从不生效。
   - `novaai_app_install` 的 `package` 与 `paths`。
7. 若客户端曾依赖 `novaai_app_install` 安装 Split/APKS/XAPK、或
   `novaai_app_export` 导出 Split/Bundle：这些从未实现，本轮已从描述中移除。
   当前 `novaai_app_install` 只走 `pm install` 单文件通路，
   `novaai_app_export` 只导出 `pm path` 给出的主 APK。
