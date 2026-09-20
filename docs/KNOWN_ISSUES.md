# 已知问题与未完成项

本文记录**当前状态下确实存在**的限制、待决策项与残余风险。
与 `security.md`（说明防护模型）互补：那里讲"挡什么"，这里讲"还差什么"。

最后更新：上游 MCP 聚合 + KernelSU WebUI 轮（第九轮：聚合网关）

> **第 1–12 节记录的是第八轮精简重构**之前的各轮排查，
> 其中涉及鉴权、多 profile、来源判定、`confirmDangerous`、61 个工具的段落
> 已不再描述当前行为 —— 保留它们是为了留下"为什么当初这样做"的痕迹。
> **当前状态以第 13、14 节为准。**

---

## 1. 构建：staging 清单有两份

`build.sh`（Unix）与 `build.ps1`（Windows）各自维护一份打包清单。
**改动其中一处必须同步另一处**，否则两个平台产出的包内容不一致。

- Windows 必须用 `build.ps1`：Windows 通常没有 `zip`，且 Git 自带的 bsdtar
  写出的 zip 不保留 Unix 权限位（实测 0755 被写成 `-rw-rw-rw-`）。
- `build.ps1` 自带产物校验：条目数、每个文件的 Unix 权限位、
  central directory 的宿主字段是否为 Unix(3)，任一不符即失败退出。
- `build.sh` 依赖 Unix 宿主的 `zip` 命令；其权限位由 `zip` 自身从文件系统读取。

**已修（第四轮）**：产物判据原先只存在于 `build.ps1` 的 `Test-Package` 里，
而 `build.sh` 没有任何产物校验。若 CI 再写一份校验去校验 `build.sh` 的产物，
就会重新制造本节所述的漂移。现已抽出：

| 文件 | 职责 |
|------|------|
| `scripts/package_contract.ps1` | 可执行权限矩阵 —— **唯一声明点** |
| `scripts/verify_package.ps1` | 产物校验 —— **唯一实现** |

`build.ps1` 点源矩阵，且其 `Test-Package` 委托给 `verify_package.ps1`；
CI 用**同一个** `verify_package.ps1` 校验 `build.sh` 的产物。
`scripts/audit_shell.ps1` 第 3 项从 `package_contract.ps1` 提取矩阵，
再验证 `build.sh` 的 chmod 目标覆盖它。于是"两个平台的包按同一套规则判定"
成为结构保证，而不是人工同步。

**仍未做**：staging 清单（复制哪些文件进 ZIP）仍是两份，`build.sh` 与
`build.ps1` 各一份。抽成单一数据文件仍未做；但**权限矩阵与产物校验已收敛**，
这是原先最容易漂移的部分。

**已修（第三轮）**：`build.ps1` 的 `Test-Executable` 要求 `bin/*/7zz` 必须是
0755，而 `build.sh` 的 chmod 清单漏了它 —— 于是 Linux 检出上跑 `build.sh`
产出的包，会被 Windows 侧校验器判为失败。本机
`core.fileMode=false`，`git ls-files -s bin/` 显示 `bin/*/7zz` 在索引里是
`100644`，所以这个漏项在 Windows 上永远看不到。现已补上。
（`Test-Executable` 本身在第四轮迁到了 `scripts/package_contract.ps1`，
见上。）

> 更正：`META-INF/com/google/android/update-binary` **一直**有单独的 chmod
> （`build.sh` 第 127 行），不在漏项之列。早先把"漏 2 类 4 个文件"写进判断是
> 错的，实际只有 `bin/*/7zz` 一类 3 个文件。

## 2. 配置字段：本轮清理 21 个无消费者字段

> **本节是历史记录。** 其中提到的 `migrate.go` 与 `config.example.json`
> 已在第八轮删除；`types.go` / `default.go` 的字段集合也已重写为 8 个键
> （见 [config.md](config.md)）。下面保留的是"当初怎么判定零消费者"的方法，
> 它比结论更有价值。

清理前 `config.json` 有 21 个字段既不被 Go 读取、也不被 shell 读取，
但 `types.go` 声明、`default.go` 赋默认值、`migrate.go` 写入，
且 `config.md` 与 `config.example.json` 在教用户配置它们。
按"是否存在对应行为"分三类处置：

### 2a. 已接线（行为存在，配置成为唯一入口）

| 字段 | 接线点 |
|------|--------|
| `limits.shellTimeoutSeconds` | `helpers.go` 的 `runCmd`/`runSh`/`runShRaw` 不再硬编码 60s，改读配置 |
| `limits.resultPreviewBytes` | 新增结果大小上限：超限截断 `content`（不切断 UTF-8 字符）、追加截断说明、丢弃 `structuredContent`。默认由 256KiB 提到 1MiB |

### 2b. 已删除（无任何对应行为）

`limits.maxConnections` · `totalTasks` · `heavyTasks` · `artifactTtlSeconds` ·
`transferChunkBytes` · `transferMaxBytes` · `uploadIdleTtlSeconds` ·
`downloadRetryAttempts` · `security.unixSocket.group` · `audit.separateArgsFile` ·
`audit.redactMode` · `skill.learnFromRiskOps` · `maxLearnedSkills` ·
`rateLimit.perSession.totalUploadBytes` · `totalDownloadBytes`

两处需要说明：

- `downloadRetryAttempts` 是**重复 owner**：`retries` 已是每次调用的参数
  （`archive.go`），且默认值同为 3，删掉全局字段不丢能力。
- `redactMode` 只实现了 `allowlist`；`all` / `none` 从未实现，其中 `none`
  是危险选项，删掉比留着更安全。`audit` 现在只有一种脱敏行为。

> 注：`uninstall.purgeInternalState` / `purgeAuditLogs` / `purgeCrashDumps` /
> `purgeUserData` 同样不被 Go 读取，但**由 `uninstall.sh` 读取**，属正常设计。

### 2c. 已删除（子目录覆盖）

`paths.downloadsDir` `uploadsDir` `artifactsDir` `tempDir`。
它们只是 `stateDir` 下的固定子目录，覆盖它们需要与 `pathguard` 的角色白名单
保持同步，维护成本大于收益。`stateDir` 是唯一旋钮。

### 2d. 已删除（第二轮补漏：`paths.crashDir` 与 `capabilities`）

第一轮的"零消费者字段"清单漏了两个：`paths.crashDir` 与顶层 `capabilities`。

**`paths.crashDir`**：崩溃目录在三处被硬编码为 `{stateDir}/crash`（`main.go`
两处、`health/watchdog.go` 一处）。它比其它子目录更硬：**崩溃处理器必须在配置
加载之前装好**。`main.go` 的顺序是 `prepareStateDir` → `InstallCrashHandlers`
→ `loadOrMigrateConfig`；若改成先读配置再装处理器，配置解析阶段的 panic 就没有
兜底了。一个只能在配置就绪后才可能生效的字段等于没有这个字段，因此删除而非接线。
`paths.auditDir` 保留可配置：审计日志在配置加载**之后**才初始化，能真正读到它。

**`capabilities`**（`map[string]bool`）：Go 侧零访问、shell 侧零读取，
原示例文件自己把它注释为"保留字段"（该文件已删除，见第 8 节追记）。
唯一同名的地方是
`server.go` 里 MCP `initialize` 参数的局部结构体，以及探针脚本里对
`initialize` 响应的 `capabilities.tools` 断言 —— 都与配置无关。
能力探测本身由 `novaai_capabilities` 工具在运行时执行，不读配置。

> 这两个字段是**机械检查的假阴性**漏掉的，值得记一笔：
> `paths.crashDir` 被"`crash` 这个子串在别处出现过"掩盖；
> `capabilities` 被"探针脚本里有 MCP 协议的 `capabilities`"掩盖。
> 按名字做全仓文本计数来判"零消费者"是不可靠的 —— 判据必须是
> "Go 是否以 `.字段名` 访问过它"，且 shell 侧要按配置键名单独确认。

## 3. shell 拦截不覆盖的形式

`internal/antibrick` 是静态分析，不做求值。以下形式不被拦截：

1. 变量与命令替换：`P=/data; rm -rf $P`、`rm -rf $(echo L2RhdGE= | base64 -d)`
2. `xargs` / `eval` / 自定义函数：`echo / | xargs rm -rf`
3. 编译或解释执行的间接路径：把逻辑写进脚本文件再执行
4. `novaai_archive extract` 不解析归档成员路径（7z 自身会拒绝 `../`）

定位是**防误操作与防模型失误**，不是对抗有动机的攻击者。

## 4. 已删除的死代码（记录）

本轮删除：

| 对象 | 删除依据 |
|------|----------|
| `config.Current()` / `Set()` / `current` | 热重载通路，零调用；改配置需重启 |
| `profile.Store.Update()` | 同上 |
| `session.Manager.RevokeAll()` | 零调用，无工具暴露"踢出所有会话" |
| `session.Manager.AddUpload()` / `AddDownload()` | 零调用 |
| `session.State.CloseFn` | 从未赋值；且 `func()` 类型**永远无法 JSON 序列化**，是 `novaai_session_list` 返回空响应体的根因 |
| `session.State.PeerUID` / `Concurrency` | 只在创建时赋值，从未读取 |
| `auth.PeerUID` + `peerUIDFromFD`(unix/windows) | 全仓零调用；`middleware.go` 传的字面量 `-1` 也无读取方 |
| `mcp.Server.sseConns` + `SSEEvent` | 初始化后从未注册连接，关机广播遍历空 map |
| `asBool` / `asInt` / `asString` | `helpers.go`，零调用 |

前几轮删除：`config.IsHotReloadable` + `noHotReloadFields`、
`adapter.FallbackChain` + `Attempt` + 5 个 ROM adapter 实现、
`auth.WithPeerUID` + `PeerUIDFromContext`。

## 5. 工具 schema 与实现的对齐

本轮用 `scripts/audit_actions.ps1` 机械对照「schema 声明的 action」与
「handler 实际处理的 case」，处理了 19 个只声明不实现的 action：

| 工具 | 处置 |
|------|------|
| `novaai_task` | **整个工具删除**。5 个 action 全空转：`list` 恒返回 `[]`，`get` 恒返回"任务系统未启动"，其余 `UNKNOWN_ACTION` |
| `novaai_schedule` | 从 schema 删除 `update` `enable` `disable`；描述改为实际能力（脚本存储 + 手动 run），不再声称"一次/周期/Cron/事件触发" |
| `novaai_download` | 删除 `batch` `status` `cancel`（返回 NOT_IMPLEMENTED） |
| `novaai_root_module` | 删除 `action` `backup` `restore` |
| `novaai_transfer_export` | 删除 `task` action 与 `taskId`/`artifactIndex`/`background` 参数 |
| `novaai_audio` / `novaai_property` / `novaai_config` / `novaai_skill` | 各删除 1 个未实现 action（`route` / `reset` / `reset` / `forget`） |
| `novaai_network` | 描述重写：删掉"持久 HTTP/Cookie、HTML 捕获、RSS/Atom、WebSocket"等不存在的承诺；`action` 改用 `enum` 声明 |

`novaai_schedule` 是**通用命令执行载体**（`create` 写脚本 + `run` 用 `sh` 执行），
与 `novaai_shell` 等价，因此已加入 `default` 与 `reverse` 两个 profile 的
`denyTools`。

### 5a. 第二轮审计：审计脚本自身有一个盲点

第一轮的 `audit_actions.ps1` 只做一件事：把 schema 声明的 action 与
handler 里的 `case` 字面量对照。它有一条"逃生舱"：

```powershell
if ($handled.Count -eq 0) { continue }   # 没有任何 case 命中声明 → 当作单动作工具跳过
```

于是"**声明了 action、但 handler 从不读 `in.Action`**"这一整类缺陷被静默跳过 ——
它们不产生任何 `case`，因此永远不会进入对照。这是"检查器的盲点"，比漏掉某一条
更危险：它让审计输出"契约一致"。

本轮把脚本从 1 项检查扩到 5 项，并新增了不依赖正则的 Go 回归测试。

| 新增检查 | 判据 | 本轮命中 |
|---|---|---|
| 摆设 action 字段 | 声明了 action 但代码里没有 `in.Action` | 8 |
| 缺兜底分支 | 读了 action 但没有 `UNKNOWN_ACTION` 出口 | 9 |
| 摆设参数 | schema 参数名在 handler 里没有同名 json tag | 9 |

### 5b. 8 个"摆设 action"字段（已删除）

这些工具声明了 `action`，但 handler 完全不看它 —— 客户端传什么值都得到同一行为：

| 工具 | 曾声明 | 实际 |
|------|--------|------|
| `novaai_shell` | `exec` | 单动作，无分派 |
| `novaai_status` | `get` | 单动作，无分派 |
| `novaai_capabilities` | `get` `probe` | 只探测一次，两个值同义 |
| `novaai_root_info` | `detect` `capabilities` `self_test` | 只返回框架/UID/su 路径，三个值同义 |
| `novaai_app_list` | `list` | 单动作，无分派 |
| `novaai_app_install` | `apk` `split` `apks` `xapk` `session` | 只有 `pm install` 单文件通路 |
| `novaai_app_export` | `apk` `splits` `bundle` | 只导出 `pm path` 的主 APK |
| `novaai_transfer_export` | `file` `directory` | 按 `os.Stat` 判定，与 action 无关 |

处置是删除 `action` 属性（不是补实现）：单动作工具不带 action 是本仓既有约定
（`novaai_device_info`、`novaai_fs_read` 等本来就如此）。相关描述同步收紧，
例如 `novaai_app_install` 不再声称支持 Split/APKS/XAPK。

### 5c. 9 个缺兜底分支的 handler（已修复 panic）

这些 handler 在 `switch in.Action` 里构造 `cmd []string`，然后**无条件**索引
`cmd[0]`。未知 action 让 `cmd` 保持 nil，于是 `cmd[0]` 越界 panic。

`internal/mcp/server.go` 的 `safeCall` 有 `recover`，所以 daemon 不会崩 ——
但用户拿到的是 `工具 novaai_app_policy 内部 panic: runtime error: slice bounds
out of range [1:0]`，一个把真正原因藏起来的错误。删除 `route` / `reset` 这类
假 action 之后，旧客户端发这些字符串就会踩到它。

修复方式与同文件已有的 `novaai_app_manage` / `novaai_app_permission` 保持一致：
在 `switch` **内部**加 `default: return errFail("UNKNOWN_ACTION", in.Action), nil`。
兜底放在 switch 内而不是 switch 后，是为了让"`cmd` 永不为空"这个不变式在索引点
本地可见，而不是依赖后面有人记得补一次检查。

命中的 9 个：`novaai_app_policy` `novaai_default_app` `novaai_notification`
`novaai_display` `novaai_audio` `novaai_connectivity` `novaai_locale_time`
`novaai_input_method` `novaai_developer`。

顺带删掉 `novaai_power` 里一处**不可达**的 `if len(cmd) == 0` 检查：
它的 `switch` 已有 `default`，所有 case 都赋非空值，该分支永远不执行。

### 5d. 9 个"摆设参数"（已删除）

`background` 是重灾区：6 个工具声明了它（`novaai_shell` `novaai_script`
`novaai_archive` `novaai_app_install` `novaai_screen` `novaai_diagnostics`），
**没有任何 handler 读取它**。`netlog.go` 里甚至已经留了注释说这个参数
"连 `json.Unmarshal` 都没接收"，但声明一直留着。

这不只是文档问题：调用方传 `background: true` 会以为命令在后台跑，实际会
同步阻塞到超时。后台执行在本服务里是**按动作**实现的（`novaai_screen record`
与 `novaai_power` 各自起 goroutine），不是一个通用开关；要做成通用能力需要
任务系统，而任务系统本轮已作为不存在的能力删除。因此删除该参数。

其余两个：

| 工具 | 删除的参数 | 理由 |
|------|-----------|------|
| `novaai_app_install` | `package` | 没有"按包名安装"通路 |
| `novaai_app_install` | `paths` | 与 `path` 是同一职责的重复 owner；描述写"多 APK 路径"但 handler 只取 `paths[0]`，多 APK/Split 需要 `install-create`/`install-write`/`install-commit` 三段式，未实现 |

### 5e. 回归测试

新增 `src/internal/tools/v02/actions_test.go`，用**真实 handler**逐个试，不依赖
正则：

- `TestUnknownActionIsRejected`：未知 action 必须"不 panic、不成功"
- `TestMissingActionIsRejected`：完全不带 action 同上
- `TestEveryDeclaredActionIsDispatched`：每个声明的 action 都不能落到
  `UNKNOWN_ACTION`
- `TestToolsAreWellFormed`：工具名唯一、有描述、schema 是 object、
  `required` 里的名字都在 `properties` 中

判据刻意是"不 panic 且不成功"，而不是"必须返回 `UNKNOWN_ACTION`"：部分 handler
把参数校验放在 action 分派之前（`novaai_script` 缺 `script` 时返回
`MISSING_SCRIPT`），那是合理顺序，不该被判失败。测试另用 `panicError` 把
"handler 主动 `return err`"（如 `novaai_config get` 读不到 config.json）与
"panic"区分开。

## 6. 会话与限流的残余限制

> 本节第 3 条已在本轮失效：按 token 哈希计数的第二层限流已删除。

- 会话数上限（32）达到后返回 `-32014`。只有带 `Mcp-Session-Id` 或含
  `initialize` 的请求占用名额，无状态客户端不消耗。但**真正创建 32 个会话
  的客户端**仍会被挡到空闲超时（30 分钟）。
- `http.Server.WriteTimeout` 为 0（无上限）。单个慢客户端可以长期占住连接；
  这是为了让长时工具调用不被中断。未做按工具区分的写超时。
- ~~限流第二层按 token 哈希计数，无 token 的入口共用 `local` 桶~~ ——
  **已删除**：所有来源本来就是同一个身份（无 token、不分来源），
  多一个维度只是多一处可被误读的状态。现在只有全局 / shell / 并发三层。

## 7. 版本控制

仓库已初始化 git（首次提交 `b1e1440`），`.gitignore` 排除 `dist/`、
`.backup/`、`bin/*/novaaimcpd`。

- `.gitattributes` 关闭了所有 EOL 转换（`* -text`）。**不要移除它**：
  `core.autocrlf` 会把 `.sh` 转成 CRLF，导致模块在 Android 上无法运行。
- 仓库级 `core.autocrlf=false` 已设置，但这是本地配置，克隆到新机器后
  需要重新设置（`.gitattributes` 的 `-text` 已能兜住，设不设都不影响正确性）。
- `.backup/` 里的手工快照是 git 接管之前的回滚手段，现已冗余，未删除。
- 远端 `origin` 已配置为 `https://github.com/yuanxing109/NovaAI-MCP.git`。
  本轮**只做本地提交，未推送**：提交与推送是两件事，推送即公开发布。

## 8. 文档

- 第一轮已同步 `config.md` / `config.example.json` / `extensions.md`
  与代码（删除的字段、schema 变化、`novaai_tasks` 能力移除）。
- 第二轮同步：`README.md` 的工具分类表（原文按 11+1 归"服务/状态"与
  "设备调度"，与实际 9+2 不符，且漏了"技能"一行；总数 61 是对的）、
  `migration.md`（第三批移除键 + 升级检查清单第 6/7 条）、
  `docs/config.md` 与 `config.example.json`（`paths.crashDir`、`capabilities`
  两个字段移除）。
- `docs/extensions.md` 第 2.3 节原文声称"服务端不会再为已移除的 action 返回
  `UNKNOWN_ACTION` —— 它们在 schema 层就被拒绝"。这是**错的**：服务端不校验
  `inputSchema`。已改写为如实描述（schema 是给客户端的契约，服务端靠 handler
  的兜底分支），并指向本节 5c。
- `docs/extensions.md` 第 1.1 节保留了一条"`novaai_tasks` 已移除"的说明，
  便于按旧文档对接的客户端定位问题。
- 第三轮同步：`README.md` 的安装步骤原文是 `[NovaAI-MCP-v0.05.zip](releases)`
  （死链），已改为指向真实 Releases 页并用 `NovaAI-MCP-v*.zip` 表述以避免
  版本漂移；`YOUR_USERNAME` 占位符替换为真实仓库地址；许可证段指向 `LICENSE`。
- 新增 `LICENSE`（MIT，署名 `NovaAI-MCP`），并**加入两个构建脚本的 staging
  清单** —— MIT 要求版权与许可声明随软件副本一同提供，而本 ZIP 就是一份副本。
  打包条目数因此由 50 增至 51。
- `build.sh` 里"`bin/tools/*.jar` 安装到状态目录"的注释已改为"随模块分发"，
  与第 10 节的 jar 单副本收敛一致。

## 9. 回归工具

三个探针脚本都是**自举**的：自行构建 daemon、生成隔离 state 目录、结束时清理，
不需要设备，也不依赖本机 `go` 在 PATH 上（用 `-GoExe` 指定）。
**本服务不鉴权**，所以三个探针都不带认证头（旧的 `-Token` 参数已删）。

| 脚本 | 覆盖 |
|------|------|
| `scripts/probe_mcp.ps1` | 协议合规：initialize 协商、通知无响应体、tools/list 数量、content 包装、错误码、批量、无鉴权通路、Host/Origin、限流、会话复用、default 档位放行、pathguard（含 Android/data 硬拒绝） |
| `scripts/probe_session.ps1` | 会话五类行为：分配与复用、无状态请求不占名额、上限 32 生效、会话不携带权限、批量隔离 |
| `scripts/probe_limits.ps1` | `resultPreviewBytes` 截断语义与 `=0` 不限制 |
| `scripts/audit_actions.ps1` | 静态契约审计（5 项检查，见第 5 节） |
| `scripts/audit_shell.ps1` | 模块侧静态审计（8 项检查，见第 10 节） |
| `scripts/verify_package.ps1` | 模块 ZIP 产物的权限位与宿主字段（唯一实现，见第 1 节） |

> **本机实测结果（2026-09-21，Windows / pwsh 7.6.6）**
>
> | 脚本 | 结果 |
> |------|------|
> | `probe_mcp.ps1` | **47 PASS / 0 FAIL** |
> | `probe_session.ps1` | **10 PASS / 0 FAIL** |
> | `probe_limits.ps1` | **8 PASS / 0 FAIL** |
> | `audit_actions.ps1` | 全部通过（exit 0） |
> | `audit_shell.ps1` | 全部通过（8/8） |
> | `verify_package.ps1` | 通过（61 条目 / 19 可执行 / host=3:61） |
>
> 这六个数是**本机真跑出来的**，不再是"以 CI 为准"的占位。

> **上一轮那句"本机只有 Windows PowerShell 5.1、跑不了 .ps1"是错的。**
> `pwsh` 7.6.6 一直装着，只是落在 `WindowsApps` 应用执行别名里
> （`%LOCALAPPDATA%\Microsoft\WindowsApps\Microsoft.PowerShell_8wekyb3d8bbwe\pwsh.exe`），
> 没有被 Bash 工具的 PATH 解析到，于是当时据"`Get-Command pwsh` 没结果"下了结论 ——
> **那一步没有验证到底，是"无法验证"被当成了"不存在"**。
>
> 与之一并纠正的还有两条由此衍生的做法：
>
> - 当时只用"UTF-8 读取 + 解析器"校验脚本语法，并另写临时 bash 脚本打
>   `curl` 来代替探针。**探针能跑就不该用替代品** —— 替代品只覆盖协议面，
>   覆盖不了 `probe_session` 的会话上限、`probe_limits` 的截断语义这些
>   需要精确构造请求的项。
> - 由此得出的"两个一致性缺口"结论里，有一条（探针未验证）已作废；
>   另一条（`--state` 与 `config.stateDir` 两个 owner）仍然成立。
>
> **仍然成立**：`powershell.exe` 5.1 直接执行 BOM-less UTF-8 的 `.ps1`
> 会语法崩（本机实测 7/7 全部报错，中文注释吃掉紧随其后的引号）。
> 所以跑这些脚本**必须用 `pwsh`**，不能退回 `powershell.exe`。
> 详见第 15.2 节。

另有一组 Go 测试承担脚本覆盖不到的部分：

- `src/internal/tools/v02/helpers_test.go` —— `shellTimeoutSeconds` 是默认超时的
  唯一来源（Windows 无 `/system/bin/sh`，无法端到端观察超时）。
- `src/internal/tools/register_test.go` —— 本地工具总数断言（**30**），
  以及"被裁掉的工具不允许留在注册表里"的负向清单。
  上游工具**不**计入（数量随配置变化）。
- `src/internal/mcp/no_auth_test.go` —— 无鉴权通路与 Host/Origin 拒绝边界。
- `src/internal/mcp/guard_e2e_test.go` —— Host/Origin + pathguard 的端到端。
- `src/internal/mcp/upstream_test.go` —— tools/list 合并、tools/call 转发、
  上游策略拒绝转 isError、`-32015` 边界、`novaai_upstream_status`。
- `src/internal/upstream/*_test.go` —— 上游包本体（22 个用例）：
  HTTP 连接、stdio spawn 与超时杀进程、命名空间与最长前缀路由、
  四态探测、断开隔离、热重载与子进程回收、exposeWhenStopped、autoLaunch、
  denyTools / riskCeiling。
- `src/internal/session/manager_test.go` —— 会话对象**不含 Profile 字段**
  （会话不携带权限）。
- `src/internal/profile/profile_test.go` —— `default` 放行全部工具。
- `src/internal/pathguard/pathguard_test.go` + `android_data_test.go` ——
  硬拒绝前缀与 `/sdcard/Android/{data,obb}` **硬拒绝**（无确认放行）。
- `src/internal/config/{validate,paths,example,upstream}_test.go` ——
  配置字段与 `docs/config.md` 逐键一致、上游配置的启动校验、
  以及 `docs/upstream.md` 示例与 `UpstreamConfig` 逐键一致且**能通过 Validate**。
- `src/cmd/novaaimcpd/main_test.go` —— PID 文件必须记录**本进程** PID（0600），
  且文件名与 `common.sh` 的 `ZCR_PID_FILE` 一致。
- `src/internal/tools/v02/actions_test.go` —— 未知/缺失 action 的负向回归。

> `audit_actions.ps1` 只扫描 `src/internal/tools`，因此它的"扫描工具数"
> 是 28（v02 全部），不含 `internal/tools` 下用 `reg.Register(&Tool{...})`
> 直注册的 2 个（`novaai_health_status`、`novaai_upstream_status`）。
> 工具总数以 `register_test.go` 的断言为准（30）。

## 10. 模块生命周期脚本（第三轮清理）

模块的安装 / 启动 / 看门狗 / 卸载脚本此前从未被系统性审过。本轮按"是否存在
真实行为"逐项判定。

### 10a. 已删除（零读者 / 零调用）

| 对象 | 位置 | 判据 |
|------|------|------|
| `manual-stop` 停止标志 | `uninstall.sh` | **全仓零读者**；真正生效的是 Magisk 自己的 `remove` 文件（`service.sh` 已读） |
| `zcr_start_supervisor auto` 实参 | `service.sh` ×3 | 函数体从不读 `$1`，没有任何模式逻辑 |
| `zcr_print_summary` | `common.sh` | 零调用；`action.sh` 已在打印更全的摘要 |
| `module_path` / `version` / `install_time` | `customize.sh` | 三个文件都是只写不读 |
| `ZIPFILE=${ZIPFILE:-$3}` 回退 | `customize.sh` | 该脚本由 `install_module` source，位置参数不是 ZIP 协议那三个；且 `ZIPFILE` 已由 `update-binary` export。回退分支永远走不到 |
| `follow` 参数 | `netlog.go` | 声明了、也读了，然后**恒定**返回 `NOT_IMPLEMENTED` |

### 10b. 已修复

| 项 | 修法 |
|----|------|
| PID 文件记的不是 daemon 的 PID | daemon 自己写 `$stateDir/novaaimcpd.pid`（0600），shell 只读；`zcr_read_pid` 用 `/proc/<pid>/comm` 校验身份后才返回，防 PID 复用误杀 |
| `apktool.jar` 在设备上存两份 | Go 侧改为从可执行文件位置推导 `<mod>/bin/tools/apktool.jar`；`customize.sh` 不再复制到状态目录，并在升级时删除旧版留下的冗余副本 |
| `update-binary` 硬编码框架路径 | 改为运行时发现：找第一个**确实提供 `install_module`** 的 `util_functions.sh`（先 grep 再 source） |
| 版本号 4 处硬编码 | 收敛到 `module.prop`：`build.sh` / `build.ps1` / `action.sh`（经 `common.sh` 的 `zcr_module_version`）/ `customize.sh` 全部读取 |
| `build.sh` 漏 chmod `7zz` | 见第 1 节 |
| `action.sh` 说"WebUI 中点击此按钮" | 无 `webroot/`，也没有 WebUI；文案已改 |
| `action.sh` 把"首个 `enabled": true"标成 LAN 开关 | 那个位置可能是 `audit.enabled`；标签改为"首个 enabled=true"，逻辑不变 |

### 10c. PID 归属：一个**未在真机证实**的假设

`common.sh` 原先写 `echo $! > "$ZCR_PID_FILE"`，而 `$!` 是 **`su` 进程**的 PID。
Magisk 的 `su -c` 是 fork 出子进程再 wait，所以真正的 `novaaimcpd` 是孙进程 ——
若如此，`zcr_stop_supervisor` 的 `kill -TERM` 到不了 daemon，卸载后它可能仍占着
`:5322` 与 `mcp.sock`（socket 文件却被 `uninstall.sh` 删了），重装时新 daemon
绑不上端口。

**这是假设，不是结论**：本仓没有设备，也没有 shell 侧测试台架，无法在开发机上
证伪。但无论假设是否成立，**"daemon 自己写 PID"都是更正确的归属** —— 原先
daemon 完全不写 PID 文件（全仓 `*.pid` 零命中），停止功能完全依赖一个语义不明确
的 `$!`。所以修复本身不依赖这个假设成立。

真机验证清单第 1 项：安装后卸载，确认 `ss -tlnp | grep 5322` 为空。

### 10d. 仍未验证 / 未做

- **模块生命周期从未在任何真机或模拟器上执行过**：`customize.sh` / `service.sh` /
  `uninstall.sh` / `action.sh` 都没有自动化台架。`audit_shell.ps1` 只做静态比对，
  它全绿**不代表模块装得上**。
- **APatch 支持仍未证实**：`update-binary` 现在按发现规则工作，比枚举路径更可能
  覆盖 APatch，但没有任何 APatch 设备验证过。若 APatch 不提供
  `util_functions.sh`，安装仍会失败（现在会打印已查找的路径，便于定位）。
- **G4 的 jar 路径需要真机确认**：`os.Executable()` 在 Android 上读
  `/proc/self/exe`，推导出 `<mod>/bin/tools/apktool.jar`。逻辑上正确，但
  `java -jar` 能否真正跑起来还依赖 Termux 的 `java` 存在，未在设备上跑过。
- **`action.sh` 不真正解析 JSON**：只 grep 第一个 `"enabled": true`，标签已如实
  写明是"首个"，但没有结构化读取。
- **`audit_shell.ps1` 的边界**：它排除注释行与 `.md` 文档，也排除自身
  （`scripts/audit_*.ps1` 必须写下这些符号才能检查它们）。这意味着
  **检查器不能自我校验**，且它无法判断"读了参数但什么也不做"这类语义缺陷 ——
  `follow` 就是这一类，只能靠 Go 测试兜。
- 第 8 项只覆盖 `.github/workflows/*.yml` 自身，且只查 `go build` / `zip -r`
  两种重新实现形式。工作流 `run:` 里调用的脚本（探针会自己 `go build`）不在
  此列；构建步骤若被删到只剩 `name:` 提到 `build.sh`，第 8 项会失败。

## 11. 发布流水线（第四轮新增）

`.github/workflows/release.yml`：push 到 `main` 时若 `module.prop` 的版本还没有
对应 Release，就自动建 tag 并发布；push tag 要求与 `module.prop` 一致；
PR 只跑门禁不发布。完整说明见 [CI.md](CI.md)。

五个 job：`verify`（ubuntu）· `build`（ubuntu）· `regression`（windows）·
`release`（ubuntu，稳定通道）· `dev`（ubuntu，预发布通道）。
两个发布 job 的 `needs` 都是 `[build, regression]`，所以审计或探针失败时不会发布。

### 11a. 预发布通道（第六轮新增）

起因是一次真实的不一致：`ab0c368` 把安全修复推上 `main`，流水线四个 job 全绿，
但 **Release 里仍然是旧产物** —— 因为 `module.prop` 的 `version` 没人动过。
"稳定版版本号由人决定"（`CI.md:14`）这条规则要保留，于是新增了一条与其**互斥**的通道：

| | 稳定通道 `release` | 预发布通道 `dev` |
|---|---|---|
| 触发 | 该版本**尚无** Release | 该版本**已有**稳定 Release |
| tag | `v0.05` | `v0.05-dev.9` |
| 标记 | 正式版 | `--prerelease` |

每次 push 到 `main` 最多只有一条通道动作。**首次 push 时只有稳定通道生效**，
dev 通道从第二次起才有输出。

序号取 `git rev-list --count HEAD`，**不是** CI run number：commit 数是仓库的
先天性质，本地跑同一段脚本能得到与 CI 相同的数字，可复现、可审计。

代价是两条分支上同一个 commit 会算出同一序号。GitHub 的 tag 不可移动，
直接建同名 tag 会让 `gh release create` 失败并把 `dev` job 判红。脚本因此先查
"同版本已发布的最大序号"，`serial <= last` 时取 `last + 1`，把失败模式变成一条 `if`。

**为什么必须"先稳定、后预发布"**：GitHub 上 tag 是全局命名空间，`v0.05` 与
`v0.05-dev.9` 是同一空间里的两个名字。真正的理由是精确性 —— `v0.05` 必须指向
"就是那个稳定构建"的 commit，不允许出现"`v0.05` 到底对应哪次构建"的含混。
判据用 `gh release view` 而不是查 tag ref：只建 tag 未发 Release 的中间状态会误判。

### 11b. 审计脚本自身的第二个盲点（第六轮修）

给第 4 项加的 dev 通道判据里，有一条是"`--prerelease` 必须存在"。它**第一次
写出来就是坏的**：判据对整份文件做 `-match`，而**本项自己的说明注释**里就写着
`` `--prerelease` 已加 ``，于是把 `--prerelease` 整行删掉，检查照样通过 ——
一条永不失败的检查。

这是与 5a 同类但机制不同的盲点：5a 是"逃生舱 `continue` 让整类缺陷不进入对照"，
这里是"检查对象的文本被检查自身的说明文字满足"。**变异测试**（删掉被检查的行，
审计必须 FAIL）是对付它的唯一可靠手段 —— 靠读代码看不出来。

现在改为先剥注释行再做判据（与第 1/8 项同一套 `Test-IsCommentLine` 策略），
四条判据全部经过变异测试：删 `--prerelease`、换掉序号来源、删序号查询、
去掉 `dev` job —— 四种变异**都正确 FAIL**，还原后通过。

### 11c. 一次被自己推翻的判断（记录）

`--prerelease` **初版是故意不加的**，理由写成"会给第 8 项造成假失败"。
实测该理由**不成立**：第 8 项只扫描 `build.sh` / `go build` / `zip -r` 字样，
`--prerelease` 三者都不含，注入后审计依旧全绿。于是改为加上该参数。

记录在此是因为它是本仓库反复出现的同一类错误的前身：**未经检验就写进"刻意决定"**
的因果解释。`CI.md` 的"几个刻意的决定"一节每个条目都该有一个可复现的验证，
否则它就是一段散文。

**仍未验证**：

- **`build.sh` 从未在 Linux 上真正执行过。** 开发机是 Windows，此前只有
  `bash -n` 语法检查与静态比对。CI 的第一次 `build` job 是它第一次真跑；
  若 Info-ZIP 的宿主字段行为与预期不同，`verify_package.ps1` 会失败并打印实际值。
- **`bash -n` 不等于模块装得上**：它只证明语法可解析。`customize.sh` /
  `service.sh` / `uninstall.sh` / `action.sh` 仍无自动化台架（见第 10d 节）。
- **五个 `scripts/*.ps1` 没有移植到 Linux。** 它们用 `$env:TEMP` 与
  `Start-Process -WindowStyle`，在 Linux 上的行为未经验证，因此 CI 把它们放在
  `windows-latest` 上跑（已知全绿：26/26、13/13、8/8、审计全 0）。
  移植到 Linux 是一件独立工作：需要替换临时目录、去掉 `-WindowStyle`，
  并在容器里验证三个探针仍全绿。
- **dev 通道的序号推导只做过离线模拟，没有在真实 Actions 上跑过第二轮。**
  首次触发只能证明"稳定版已发布时不动作"这一支；`last + 1` 的递增分支要等
  第二次 push 才被真正执行。这是本轮最需要真机验证的一点。
- **CI 无法验证真机安装**，也不做签名。
- `GO_VERSION` 用的是 `stable`（`go.mod` 的 `go 1.22` 是语言下限，不是工具链）。
  要固定工具链需改工作流。

## 12. 第五轮：授权边界与只读根

本轮改动集中在三类问题上，都**只在 Windows 开发机上才会显现**或**只在
配置组合错误时才触发**，因此此前无人发现。

### 12a. profile 回退曾经是 fail-open（已修）

`profile.Store.Get` 在名字不存在时返回一个**凭空构造**的 profile：
`{AllowTools: ["*"], RiskCeiling: 1}`。

同一个缺失条件，`middleware.go` 的 `profileFromContext` 用的是另一套语义
（回退到名字 `"default"`）。两个 owner、两个答案。

危害是具体的：`readonly` 的 `riskCeiling` 是 **0**，而构造出来的是 **1**。
`novaai_fs_write` / `novaai_download` / `novaai_transfer_upload` 都在 risk 1，
所以 `sessionBinding` 里把 `readonly` 打成 `redonly` 会把只读身份**提升**为可写。

现在：回退到 `default`（真实存在、可审计），并且在 `config.Validate` 里
**启动即拒绝**悬空的 profile 引用 —— 拼写错误不该留到运行时才静默换档位。

### 12b. 危险安全组合无校验（已修；其后随开关一并删除）

> **第八轮补记**：本节列的三个组合（`anonymous` / `validateHost` /
> `validateOrigin` / `allowCors`）**已全部不存在** —— 那些开关、以及
> `Validate` 里的组合校验，在精简重构中一起删掉了。配置里已经没有可以
> "组合出错"的字段（见 [config.md](config.md) 的「非法配置」一节）。
> 本节保留的是当时的判定依据：**组合校验不可能靠逐字段检查发现**。

`Validate` 原本只查 4 件事。它**已经**正确地拒绝了一个危险组合
（`lan.enabled` + `!token.enabled`），但对同类组合完全缺失：

| 组合 | 后果 |
|------|------|
| `anonymous` + `!validateHost` | DNS rebinding 后浏览器与端口同源，失去唯一来源校验 |
| `anonymous` + `!validateOrigin` | 任意网页可跨源盲打 root 工具 |
| `allowCors` + `!validateOrigin` | CORS 反射任意 Origin，网页可带 token 全权访问 |

第二条的关键在于服务端**不检查 Content-Type**（`middleware.go` 只按字节
解析 JSON）。`text/plain` 的请求因此是"简单请求"，不触发 preflight，
会被真正发出并执行；攻击者读不到响应，但 root 工具的副作用不需要读响应。

单独关 `validateOrigin`（不开 anonymous、不开 CORS）仍然允许。

### 12c. `filepath.Join` 用于 Android 路径（已修）

`default.go` 与 `migrate.go` 用 `filepath.Join` 拼接 `/data/adb/novaai-mcp`
下的子路径。在 Linux/Android 上与 `path.Join` 等价，所以问题在设备上不可见；
在 **Windows 开发机**上它会产出 `\data\adb\novaai-mcp\workspace`。

而 `pathguard.Normalize` 对不以 `/` 开头的输入**原样返回**，于是这条路径被
当成相对路径，受保护判定静默失效 —— 不报错，只是不再保护。

已全部改为 `path.Join`（`default.go` 3 处、`migrate.go` 1 处），
并在 `config/paths_test.go` 里锁住"配置里的路径必须以 `/` 开头且不含反斜杠"。

**这个 bug 是被新测试抓到的**，不是靠 review：把 `config.example.json` 并入
`config.md` 时顺手加了一条"示例取值必须与 `Default()` 一致"的断言，
它当场报出三处路径不一致。

### 12d. `/sdcard/Android/{data,obb}` 此前**完全可写**（本次新增保护）

> **第八轮变更**：本节的"可确认档"已取消。该位置现在是**硬拒绝**，
> `confirmDangerous` 参数已从所有工具 schema 与 handler 中删除。
> 理由与影响见第 13.5 节。下面保留的是"为什么需要保护它"的推理。

`pathguard` 里原本没有任何一条 `Android/data` 规则。三层判定
（`fixedDeny` / `criticalRoots` / `stateDir`）对
`/sdcard/Android/data/com.x/` 全部不匹配，因此 `novaai_fs_write`、
`fs_manage remove`、`archive` 都能直接删改它。

新增第四层「只读根」：读放行，变更默认拒绝，`confirmDangerous: true` 后放行。
硬拒绝位置（`/system`、`/data/adb/modules`…）**不受确认影响** ——
否则确认就成了万能钥匙。

覆盖全部别名（`/sdcard`、`/storage/emulated/0`、`/data/media/0`、
`/mnt/sdcard`）。这不是冗余：`/sdcard/Android/data` 是指向
`/storage/emulated/0/Android/data` 的符号链接，只写一条会被另一条绕过，
而绕过是静默的。

**已知残余风险**：

- **`rm -rf /sdcard/Android` 未被拦截**。它不在 `criticalRoots` 里，因此
  会连带清空 `data` 与 `obb`。这是一个真实的缺口，但修它需要改动
  `criticalRoots` 的语义（那个集合的成员当前都可以被整体递归删除，
  只要不命中 `fixedDeny`），影响面超出本次范围。已用
  `TestAndroidDirItselfIsWritable` 记录现状，不会让它悄悄溜过去。
- **`confirmDangerous` 是模型自己填的布尔值**，无法证明真的发生过用户判断。
  真实客户端通常把它渲染成需要人点确认的提示，但那是客户端的善意，
  不是服务端的保证。因此这一层提供的是"默认不会误删别的应用的数据"，
  **不是**"对抗已沦为攻击者的模型"。对抗后者要靠 profile。
- ~~**`archive` / `transfer_upload` / `download` 尚未接入可确认档**。~~ ——
  **不再适用**：可确认档已取消，这些工具对 `Android/data` 的写入与其他
  位置一样是硬拒绝（`PROTECTED_PATH`），不再有"待补齐 confirmDangerous"
  这个待办。

### 12e. 读取守卫（本次新增）

`novaai_fs_read` 在此之前**没有任何路径守卫**，`cat /dev/block/by-name/boot`
会把整个分区的原始字节塞进工具结果。

新增 `pathguard.CheckRead`，但刻意**不复用 `fixedDeny`**：那个集合回答的是
"改了会不会开不了机"，包含 `/data/adb/modules`；而**读** `module.prop`
正是排查模块问题的正常手段。用写入规则去限制读取会砍掉真实能力。

因此读取只拒绝 `/dev/block` 与 `/proc/sys`。这组用例在开发中被新测试
当场纠偏过一次 —— 初版复用了 `fixedDeny`，被
`TestCheckReadAllowsEverythingOrdinary` 判为过度收紧。

### 12f. 未做的事（已失效）

- ~~**限流没有删，也不建议删。**~~ —— 有效。`qps <= 0` 与
  `maxConcurrent <= 0` 仍是"关闭"开关（`bucket.allow` 与 `AcquireSlot`
  都直接放行），改配置即可，不需要动代码。
- ~~**token 认证对局域网不可关闭。**~~ —— **已失效**：token 与 `lan.enabled`
  已整体删除，本服务不再鉴权。见第 13 节。

---

## 13. 第八轮：精简重构的残余风险（当前状态）

本轮删掉了 token、LAN 区分、来源判定、多 profile、`confirmDangerous`、
配置迁移、按身份的限流层，工具从 61 个裁到 29 个（第九轮因上游聚合
又加回 1 个观测工具，现为 **30**）。
**权限边界因此完全落在"网络可达性"上。** 以下六条是接受的代价，
不是待修的缺陷。

### 13.1 同网段任何设备可 root shell

默认 `listen: "0.0.0.0:5322"`，**无鉴权**。同一个 WiFi 上的任何设备 ——
家里的 IoT、访客手机、被入侵的路由器 —— 都能调用 `novaai_shell`，
那是一个以 root 身份运行的任意命令执行口子。

**前提是家里 WiFi 可信、设备不暴露公网。** 这是使用前必须确认的唯一一件事。

### 13.2 不要暴露公网

- 不要在路由器上做 `5322` 的端口转发。
- 不要在咖啡厅 / 酒店 / 公司 / 展会 WiFi 上开着它。
- 想只给自己用：`listen` 改成 `127.0.0.1:5322`（本机 + 数据线可达），
  或只留 Unix socket。

### 13.3 `anonymous` 配置项已移除，所有来源一视同仁

不再区分 loopback 与局域网，也不再有"本机免 token、局域网要 token"这条
曾经的权限边界（ADR-004 的方案已废弃）。Host/Origin 是**唯一**还在做的
来源校验，而它只挡浏览器。

### 13.4 shell 是万能绕过

`pathguard` 与 `antibrick` 只防手滑，不防恶意调用方。只要 `novaai_shell`
可达，`P=/system; echo x > $P/build.prop`、`base64 -d | sh`、
`echo / | xargs rm -rf` 都能绕过它们。`antibrick` 头部已写明**冻结**，
不再为新的绕过形式追加规则。

### 13.5 `confirmDangerous` 已移除，`androidDataRoots` 为硬拒绝

`/sdcard/Android/{data,obb}`（及其全部别名）现在**硬拒绝**，没有任何
确认放行通道。这是有意的：`confirmDangerous` 是模型自己填的布尔值，
无法证明真的发生过用户判断，而 shell 可达时它拦不住任何有动机的调用方 ——
保留它只增加状态与心智负担。

**确需访问 `/sdcard/Android/{data,obb}`，走 `novaai_shell`。**
可见性与安全性在这里是**分开**的：通用文件/归档工具被挡，shell 不挡。

### 13.6 Host / Origin 校验不防直接内网访问

`hostMiddleware` 只校验 Host 头是不是 IP 字面量或 `localhost`，
**不校验来源 IP**。它防的是浏览器 DNS-rebinding（恶意网页把你的域名
解析到 `127.0.0.1`），**不能阻挡 Python / Go / curl 直接用内网 IP 访问**。

后者已在 13.1 里被明确接受。

### 13.7 本轮引入的两处一致性缺口（记录）

- **`--state` 参数与 `config.stateDir` 是两个 owner。** 审计目录
  （`cfg.AuditDir()`）与 `pathguard` 的保护前缀取自 `config.stateDir`，
  而 PID 文件、崩溃目录、工作目录取自 `--state`。设备上二者默认同值
  （`/data/adb/novaai-mcp`），因此不可见；但在开发机上会产生
  "配置说一个目录、进程用另一个"的现象。**未修**（不在本轮范围内）。
- **`skills/*.md` 已无工具读取。** `novaai_skill` 被裁掉后，随模块分发的
  5 个技能文档不再有消费者，只能由人或 `novaai_fs_read` 直接读。
  本轮已把其中的工具名更新到现有工具集（否则会教客户端调用不存在的工具），
  但**这批文件是否保留本身需要一次决策**：要么删掉、要么给它们一个新的
  入口（例如并入 `instructions`）。第 13.7 条与这一条都属于"重构留下的
  半成品"，不是 bug。

---

## 14. 第九轮：上游 MCP 聚合 + KernelSU WebUI

本轮把服务从"一个工具箱"扩成"聚合网关"：新增 `internal/upstream/`
（HTTP / stdio 两种上游、四态状态、命名空间合并、路由转发、懒启动）与
`webroot/`（KernelSU 页面）。工具数 29 → **30**（新增
`novaai_upstream_status`）。

### 14.1 上游的安全边界（必须知道的三条）

1. **上游 MCP 的安全性由上游自己负责。** 本服务只转发，不覆盖上游的鉴权，
   也无法验证上游声称的工具描述是真的。
2. **`stdio` 上游是以 root 身份 spawn 的任意可执行文件。** 与
   `novaai_shell` 同级的能力 —— 能配一个 stdio 上游的人本来就能拿到 shell。
   `launch.command` 直接 spawn（不经 shell、参数逐个传递），所以配置内容
   不会变成注入点；但 `launch.intent` 会调 `am start`，同样等价于一次
   任意命令执行。
3. **上游工具继承全局 `default` 档位**（放行）。上游级控制只有
   `denyTools` 与**粒度有限**的 `riskCeiling`：

   `riskCeiling` 比较的是 `profile.ResolveRisk(工具名, action)` 推断出的
   等级，而**未知工具名一律算 1**。所以 `riskCeiling >= 1` 等于没有限制，
   `riskCeiling = 0` 又被当成"继承默认 3"（Go 的 int 无法区分 0 与缺省）。
   **真正有效的上游级控制是 `denyTools`。** 这是本轮最容易被高估的一条，
   写在 [upstream.md](upstream.md) 的字段表里。

### 14.2 未运行的上游：工具表与状态是两件事

`exposeWhenStopped: true` 时，暴露的是**上一次成功探测**拿到的工具列表 ——
停止的服务不可能回答 `tools/list`。因此：

- `status` 反映当前存活，`tools` 反映最后已知的 schema，**两者不共用一个
  赋值**。第一版实现在探测失败时把 `e.tools` 一起清零，导致
  "停过一次之后工具列表再也回不来"，被 `TestUpstream_ExposeWhenStopped` 抓到。
- **从未成功探测过的上游，即使开了 `exposeWhenStopped` 也没有工具可暴露**
  —— 不能凭空编造 schema。这不是缺陷，是定义。

### 14.3 拉起的进程不由本服务托管

`launch` 的语义是"把它拉起来"，不是"由我托管"。因此：

- autoLaunch 起的进程不会随 daemon 退出而回收；
- 它可能被 LMK 杀掉，那时状态会变回 `stopped`；
- 这也意味着**测试里必须自己让假上游退出**，否则它会一直占着端口与
  测试二进制的文件句柄（Windows 上表现为 `go test` 收尾时 unlinkat 失败）。
  `fake_test.go` 的 `/__exit` 端点就是为此存在的。

### 14.4 WebUI 的已知限制

- **只在 KernelSU 上可用。** Magisk / APatch 没有等价的模块页面机制；
  页面会显示"未检测到 KernelSU 桥"的横幅并降级为只读浏览。
- **不显示风险等级。** 风险等级按既定契约不在 `tools/list` 里声明
  （见 [extensions.md](extensions.md) 2.2），页面复刻一份等于制造第二个
  owner。这是**有意接受的与方案原文的偏离**：方案要求工具目录显示风险等级，
  但那样做要么改协议契约、要么复制风险表，两者都比"不显示"更糟。
- **不做后台轮询。** 页面上的状态是快照，需要手动点「全部探测」。
- **`file://` 下不用 ES module。** 静态 `import` 可能被 CORS 拦掉，
  所以 `webroot/` 全部是经典脚本（挂全局），对 `kernelsu` 的导入是
  运行时动态尝试。
- **配置写入用单引号 heredoc。** 实测 `$HOME` 与 `` `id` `` 原样落盘、
  未被求值；含真实换行的序列化结果会被拒绝（那样 heredoc 的终止条件不再可靠）。
- **添加表单的校验是双份的**：页面那份只为人话提示，真正把关的是
  `config.Validate`，两边规则不完全重合。

### 14.5 本轮抓到的两个缺陷（记录）

- **路由漏洞：`fake__` 被当成合法工具名。** `SplitName` 只检查了前缀，
  没检查分隔符之后的工具名是否为空，于是 `fake__` 会带着一个空名字
  一路转发给上游。修在 `merge.go`（空工具名直接判为"不是上游工具"），
  由 `TestUpstream_RouteByPrefix` 与 `TestServerUnknownUpstreamPrefixIsToolNotFound`
  双向锁住。
- **文档契约测试被自己的散文骗过。** `docs/upstream.md` 的说明文字里
  写了围栏标记本身，而取示例的 `extractJSONBlock` 只看"第一段 ```json"，
  于是取到了半截散文，报错是 `invalid character '代'`。这与
  KNOWN_ISSUES 第 5a / 11b 节是同一类（**检查器被检查对象以外的文本满足**）。
  现在提取逻辑改为"取第一段**内容是合法 JSON** 的围栏"，
  `config.md` 与 `upstream.md` 共用它。

### 14.6 工具级 code 的"中文名"机制（本轮新增）

工具级 `code`（`PROTECTED_PATH` 这类）保留英文 —— 它是给**程序**匹配的
标识符，改了就是破约，下游（探针正则、别的语言写的客户端）全要跟着动。
但读日志的人和 AI 需要看得懂，所以每个结果额外带一个稳定中文名：

```json
{"success": false, "code": "PROTECTED_PATH", "codeName": "路径受保护", "message": "..."}
```

唯一声明点是 `src/internal/tools/v02/codes.go` 的 `codeNames`（60 条），
四个出口（`ok` / `okMsg` / `errFail` / `execResult`）自动附加。
**数字错误码不参与** —— `-32015` 这类来自 JSON-RPC 2.0 与 MCP 生态的约定。

`codes_test.go` 盯三个方向，并且**每个方向都做过变异测试**
（删登记 / 加未登记 code / 改文档中文名 / 删文档行 / 加死条目 /
给出口去掉装饰 —— 六种变异全部如期 FAIL，见第 9 节的说明）：

| 方向 | 后果 |
|---|---|
| 代码里有 code 没登记 | 结果少 `codeName` |
| 词表里有死条目 | 后来者照着它写分支 |
| `docs/errors.md` 第 3 节与词表不一致 | 文档在教不存在的 code |

顺带删掉了 `internal/tools/errors.go`：它的 `fail()` **零调用**，而且是
`code` 的**第二个生产点** —— 留着它，将来谁用了它，那个结果的 code 就会
静默绕过中文名表。同类的三处手写 `"code": "OK"` map（`app.go` 一处、
`exec.go` 两处）收敛成了 `execResult`。

> **`extensions.md` 4.3 里那张 code 表已删。** 它和 `errors.md` 第 3 节
> 是同一份清单的两个 owner，没有任何机制保证同步。现在只讲字段语义 +
> 指向 `errors.md`。

### 14.7 仍未做

- **上游聚合没有真机验证。** 本机（Windows）用测试二进制扮演上游跑通了
  HTTP 与 stdio 两条路（见第 9 节的说明），但 `launch.intent` 需要
  Android 的 `am`，**完全未验证**。
- **没有上游的健康巡检。** 状态只在启动、`probe_upstreams`、以及调用
  非 running 上游前刷新。一个 running 的上游中途死掉，在下次调用之前
  不会被发现（调用时会失败并立刻重探，所以影响是"第一次调用失败"）。
- **stdio 上游的输出没有独立的上限。** 读的是逐行 JSON，一行超长
  （恶意或故障上游）会吃内存，只有 `bufio` 的 1 MiB 缓冲作为第一道；
  HTTP 上游有 8 MiB 响应上限。
- **`webroot/` 没有自动化测试。** 只做了 `node --check` 语法校验与
  heredoc 写盘的实测；页面逻辑（DOM 渲染、事件）无覆盖。
- **审计 `upstream_reload` 只记集合差异**，不记 `url` / `command` 的改动。
  同一个上游换了地址再重载，审计里只会看到"上游集合未变"。
- **`codeName` 只覆盖 v02 的工具。** `tools` 包直注册的两个工具
  （`health_status`、`upstream_status`）返回体里**没有** `code` 字段，
  因此也没有 `codeName`。要么给它们补上 `code: "OK"`，要么接受这个不一致 ——
  现在是不一致状态，但至少 `codes_test.go` 会挡住"用另一个 code 生产点绕过去"。

---

## 15. 第十轮：第一次在本机跑通全部闸门

这一轮的起点是"编译打包"，但真去跑构建之后，**六个闸门里有三个是坏的**。
下面按"症状 → 根因 → 修法 → 如何证明修对了"记录。

### 15.1 三个坏掉的闸门

#### (a) `audit_shell.ps1` 第 5 项：读一个已被删除的文件

**症状**：`audit_shell.ps1` exit=1，报 `Get-Content: 找不到路径 ...\v02\reverse.go`。

**根因**：第 5 项断言 `reverse.go` 里出现 `apktoolJarPath()`，而 `reverse.go`
随逆向工具在精简重构中被删。`Get-Content` 抛错后脚本终止
（`$ErrorActionPreference='Stop'`）。

**真正的危害不是这一项失败，而是第 6/7/8 项根本没跑过。**
"检查器依赖被检查对象的某个具体文件"这类写法，被检查对象一改，检查器不是
**失败**而是**消失** —— 后三项（update-binary 发现规则、shell 结构配平、
CI 是否调用 build.sh）在上一轮之后一直处于无覆盖状态。

**修法**：改指当前真实的不变量。Go 侧已不再解析任何 jar 路径
（`grep -rn '\.jar' src --include=*.go` 为空），jar 的唯一消费者是
`bin/wrappers/{apktool,baksmali,smali}`，它们直接 exec 模块内
`/data/adb/modules/novaai.mcp/bin/tools/<name>.jar`。于是断言收敛为：

1. 没有任何代码从**状态目录**读 jar（沿用旧断言）；
2. Go 代码里不出现任何 jar 路径（新加，限定 `.go`）；
3. 三个需要 jar 的 wrapper 必须指向模块内路径（新加）；
4. `customize.sh` 不得把 jar 复制进状态目录（沿用旧断言）。

顺带给 `Find-CodeHits` 加了 `-OnlyExtension`：**"Go 里不许出现 X" 与
"任何文件里都不许出现 X" 是两个断言**，混用会把 `build.sh` / `customize.sh`
里合法的 jar 引用一起报出来（第 2 条初版就是这么错的，当场被自己抓到）。

#### (b) `probe_mcp.ps1` `[4] security.auth`：读错了 JSON 层级

**症状**：`FAIL security.auth = none -> auth=`。

**根因**：工具结果是 `{success, code, codeName, data}`，业务字段在 **`data`** 下。
断言写成 `structuredContent.security.auth`，漏了 `data` 一层 → 永远拿到 `$null`
→ **恒失败**。这条是上一轮改写探针时新写的，属自造。

**修法**：改到 `structuredContent.data.*`，并顺手把这条**扩成一组**：
除 `auth=none` / `profile=default` / `address.tcp` 外，新增三条"已删除的配置段
不得回归"（`token` / `lan` / `sessionBinding`）。负向断言的前置条件
（`data` 非空）写进条件本身 —— 否则 `data` 一旦缺失，这三条会因为"读不到东西"
而恒真，正好是"永不失败的检查"。

#### (c) `probe_mcp.ps1` `[8] 恶意 Host`：检查手段本身到不了线上

**症状**：`FAIL 恶意 Host 被拒 (-32001)`，而报错详情里是一份**正常的
tools/list 结果**。

**定位过程**（这一步值得记，因为结论反直觉）：

| 实验 | 结果 |
|------|------|
| 冷 `HttpClient` + `Headers.Host = evil.example.com` | 被拒 ✅ |
| **预热过**的 `HttpClient` 改 `Host` | **放行** ❌ |
| 预热 + `Connection: close` | 放行 ❌ |
| 预热用旧客户端、改 Host 用**新**客户端 | 被拒 ✅ |
| 原始 socket 手写 `Host: evil.example.com` | 被拒 ✅ |
| 对照：Host 保持 `127.0.0.1` | 放行（符合预期）|

**根因不在服务端**：`HttpRequestMessage.Headers.Host` 的覆盖在**连接池已经
建立 keep-alive 连接之后到不了线上**。探针第 8 项之前已经发过十来发请求，
所以那个域名从来没出现在报文里。旧版（HEAD 就有）一直踩这个坑 ——
它是一条**恒失败**的检查，而 CI 的 `regression` job 失败即 `throw`，
也就是说它一直在拦发布。

**修法**：改用**手写 HTTP 报文走原始 socket**（`SendRaw`）。我们写什么字节，
服务端就收到什么，不存在协议栈替我们做决定这回事。

同时补上**对照**：同一个通道、合法 Host 必须放行。单边检查永远分不清
"正确拒绝"与"整体坏掉"。

并把 `Send()` 的 `hostOverride` 参数**停用**（传了就抛错）：PowerShell 的多余
位置参数不会报错，若直接把参数删掉，将来传进来的 Host 会被 `$args` 静默吞掉，
然后发出一个 Host 根本没改过的请求 —— 那正是这个 bug 的复发方式。

### 15.2 `pwsh` 一直装着（上一轮的结论是错的）

上一轮断定"本机只有 Windows PowerShell 5.1，跑不了 `.ps1`"，据此改用了
"语法解析 + 临时 bash 打 curl"的替代验证。**这个前提是错的**：

```
%LOCALAPPDATA%\Microsoft\WindowsApps\Microsoft.PowerShell_8wekyb3d8bbwe\pwsh.exe
=> 7.6.6
```

它落在 `WindowsApps` 应用执行别名里，没被 Bash 工具的 PATH 解析到。
当时据"`Get-Command pwsh` 没结果"下了结论 —— 那一步没有再验证就当成结论了，
属于本仓库的老毛病（第 11c 节：**未经检验就写进"刻意决定"**）。

**仍然成立的部分**：`powershell.exe` 5.1 直接执行 BOM-less UTF-8 的 `.ps1`
会语法崩（本机实测 7/7 全报错），所以这些脚本**必须用 `pwsh` 跑**。

> 这条纠正连带作废了上一轮"探针未在本机验证"的结论。第 9 节已更新为
> 本机实测的六个数（47 / 10 / 8 / 通过 / 8-of-8 / 通过）。

### 15.3 `bin/` 下的入库资源被删了（打包会静默缺件）

**症状**：`git status` 里 `bin/` 下 12 个**已入库**的文件（3 个 `7zz`、
3 个 jar、6 个 wrapper，约 41 MB）处于 `D`（工作区已删、索引仍有）状态。

**危害**：`build.sh` / `build.ps1` 对这些目录都是**条件复制**
（`if [ -d ... ]` / `if (Test-Path ...)`），所以照现状构建**会成功**并产出一个
**没有 7z、没有 apktool、没有 wrapper** 的包，而 `verify_package.ps1`
只检查"存在的条目"的权限位，一样通过。**两道闸门都不报错。**

**修法**：`git restore bin/` 恢复（41 MB，与索引一致）。本轮产物已包含全部 12 个。

**嫌疑是 `build.ps1 clean`**：它的实现是 `Remove-Item -Recurse -Force $BinDir`，
而 `bin/` 并不是纯产物目录 —— `.gitignore` 只忽略 `/bin/*/novaaimcpd`，
其余（`7zz` / `tools/` / `wrappers/`）都是**版本化资产**。
也就是说 `clean` 会连入库内容一起删掉，且删完不影响任何闸门的结论。

> **待决**：`clean` 该只删 `bin/*/novaaimcpd` 与 `dist/`，还是干脆把
> `bin/` 全量交给 git？倾向后者+一个显式 `--purge`，但这是行为变更，
> 本轮未动。**在此之前，不要把 `clean` 当作安全的日常目标。**

### 15.4 编码环境（回答"`.ps1` 什么时候能用"）

| 解释器 | BOM-less UTF-8 的 `.ps1` | 本仓库能不能用 |
|--------|--------------------------|----------------|
| `powershell.exe` (5.1) | 按系统 ANSI（本机 CP936）解析，中文注释会吃掉引号 | **不能**，实测 7/7 语法报错 |
| `pwsh` (7.x) | 按 UTF-8 | 能，`build.ps1` + 6 个脚本全部正常 |

CI 用的 `pwsh`（`windows-latest` 自带），与本机一致，所以"本地绿 → CI 绿"
这条链路在本轮第一次真正成立。

### 15.5 本轮验证记录

- `gofmt -l` 空 / `go build ./...` / `go vet ./...` / `go test -count=1 ./...` 全绿
- **六个闸门本机实跑全绿**（数字见第 9 节）
- **五个变异测试全部如期 FAIL**，逐条证明新检查有牙齿：

| 变异 | 必须失败于 |
|------|------------|
| `novaai_status` 的 `security` 里塞回 `token` | `[4] 已删除的 security.token 未回归` |
| `hostAllowed` 一律放行 | `[8] 恶意 Host 被拒` |
| `SendRaw` 恒发域名 Host | `[8] 对照：同一通道合法 Host 放行` |
| wrapper 指向状态目录 | `audit_shell` 第 5 项 |
| （+ `codes` 那一组 6 个，见 14.6） | — |

> 第三个变异是补做的。最初写的"让 `hostAllowed` 一律拒绝"**不算证据**：
> 那样探针在 `[4]` 就会因 `$j.result` 为 `$null` 抛错终止，根本走不到 `[8]`。
> **"某个变异让它失败了"不等于"失败的是我想验的那条断言"** ——
> 这是第 5a / 11b 节同一类问题的第三种形态（前两种是：检查被无关文本满足、
> 检查依赖的文本消失）。

### 15.6 仍未做

- `build.ps1 clean` 的破坏性未修（见 15.3，属行为变更，需先决策）。
- 模块生命周期脚本（`customize.sh` / `service.sh` / `post-fs-data.sh` /
  `uninstall.sh`）**依然没有自动化台架**：`audit_shell` 只做文本/结构比对，
  第 7 项也只做关键字配平。本机跑通六个闸门**不等于模块装得上**。
- `bin/` 下 41 MB 二进制与 jar 的来源、版本、校验方式均无记录
  （本轮只是恢复，没有追溯它们是怎么进来的）。
