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
   - `unixSocket`：启用，路径 `<stateDir>/mcp.sock`，权限 0660，组 `shell`
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
确认 default profile 的 `denyTools` 是否包含 `novaai_shell`。

## v0.05 移除的配置键

以下 4 个键在 v0.05 被删除（它们从未被任何代码读取，只在文档里存在）：

| 键 | 原文档说明 |
|----|-----------|
| `network.legacySse` | 是否启用旧版 SSE 支持 |
| `security.onLinkOnly` | 仅允许本地连接 |
| `security.dropFrontendUid` | 前端 UID（用于权限降级） |
| `security.unixSocket.peerUidRecordOnly` | 是否仅记录对端 UID |

**旧配置文件里的这些键会被静默忽略**，不需要手工清理。

原因：Go 的 `json.Unmarshal` 默认忽略结构体中不存在的字段，且这些键
从未影响任何行为。删除它们是纯粹的文档与结构清理，不改变运行时语义。

其余 20 个同样无消费者的字段**没有**删除，清单见
[`KNOWN_ISSUES.md`](KNOWN_ISSUES.md) 第 2 节——其中一部分应当接线而非删除。

## 升级检查清单

1. 升级后确认 `<stateDir>/.migrated-v003` 已生成。
2. 用 `novaai_auth_status` 确认当前 token 与 `config.json` 一致
   （v0.05 之前的版本里这两者可能是两个不同的随机值）。
3. 确认 default profile 的 `denyTools` 含 `novaai_shell`、`novaai_script`。
4. 若曾手工配置过本文件列出的 4 个已删键，删除它们不会报错，留着也无害。
