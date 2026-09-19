# 已知问题与未完成项

本文记录**当前状态下确实存在**的限制、待决策项与残余风险。
与 `security.md`（说明防护模型）互补：那里讲"挡什么"，这里讲"还差什么"。

最后更新：v0.05 之后的清理轮（第三轮：模块生命周期脚本与发布物料）

---

## 1. 构建：staging 清单有两份

`build.sh`（Unix）与 `build.ps1`（Windows）各自维护一份打包清单。
**改动其中一处必须同步另一处**，否则两个平台产出的包内容不一致。

- Windows 必须用 `build.ps1`：Windows 通常没有 `zip`，且 Git 自带的 bsdtar
  写出的 zip 不保留 Unix 权限位（实测 0755 被写成 `-rw-rw-rw-`）。
- `build.ps1` 自带产物校验：条目数、每个文件的 Unix 权限位、
  central directory 的宿主字段是否为 Unix(3)，任一不符即失败退出。
- `build.sh` 依赖 Unix 宿主的 `zip` 命令；其权限位由 `zip` 自身从文件系统读取。

**已修（第三轮）**：`build.ps1` 的 `Test-Executable` 要求 `bin/*/7zz` 必须是
0755，而 `build.sh` 的 chmod 清单漏了它 —— 于是 Linux 检出上跑 `build.sh`
产出的包，会被 `build.ps1` 自己的 `Test-Package` 判为失败。本机
`core.fileMode=false`，`git ls-files -s bin/` 显示 `bin/*/7zz` 在索引里是
`100644`，所以这个漏项在 Windows 上永远看不到。现已补上，并由
`scripts/audit_shell.ps1` 第 3 项机械比对两份清单（抽出 `Test-Executable`
的规则，逐个验证 `build.sh` 的 chmod 目标能覆盖它）。

> 更正：`META-INF/com/google/android/update-binary` **一直**有单独的 chmod
> （`build.sh` 第 127 行），不在漏项之列。早先把"漏 2 类 4 个文件"写进判断是
> 错的，实际只有 `bin/*/7zz` 一类 3 个文件。

**仍未做**：把清单抽成单一数据文件供两个脚本共用；`build.sh` 也**没有**任何
产物校验（只有 `build.ps1` 有）。所以"两个脚本产出同一个包"仍靠事后比对 +
人工同步，不是结构保证。

## 2. 配置字段：本轮清理 21 个无消费者字段

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
`config.example.json` 自己把它注释为"保留字段"。唯一同名的地方是
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

- `session.maxSessions`（默认 32）达到后返回 `-32014`。现在只有带
  `Mcp-Session-Id` 或含 `initialize` 的请求占用名额，无状态客户端不再消耗。
  但**真正创建 32 个会话的客户端**仍会被挡到空闲超时（默认 30 分钟）。
- `http.Server.WriteTimeout` 为 0（无上限）。单个慢客户端可以长期占住连接；
  这是为了让长时工具调用不被中断。未做按工具区分的写超时。
- 限流第二层按 **token 哈希**计数，无 token 的入口（unix socket、
  匿名 loopback）共用一个 `local` 桶 —— 同一设备上的本地调用方之间不隔离。

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

| 脚本 | 覆盖 | 当前结果 |
|------|------|----------|
| `scripts/probe_mcp.ps1` | 协议合规：initialize 协商、通知无响应体、tools/list 数量、content 包装、错误码、批量、鉴权、Host/Origin、限流、会话复用、profile 门禁、pathguard | 26/26 |
| `scripts/probe_session.ps1` | 会话四类行为：`session_list` 可编码、限流身份不可伪造、无状态请求不占名额、上限自愈 | 13/13 |
| `scripts/probe_limits.ps1` | `resultPreviewBytes` 截断语义与 `=0` 不限制 | 8/8 |
| `scripts/audit_actions.ps1` | 静态契约审计（5 项检查，见第 5 节） | 全 0 |
| `scripts/audit_shell.ps1` | 模块侧静态审计（7 项检查，见第 10 节） | 全 0 |

另有三组 Go 测试承担脚本覆盖不到的部分：

- `src/internal/tools/v02/helpers_test.go` —— `shellTimeoutSeconds` 是默认超时的
  唯一来源（Windows 无 `/system/bin/sh`，无法端到端观察超时）。
- `src/internal/tools/register_test.go` —— 工具总数断言（61）。
- `src/internal/tools/v02/actions_test.go` —— 未知/缺失 action 的负向回归；
  第三轮新增 `follow` 不得出现在 `novaai_log` schema 的断言。
- `src/cmd/novaaimcpd/main_test.go` —— PID 文件必须记录**本进程** PID（0600），
  且文件名与 `common.sh` 的 `ZCR_PID_FILE` 一致。这是 G2 的回归防线。
- `src/internal/tools/v02/reverse_test.go` —— `apktool.jar` 必须从可执行文件
  位置推导，且**不得**再指向状态目录副本。这是 G4 的回归防线。
- `src/internal/config/example_test.go` —— `docs/config.example.json` 的键集合必须
  与 `Config` 的 json tag **完全一致**，且示例里不允许出现"保留字段"式说明。
  这是对 `crashDir` / `capabilities` 那类漂移的结构化防线：按名字做全仓文本
  计数会假阴性，键集合比对不会。

> `audit_actions.ps1` 只扫描 `src/internal/tools`，因此它的"扫描工具数"
> 是 56（v02 全部），不含 `internal/tools` 下用 `reg.Register(&Tool{...})`
> 直注册的 5 个。工具总数以 `register_test.go` 的断言为准（61）。

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
