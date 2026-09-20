# 配置迁移

## 一句话

**配置极简，无需迁移。**

`schemaVersion` 字段、`internal/migrate` 包、迁移锁、幂等标记、回滚备份、
组合校验**全部已删除**。`config.Load` 以 `Default()` 为基底反序列化，
旧配置里不存在的键会从默认值补齐，多余的键被 `json.Unmarshal`
**静默忽略**。

---

## 当前行为

| 场景 | 结果 |
|------|------|
| 全新安装 | 首次启动生成完整 `config.json` |
| 旧配置里少了某个新键 | 从 `Default()` 补齐 |
| 旧配置里多了已删除的键 | **静默忽略**，不需要手工清理 |
| 旧配置里值非法（如 `profile: "readonly"`） | 启动时 `Validate` 拒绝，报具体错误 |

配置文件位置：`/data/adb/novaai-mcp/config.json`。字段清单见
[config.md](config.md)。

---

## 已移除的配置段

本轮（精简重构）整段删除：

| 段 | 原用途 | 为什么删 |
|----|--------|----------|
| `security` | `token` / `anonymous` / `lan` / `cors` / `allowedOrigins` / `sessionBinding` / `validateHost` / `validateOrigin` / `unixSocket` | 无鉴权，所有来源一视同仁；`unixSocket` 提到顶层 |
| `network` | `port` / `bindAddress` / `legacySse` … | 合并为单个顶层 `listen` |
| `profiles` | `default` / `conservative` / `readonly` / `reverse` / `agent_full` | 只留 `default` 一个形状，写在代码里 |
| `session` | `maxSessions` / `idleTimeoutSeconds` / `sweepIntervalSeconds` | 收敛为常量（32 / 30 分钟 / 5 分钟） |
| `rateLimit` | `global` / `perSession` / `perTool` / `perSession.maxConcurrentTools` | 收敛到顶层 `limits`（3 个键），删掉按身份的层 |
| `paths` | `stateDir` / `workspaceRoot` / `auditDir` / `crashDir` | `stateDir` 提到顶层；其余子目录由它派生 |
| `uninstall` | `purgeInternalState` / `purgeAuditLogs` / `purgeCrashDumps` / `purgeUserData` | **保留由 `uninstall.sh` 读取**，见下 |
| `skill` | `learnFromRiskOps` / `maxLearnedSkills` | 无学习机制（更早一轮已删） |
| `capabilities` | `map[string]bool` | 零消费者（更早一轮已删） |
| `audit.includeArgs` / `separateArgsFile` / `redactMode` | 参数记录策略 | 只剩一种行为（白名单脱敏），无开关 |

> `uninstall` 段是**唯一**不被 Go 读取、但被 shell 读取的段：
> `uninstall.sh` 用 `grep` 判断是否清理内部状态 / 审计 / 崩溃转储 / 用户目录。
> 它不属于 Go 的 `Config` 结构体，因此写在 `config.json` 里**会被 Go 侧忽略**，
> 但对卸载脚本有效。默认全部 `false`（保留数据）。

---

## 旧配置会发生什么（实测语义）

`config.Load` 的实现是"**先 `Default()`，再 `json.Unmarshal` 覆盖**"。
因此：

| 旧配置形态 | 加载结果 |
|---|---|
| 只有旧键（`security` / `network` / `profiles`…） | ✅ 通过；全部被忽略；`listen`、`limits`、`audit` 等从默认值补齐 |
| 部分新键 + 部分旧键 | ✅ 通过；新键生效，旧键忽略 |
| `profile` 写了 `default` 以外的值 | ❌ 启动被拒：`profile 只能是 "default"` |
| `listen` 是域名或端口越界 | ❌ 启动被拒 |

**不会因为"旧配置缺新键"而启动失败**，也不会因为"旧配置有多余键"而失败。

> **行为提醒**：旧配置若显式写过 `listen`（例如曾经为了"仅本机"
> 而把它配成 `127.0.0.1:5322`），升级后**这个值会保留**，因为
> `json.Unmarshal` 会覆盖默认值。这是期望行为，不是回归。
>
> 反之，旧配置里的 `network.port` 不会再影响任何东西 —— `listen` 是唯一入口。

---

## 升级检查清单

1. 升级后确认能连上：`curl -X POST http://127.0.0.1:5322/mcp -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":1,"method":"ping"}'`
   —— **不需要任何认证头**。
2. 确认 `listen` 是你想要的：`"0.0.0.0:5322"`（局域网直连）还是
   `"127.0.0.1:5322"`（仅本机）。这是本服务**唯一的权限旋钮**。
3. 若客户端在发 `Authorization` 头：删掉。服务端不看它，但带上也不会出错。
4. 若客户端在调用已裁剪的工具（`novaai_reverse_*`、`novaai_hook_*`、
   `novaai_skill`、`novaai_setting`、`novaai_network`、`novaai_backup`、
   `novaai_auth_status`、`novaai_session_*`、`novaai_audit_status`…）：
   会收到 `-32015 工具不存在`。这些能力改由 `novaai_shell` 承担，
   完整清单见 [README.md](../README.md) 的工具表与
   [KNOWN_ISSUES.md](KNOWN_ISSUES.md) 第 13 节。
5. 若客户端按旧文档发送过 `confirmDangerous` / `background` 参数：
   服务端忽略未知参数，不会报错，但也不会有任何效果。
6. 旧配置里的已删键**不需要手工清理**。想清理就按 [config.md](config.md)
   的「完整配置」重写一份。

---

## 为什么不再需要迁移机制

迁移机制存在的理由是"配置 schema 会随版本演进，且演进过程不能丢用户意图"。

现在的配置只有 8 个键，且每一个都是"要么保持默认、要么改一个值"类型的旋钮，
没有需要**转换**的结构。再加上 Go 的 `json.Unmarshal` 对未知键天然宽容、
对缺失键由 `Default()` 兜底，迁移机制就成了纯粹的维护负担：

- 它需要自己的锁（`.migrate.lock`）、幂等标记（`.migrated-v003`）、
  回滚备份（`config.json.v002.bak`）；
- 它的存在本身要求 `Validate` 接受多个 `schemaVersion`，
  而旧版本的键集合没有任何东西在维护 —— 一个只为历史存在的校验分支，
  比没有校验更危险。

因此：**`schemaVersion` 字段已删除，`internal/migrate` 包已删除。**
