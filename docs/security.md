# 受保护路径与 shell 拦截

本模块以 root 身份运行，多数工具最终都会落地成一次文件变更或一条 shell 命令。
本文说明"什么被挡住、什么没被挡住"，以及边界在哪。

## 一句话模型

**通用载体受管，专用 owner 不受管。**

- *通用载体*：`novaai_fs_write`、`novaai_fs_manage`、`novaai_archive`、
  `novaai_download`、`novaai_transfer_upload`、`novaai_transfer_export`、
  `novaai_shell`、`novaai_script`、`novaai_schedule`、`novaai_backup`(restore)、
  `novaai_diagnostics`、`novaai_config`(export)。
  它们能到达任意路径，所以必须过 `internal/pathguard` 判定。
- *专用 owner*：`novaai_config` 拥有 `config.json`，`novaai_root_module` 与
  `novaai_hook_*` 拥有 `/data/adb/modules`，`novaai_systemless` 拥有
  systemless 覆盖。它们就是这些位置的合法管理者，因此**不**过判定。
  对它们的使用由 `profiles` 控制（`default` 拒绝 `novaai_config`、
  `novaai_root_module`、`novaai_systemless`、`novaai_schedule`）。

这样划分的理由：如果 owner 也被拦住，工具就失去意义；如果载体不被拦，
一个 `fs_write` 就能绕过所有工具级设计。

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
| `<stateDir>` 下除工作子树外的一切 | 模块自身 `config.json` / `token` / `audit/` |

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

### 4. 只读根 —— `/sdcard/Android/{data,obb}`

这一档的判据和前两层不同：前两层问"改了会不会**开不了机**"，这一层问
"改了会不会让**别的应用**丢数据、而且用户无从察觉"。

`Android/data` 与 `Android/obb` 是其他应用的私有外部存储。以 root 写进去
不会让设备变砖，但会静默破坏那个应用的状态，通常不可恢复。因此：

**读：放行。变更：默认拒绝，`confirmDangerous: true` 后放行。**

| 操作 | 结果 |
|------|------|
| 读 `/sdcard/Android/data/com.x/files/a` | 放行 |
| 写 `/sdcard/Android/data/com.x/files/a` | 拒绝（可确认） |
| 同上 + `confirmDangerous: true` | 放行 |
| 删 `/sdcard/Android/data/com.x` + 确认 | 放行 |
| 任何对 `/system` `/data/adb/modules` 的变更 + 确认 | **仍然拒绝** |

覆盖全部别名：`/sdcard`、`/storage/emulated/0`、`/data/media/0`、
`/mnt/sdcard`，各自带 `Android/data` 与 `Android/obb`。这不是冗余 ——
在 `/sdcard/Android` 下，`data` 与 `obb` 是指向 `/storage/emulated/0/...`
的符号链接，只写一条会被另一条绕过，而绕过是静默的。

判定按路径分量进行，因此 `/sdcard/Android/media`（公开目录）与
`/sdcard/Android/database`（目录名相近但分量不同）不受影响。

**确认能做什么、不能做什么** —— 这里必须说清楚，避免高估这一层：

`confirmDangerous` 是**模型自己填的布尔值**。本服务无法强制它先问过用户。
在真实客户端里它通常被渲染成一个需要人点确认的提示，但那是客户端的善意，
不是服务端的保证。所以这一档提供的是：

- ✅ 默认不会误删别的应用的数据（模型的常见失误）
- ❌ 不能对抗已沦为攻击者的模型

要对抗后者，正确做法是把 token 绑到 `readonly` profile，而不是依赖确认。

### 5. 读取守卫为什么比写入窄得多

`novaai_fs_read` 走的是另一个判定（`pathguard.CheckRead`），只拒绝
`/dev/block` 与 `/proc/sys`。

刻意不复用写入那套前缀集合，理由有两条：

- `fixedDeny` 含 `/data/adb/modules`，但**读** `module.prop` 正是排查模块
  问题的正常手段；用写入规则去限制读取会砍掉真实能力；
- `/sdcard/Android/data` 的语义就是"只能读不能动"，读是这一档承诺的能力。

`cat /dev/block/by-name/boot` 在此之前完全没有守卫，会把整个分区的原始
字节塞进工具结果 —— 这是这次收紧唯一针对的目标。

## 符号链接

判定前会解析已存在部分的符号链接。否则
`ln -s /system /data/local/tmp/x` 之后写 `/data/local/tmp/x/build.prop`
就能绕过前缀检查。不存在的尾部原样保留（新建文件时就是这种情况）。

## shell 拦截的边界

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

### 已知残余风险

这些**不**被拦截，是刻意的取舍，不是遗漏：

1. **变量与求值**：`P=/data; rm -rf $P`、`rm -rf $(echo L2RhdGE= | base64 -d)`
   —— 要拦住需要真正的 shell 语义求值，代价远超收益。
2. **`xargs` / `eval` / 自定义函数**：`echo / | xargs rm -rf`。
3. **编译或解释执行的间接路径**：把逻辑写进脚本文件再执行。

因此定位是**防误操作与防模型失误**，不是对抗有动机的攻击者。
如果模型已经在跑不受信任的指令，正确做法是把 token 绑到 `readonly`
或 `reverse` profile，而不是依赖拦截。

## 默认 profile 不含 shell

`default` 的 `denyTools` 包含 `novaai_shell`、`novaai_script` 与 `novaai_schedule`。

原因：只要通用 shell 可达，上面所有判定都只是建议——`reboot recovery`、
`rm -fr /data`、`echo x > /dev/block/by-name/boot` 都能绕过工具层设计。
`novaai_schedule` 属同类载体：`create` 写脚本、`run` 用 `sh` 执行，
效果与 `novaai_shell` 等价，所以必须一起挡。
61 个结构化工具已覆盖绝大多数场景。

需要时把 token 绑到 `agent_full`：

```json
"sessionBinding": {
  "byTokenHash": { "<token 的 sha256>": "agent_full" },
  "fallback": "default"
}
```

`novaai_auth_status` 会返回当前 token 的哈希，可直接填进去。

## 归档恢复的额外判定

`novaai_backup restore` 的 `destination` 默认就是 `/`，所以只检查目标是
不够的。恢复前会先 `tar -tzf` 列出成员并逐条判定：

- 成员是绝对路径 → 拒绝
- 成员含 `..` → 拒绝
- 成员解析后落在硬拒绝前缀 → 拒绝（`UNSAFE_ARCHIVE`）

`novaai_archive extract` 会判定解压目标目录，但不解析归档成员——
7z 自身会拒绝 `../` 成员，这一层没有重复实现。

## 相关错误码

| 错误码 | 触发 |
|--------|------|
| `PROTECTED_PATH` | 目标路径落在受保护位置，或落在只读根且未确认 |
| `UNSAFE_ARCHIVE` | 备份归档含绝对路径、`..` 或受保护成员 |
| `INVALID_PARAM` | ID 类参数（`moduleId` / `scheduleId` / 技能 `id`）未通过白名单 |
| `-32001` | Host/Origin 校验失败，或鉴权失败 |
| `-32003` | profile 不允许该工具 |
| `-32009` | 触发限流 |
| `-32010` | 并发调用数超过上限 |

## 启动时的组合校验

`internal/config.Validate` 拒绝的不只是单个取值，还有**合在一起才危险**
的配置。这类配置每个开关单独看都合法，不可能靠逐字段检查发现：

| 组合 | 后果 |
|------|------|
| `anonymous` + `!validateHost` | DNS rebinding 后浏览器与端口同源，失去唯一来源校验 |
| `anonymous` + `!validateOrigin` | 任意网页可跨源盲打 root 工具 |
| `allowCors` + `!validateOrigin` | CORS 反射任意 Origin，网页可带 token 全权访问 |
| `lan.enabled` + `!token.enabled` | 局域网裸奔 |
| 绑定指向不存在的 profile | 过去会静默回退到比 `default` 更宽松的档位 |

`anonymous` + `!validateOrigin` 之所以危险，是因为服务端**不检查
Content-Type**（只按字节解析 JSON）。`text/plain` 的请求因此属于"简单请求"，
不触发 preflight，会被真正发出并执行 —— 攻击者读不到响应，但 root 工具的
副作用不需要读响应。

单独关掉 `validateOrigin`（不开 anonymous、不开 CORS）**是允许的**：
那是"原生客户端 + 自建前端"的常见组合，危害面显著小于上面几种。

## 已决策的边界（追认）

以下两处是**刻意**不设守卫，不是遗漏。记录在此，避免后续被当成 bug
重新"修"一遍。

### `novaai_setting` / `novaai_property` 不做值级黑名单

这两个工具不接受路径参数，`pathguard` 不适用。真正能拦的只有
`settings put` 的**值**级黑名单（例如禁止改锁屏相关键）。

不做的理由：

- 值空间是开放的，黑名单必然不全，挡不住有动机的攻击者；
- 误伤面大——`novaai_setting` 的主要用途就是改设置；
- 这类改动都能从 recovery 或 `settings` 反向恢复，不属于"格机"，
  而本模块的防护目标是"不格机、不乱改设置到不可恢复"。

风险控制交给 profile：需要严格模式时把 token 绑到 `readonly`。

### `novaai_systemless` / `novaai_hook_*` 豁免 `pathguard`

`/data/adb/modules` 在硬拒绝前缀里，但这两个工具就是该目录的合法管理者
（`novaai_hook_xposed` 要启停 LSPosed 模块，`novaai_systemless` 要管理
systemless 覆盖）。它们也过判定的话，工具直接失效。

替代控制有两条：

1. `default` profile 拒绝 `novaai_systemless` 与 `novaai_root_module`；
2. `moduleId` / 技能 `id` / `scheduleId` 统一过 `idRe` 白名单
   （`^[A-Za-z0-9_-]+$`），消灭了原先 `../../x` 形式的路径穿越。

代价：绑定到 `agent_full` 的 token 可以改坏模块目录导致卡开机。
这是 `agent_full` 的定义，不是缺陷。
