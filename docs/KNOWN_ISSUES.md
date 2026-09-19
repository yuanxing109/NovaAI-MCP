# 已知问题与未完成项

本文记录**当前状态下确实存在**的限制、待决策项与残余风险。
与 `security.md`（说明防护模型）互补：那里讲"挡什么"，这里讲"还差什么"。

最后更新：v0.05（对应 commit `b1e1440` 之后的清理轮）

---

## 1. 构建：staging 清单有两份

`build.sh`（Unix）与 `build.ps1`（Windows）各自维护一份打包清单。
**改动其中一处必须同步另一处**，否则两个平台产出的包内容不一致。

- Windows 必须用 `build.ps1`：Windows 通常没有 `zip`，且 Git 自带的 bsdtar
  写出的 zip 不保留 Unix 权限位（实测 0755 被写成 `-rw-rw-rw-`）。
- `build.ps1` 自带产物校验：条目数、每个文件的 Unix 权限位、
  central directory 的宿主字段是否为 Unix(3)，任一不符即失败退出。
- `build.sh` 依赖 Unix 宿主的 `zip` 命令；其权限位由 `zip` 自身从文件系统读取。

未做：把清单抽成单一数据文件供两个脚本共用。当前靠注释互相指向 + 人工同步。

## 2. 配置项：20 个字段无任何消费者

`config.json` 里有 20 个字段，**Go 代码与 shell 脚本都不读取它们**。
它们在 `types.go` 声明、在 `default.go` 赋默认值、在 `migrate.go` 写入，
并且 `docs/config.md` 与 `docs/config.example.json` 都在教用户配置它们。

按"是否存在对应行为"分两类：

### 2a. 行为存在但被硬编码 —— 应当接线，不应删除

删掉这些字段会丢失唯一的配置入口，所以正确的修法是让代码读取配置。

| 字段 | 现状 |
|------|------|
| `limits.shellTimeoutSeconds` | `helpers.go` 的 `runCmd`/`runSh`/`runShRaw` 在 `timeout <= 0` 时硬编码 60s，从未传入配置值 |
| `paths.downloadsDir` `uploadsDir` `artifactsDir` `tempDir` | `cmd/novaaimcpd/main.go` 用 `filepath.Join(stateDir, "downloads")` 等硬编码路径建目录，忽略配置值 |
| `limits.downloadRetryAttempts` | `archive.go` 的 `curl --retry` 用的是硬编码值 |
| `limits.transferChunkBytes` `transferMaxBytes` `uploadIdleTtlSeconds` | 传输层有对应机制，但读的是别处的常量/参数 |
| `audit.redactMode` | `audit/redact.go` 固定按 allowlist 处理，`all` / `none` 两种模式从未实现 |

### 2b. 无对应行为 —— 可删

| 字段 | 说明 |
|------|------|
| `limits.maxConnections` | HTTP server 未设置连接上限 |
| `limits.totalTasks` `heavyTasks` | 无任务数上限；并发由 `rateLimit.perSession.maxConcurrentTools` 单独管理 |
| `limits.resultPreviewBytes` | 无结果预览截断（`audit.argPreviewBytes` 是审计参数预览，是另一个东西） |
| `limits.artifactTtlSeconds` | 无产物过期清理 |
| `security.unixSocket.group` | socket 只做了 `chcon`，从未 `chgrp`；`middleware.go` 的注释也承认这点 |
| `audit.separateArgsFile` | 从未写出独立的参数文件 |
| `skill.learnFromRiskOps` `maxLearnedSkills` | 无学习逻辑，也无数量上限；`novaai_skill` 直接用 `stateDir/skills` |

> 注：`uninstall.purgeInternalState` / `purgeAuditLogs` / `purgeCrashDumps` /
> `purgeUserData` 同样不被 Go 读取，但**由 `uninstall.sh` 读取**，属正常设计。

## 3. shell 拦截不覆盖的形式

`internal/antibrick` 是静态分析，不做求值。以下形式不被拦截：

1. 变量与命令替换：`P=/data; rm -rf $P`、`rm -rf $(echo L2RhdGE= | base64 -d)`
2. `xargs` / `eval` / 自定义函数：`echo / | xargs rm -rf`
3. 编译或解释执行的间接路径：把逻辑写进脚本文件再执行
4. `novaai_archive extract` 不解析归档成员路径（7z 自身会拒绝 `../`）

定位是**防误操作与防模型失误**，不是对抗有动机的攻击者。

## 4. 已识别但未处理的死代码

| 对象 | 证据 | 状态 |
|------|------|------|
| `auth.PeerUID` + `peerUIDFromFD`(unix/windows) | `PeerUID` 全仓零调用 | 待决策 |
| `session.State.PeerUID` | 唯一写入点是 `middleware.go:109` 传的字面量 `-1`，全仓无读取 | 待决策 |
| `mcp.Server.sseConns` + `SSEEvent` | 初始化后从未注册任何连接，关机广播遍历的是空 map | 待决策 |

这三项同属"对端 UID 记录"与"SSE 推送"两条已失效的职责。
删除它们不影响任何现有功能，但会改动 `session.State` 与 `mcp.Server` 的结构。

已在本轮删除的同类对象：`config.IsHotReloadable` + `noHotReloadFields`、
`adapter.FallbackChain` + `Attempt` + 5 个 ROM adapter 的实现、
`auth.WithPeerUID` + `PeerUIDFromContext`。

## 5. 版本控制

仓库已初始化 git（首次提交 `b1e1440`），`.gitignore` 排除 `dist/`、
`.backup/`、`bin/*/novaaimcpd`。

- `.gitattributes` 关闭了所有 EOL 转换（`* -text`）。**不要移除它**：
  `core.autocrlf` 会把 `.sh` 转成 CRLF，导致模块在 Android 上无法运行。
- 仓库级 `core.autocrlf=false` 已设置，但这是本地配置，克隆到新机器后
  需要重新设置（`.gitattributes` 的 `-text` 已能兜住，设不设都不影响正确性）。
- `.backup/` 里的手工快照是 git 接管之前的回滚手段，现已冗余，未删除。

## 6. 文档缺口

- `docs/config.md` 的 `audit` 一节未记录 `separateArgsFile`（该字段本身也是死的，见 2b）。
- `docs/config.md` 与 `config.example.json` 的 profile 示例此前与
  `config.Default()` 不一致，本轮已同步。
