# 受保护路径与 shell 拦截

本模块以 root 身份运行，多数工具最终都会落地成一次文件变更或一条 shell 命令。
本文说明"什么被挡住、什么没被挡住"，以及边界在哪。

## 一句话模型

**权限边界 = 网络可达性，其余全是防手滑。**

本服务**没有鉴权**。谁连得上 `0.0.0.0:5322`，谁就能调用全部本地工具
（以及所有已合并的上游工具），包括 `novaai_shell`。下面所有判定都在
**这个前提之下**：

- 它们降低的是"模型或用户**手滑**把设备弄坏"的概率；
- 它们**不**对抗有动机的攻击者 —— shell 可达时，任何路径判定都能被绕过
  （`P=/system; echo x > $P/build.prop`、`base64 -d | sh`、写脚本再执行…）。

要真正收窄权限，唯一的旋钮是**网络可达性**：把 `listen` 改成
`127.0.0.1:5322`（只本机 / 数据线可达），或只用 Unix socket。
详见 [config.md](config.md) 与 [KNOWN_ISSUES.md](KNOWN_ISSUES.md) 第 13 节。

---

## 中间件：只有一层

请求链只有 `hostMiddleware`（`internal/mcp/middleware.go`）。它做两件事：

| 判据 | 行为 |
|------|------|
| Host 必须是 **IP 字面量**或 `localhost` | 否则 `-32001` + `host_rejected` 审计 |
| **有 `Origin` 头直接拒绝** | 否则 `-32001` + `origin_rejected` 审计 |

没有鉴权层、没有 LAN 层、没有来源判定、没有 CORS —— 所有来源一视同仁。

### 局限（重要）

> `hostMiddleware` 只校验 Host 头是否为 IP 字面量或 `localhost`，
> **不校验来源 IP**。它防的是浏览器 DNS-rebinding，
> **不能阻挡 Python / Go / curl 直接用内网 IP 访问**。

也就是说：一个连不上你的服务、但能被你的浏览器打开的恶意网页会被挡住；
而同网段上任何一台会发 HTTP 请求的设备都畅通无阻。后一种情况在
"同网段任何设备可 root shell"这条残余风险里已被明确接受。

---

## 工具的角色划分

判定只对**通用载体**生效；**专用 owner** 就是那些位置的合法管理者，不过判定。

- *通用载体*：`novaai_fs_write`、`novaai_fs_manage`、`novaai_archive`、
  `novaai_download`、`novaai_transfer_upload`、`novaai_transfer_export`、
  `novaai_shell`、`novaai_script`、`novaai_diagnostics`、`novaai_config`(export)。
  它们能到达任意路径，所以必须过 `internal/pathguard` 判定。
- *专用 owner*：`novaai_config` 拥有 `config.json`，`novaai_root_module` 拥有
  `/data/adb/modules`，`novaai_systemless` 拥有 systemless 覆盖。
  它们就是这些位置的合法管理者，因此**不**过判定。

这样划分的理由：如果 owner 也被拦住，工具就失去意义；如果载体不被拦，
一个 `fs_write` 就能绕过所有工具级设计。

> 曾经的替代表达是"专用 owner 由 profile 拒绝来补偿"（`default` 档位 deny
> `novaai_config` / `novaai_root_module` / `novaai_systemless`）。
> 多档位已移除，`default` 放行全部工具，这条补偿**不再存在** ——
> 现在 owner 的写入能力就是它的定义，不接受它请改 `listen`。

---

## 受保护位置

判定分三层（`internal/pathguard/pathguard.go`）：

### 1. 硬拒绝前缀 —— 任何变更都不允许

| 前缀 | 原因 |
|------|------|
| `/system` `/vendor` `/product` `/system_ext` `/odm` `/oem` | 系统分区 |
| `/boot` `/recovery` `/persist` `/metadata` `/efs` `/firmware` `/modem` `/radio` | 引导与基带分区 |
| `/dev/block` | 块设备节点 |
| `/proc/sys` `/sys` | 内核参数 |
| `/data/adb/modules` `/data/adb/modules_update` | 模块目录（改错会卡开机） |
| `/data/adb/magisk` `/data/adb/ksu` `/data/adb/ksud` `/data/adb/ap` `/data/adb/lspd` | root 框架自身（改错会丢 root） |
| `/data/adb/post-fs-data.d` `/data/adb/service.d` | 开机脚本目录（写入即等于任意开机提权） |
| `/sdcard/Android/{data,obb}` 及其全部别名 | 应用私有外部存储（见第 4 层） |
| `<stateDir>` 下除工作子树外的一切 | 模块自身 `config.json` / `audit/` |

`<stateDir>` 内仍可写：`workspace/` `tmp/` `downloads/` `uploads/`
`artifacts/` `backups/` `schedules/` `skills/`。这是角色限定，不是例外机制。

前缀按**路径分量**比较：`/system_ext` 不会被 `/system` 规则误伤，
`/systemfoo` 也不受保护。

### 2. 递归删除限制

`/`、`/data`、`/data/adb`、`/sdcard`、`/storage/emulated/0`、`/mnt`、
`/data/local/tmp` 可以被**写入**，但不能被**整体递归删除或递归改权**。
所以 `rm -rf /data` 被拒，而 `rm -rf /data/local/tmp/mydir` 放行。

### 3. 通配符

`rm -rf /data/*` 与 `rm -rf /data` 等价，所以通配符按"被清空的目录"判定：

| 命令 | 结果 |
|------|------|
| `rm -rf /data/*` | 拒绝 |
| `rm -rf /sdcard/*` | 拒绝 |
| `rm -rf /system/*` | 拒绝 |
| `rm -rf /data/adb/modules/*` | 拒绝 |
| `rm -rf /data/local/tmp/*` | 放行 |
| `rm -rf /sdcard/DCIM/*` | 放行 |

### 4. `/sdcard/Android/{data,obb}` —— 硬拒绝（不容确认）

这一层的判据和前两层不同：前两层问"改了会不会**开不了机**"，这一层问
"改了会不会让**别的应用**丢数据、而且用户无从察觉"。

`Android/data` 与 `Android/obb` 是其他应用的私有外部存储。以 root 写进去
不会让设备变砖，但会静默破坏那个应用的状态，通常不可恢复。

**读：放行。变更：硬拒绝。**

| 操作 | 结果 |
|------|------|
| 读 `/sdcard/Android/data/com.x/files/a` | 放行 |
| 写 `/sdcard/Android/data/com.x/files/a` | **拒绝** |
| 删 `/sdcard/Android/data/com.x` | **拒绝** |
| 任何对 `/system` `/data/adb/modules` 的变更 | **拒绝** |

> **`confirmDangerous` 已移除。** 它曾是这一档的"默认拒绝、显式确认后放行"
> 开关。删它的理由很直接：它是**模型自己填的布尔值**，无法证明真的发生过
> 用户判断；而 shell 口子开着，任何确认档都能被绕过 —— 它不产生实质安全收益，
> 只增加状态与心智负担。直接硬拒绝更清晰。
> **确需访问 `/sdcard/Android/{data,obb}`，走 `novaai_shell`。**

覆盖全部别名：`/sdcard`、`/storage/emulated/0`、`/data/media/0`、
`/mnt/sdcard`，各自带 `Android/data` 与 `Android/obb`。这不是冗余 ——
在 `/sdcard/Android` 下，`data` 与 `obb` 是指向 `/storage/emulated/0/...`
的符号链接，只写一条会被另一条绕过，而绕过是静默的。

判定按路径分量进行，因此 `/sdcard/Android/media`（公开目录）与
`/sdcard/Android/database`（目录名相近但分量不同）不受影响。

### 5. 读取守卫为什么比写入窄得多

`novaai_fs_read` 走的是另一个判定（`pathguard.CheckRead`），只拒绝
`/dev/block` 与 `/proc/sys`。

刻意不复用写入那套前缀集合，理由有两条：

- `fixedDeny` 含 `/data/adb/modules`，但**读** `module.prop` 正是排查模块
  问题的正常手段；用写入规则去限制读取会砍掉真实能力；
- `/sdcard/Android/data` 的语义是"只能读不能动"，读是这一档承诺的能力。

`cat /dev/block/by-name/boot` 在此之前完全没有守卫，会把整个分区的原始
字节塞进工具结果 —— 这是这次收紧唯一针对的目标。

## 符号链接

判定前会解析已存在部分的符号链接。否则
`ln -s /system /data/local/tmp/x` 之后写 `/data/local/tmp/x/build.prop`
就能绕过前缀检查。不存在的尾部原样保留（新建文件时就是这种情况）。

## shell 拦截的边界（antibrick 已冻结）

`internal/antibrick` 把命令切成简单命令，识别动词与真实参数，再把路径参数
交给 `pathguard`。它覆盖：

- 引号与空白变形：`rm -fr /`、`rm -rf  /`、`rm -rf "/"`、`rm -rf '/'`
- 前缀包装：`busybox rm -rf /`、`toybox rm -rf /`
- shell 包装：`sh -c '...'`、`su -c '...'`、`su 2000 -c '...'`（递归展开 4 层）
- `cd` 跟踪：`cd / && rm -rf data`
- 重定向：`echo x > /system/build.prop`
- 动词：`rm` `rmdir` `unlink` `shred` `truncate` `dd`(`of=`) `chmod`
  `chown` `chgrp` `mv` `cp` `install` `ln` `tar`(`-x`) `unzip` `7z`
  `find -delete/-exec`
- 分区类动词直接拒绝：`mkfs*` `mke2fs*` `mkswap` `wipefs` `fdisk` `sgdisk`
  `parted` `flash` `fastboot` `make_ext4fs` `e2fsck`

### 冻结声明

`src/internal/antibrick/intercept.go` 头部有这段注释，它是这个模块的定位：

> 本解析器已冻结，不再新增规则。
> 无法拦截 `eval`、base64 管道、解释器 `system()` 等动态构造。
> 定位：防 AI / 用户手误，不是对抗恶意攻击者。

**已冻结**意味着：不新增动词、不新增变形、不为新发现的绕过开规则。
它是一个静态分析器，而静态分析对动态构造的追赶上不封顶 —— 继续加规则只会
制造"越来越安全"的错觉。

### 已知残余风险（刻意不拦）

这些**不**被拦截，是取舍，不是遗漏：

1. **变量与求值**：`P=/data; rm -rf $P`、`rm -rf $(echo L2RhdGE= | base64 -d)`
   —— 要拦住需要真正的 shell 语义求值，代价远超收益。
2. **`xargs` / `eval` / 自定义函数**：`echo / | xargs rm -rf`。
3. **编译或解释执行的间接路径**：把逻辑写进脚本文件再执行。

---

## `default` 档位放行全部工具

`default` 的 `denyTools` 为空，即**放行**全部本地工具，含
`novaai_shell` / `novaai_script`。

这是刻意的：本工具面向**单用户自有设备**，"装完即用、含 shell"是所有者提出的
诉求。前任 ADR（ADR-004）试图用"入口来源"承担权限边界（loopback 免 token、
局域网必须带 token），本轮把 token 与来源判定全部删除，因此**边界完全落在
监听地址上**：

| 入口 | 可达性 |
|---|---|
| unix socket | 文件权限 0660，仅 root |
| `listen: 127.0.0.1:5322` | 本机 + 数据线（`adb forward`） |
| `listen: 0.0.0.0:5322`（默认） | **同网段全部设备** |

代价必须记在案：只要通用 shell 可达，上面所有判定都只是建议 ——
`reboot recovery`、`rm -fr /data`、`echo x > /dev/block/by-name/boot`
都能绕过工具层设计。

### 如需收紧

只有两个动作，都在 `config.json` 里，不需要改代码：

1. **收窄可达性** —— `listen` 改成 `127.0.0.1:5322`，或关掉 TCP 只留 Unix socket。
2. **关掉限流/审计**（不是收紧，是减负）—— 见 [config.md](config.md)。

多档位已移除。若确实需要"某些工具不开放"，目前的正确做法是把 `listen`
收窄到本机，再在客户端侧限制可用工具集。

---

## `novaai_config` 的边界（有意为之）

> `novaai_config` 的 `update` action 允许运行时修改服务配置，**包括 `listen`**。
> 默认档位下可把 `0.0.0.0:5322` 改成 `127.0.0.1:5322`，也可以改回来。
>
> 这是有意为之：监听地址属于**运行时配置**，不属于 `pathguard` 的保护范围。
> `pathguard` 保护的是"改了会格机 / 丢数据"的位置，而改 `listen` 只是让服务
> 换个地址监听 —— 最坏情况是自己连不上自己，重启后配置仍在。
>
> 注意 `update` 只写文件，**需要重启 supervisor 才生效**。

---

## 归档的额外判定

`novaai_archive` / `novaai_transfer_export` / `novaai_download` 的目标路径
都会过 `guardPath`（硬拒绝前缀 + `criticalRoots`）。

**归档成员不做逐条判定。** 早期版本的 `novaai_backup restore` 会
`tar -tzf` 列出成员并逐条判定（拒绝绝对路径、`..`、受保护前缀），
该工具已随本轮精简删除，这条路也一起没了。

现在的语义是：**解压目标目录过判定，成员内容不过**。这是可以接受的，因为
解压到合法目录本身不会伤到受保护位置 —— 危害只在你手工把解出来的东西
复制回 `/system`、`/data/adb/modules` 时出现，而那时 `fs_manage` 会拒绝。

需要成员级检查时自己跑一次：

```
novaai_shell → command: "tar -tzf <归档> | grep -E '^(/|\\.\\.)|(^|/)\\.\\.' || echo SAFE"
```

## 上游 MCP 的安全边界

上游聚合（见 [upstream.md](upstream.md)）把外部 MCP 服务的工具并进本服务的
工具面。它**不引入新的鉴权问题，但引入新的信任边界**：

| 事实 | 含义 |
|---|---|
| 上游工具与本地工具走**同一条**准入路径 | 上游工具也吃全局限流、并发槽、审计；没有绕过 |
| **上游的安全性由上游自己负责** | 本服务只转发，不覆盖上游的鉴权，也无法验证上游声称的工具描述是真的 |
| 上游工具继承全局 `default` 档位 | 即放行。上游级控制只有 `denyTools` 与粒度有限的 `riskCeiling`（见 upstream.md 的字段表） |
| **`stdio` 上游是以 root 身份 spawn 的任意可执行文件** | 与 `novaai_shell` 同级的能力。能配一个 stdio 上游的人，本来就能拿到 shell |
| `launch.intent` 会调 `am start` | 等价于 `novaai_shell` 执行一次 `am start`；`launch.command` 直接 spawn（不经 shell，参数逐个传递，因此配置内容不会变成注入点） |
| 上游结果同样受 `resultPreviewBytes` 截断 | 与本地工具一致，截断时丢弃 `structuredContent` |

**结论**：上游聚合没有放宽任何东西 —— 因为它能做的事，`novaai_shell` 都能做。
但它把"执行任意代码"从"要写一条 shell 命令"降低到"填一个表单"，
这是使用体验上的变化，不是权限模型上的变化。

---

## 相关错误码

| 错误码 | 触发 |
|--------|------|
| `PROTECTED_PATH` | 目标路径落在受保护位置（含 `/sdcard/Android/{data,obb}` 硬拒绝） |
| `INVALID_PARAM` | ID 类参数（`moduleId` 等）未通过白名单 |
| `-32001` | Host / Origin 校验失败 |
| `-32003` | 档位不允许该工具（当前 `default` 放行全部，理论不可达） |
| `-32009` | 触发限流 |
| `-32010` | 并发调用数超过上限 |

## 启动时的校验

`internal/config.Validate` 只做**单字段**校验，没有组合校验 ——
配置里已经没有可以"组合出错"的开关了（无 token、无 LAN、无 CORS、
无来源判定）。校验项见 [config.md](config.md) 的「非法配置」一节。
