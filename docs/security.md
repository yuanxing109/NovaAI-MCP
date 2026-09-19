# 受保护路径与 shell 拦截

本模块以 root 身份运行，多数工具最终都会落地成一次文件变更或一条 shell 命令。
本文说明"什么被挡住、什么没被挡住"，以及边界在哪。

## 一句话模型

**通用载体受管，专用 owner 不受管。**

- *通用载体*：`novaai_fs_write`、`novaai_fs_manage`、`novaai_archive`、
  `novaai_download`、`novaai_transfer_upload`、`novaai_transfer_export`、
  `novaai_shell`、`novaai_script`、`novaai_backup`(restore)、
  `novaai_skill`(forget)、`novaai_diagnostics`、`novaai_config`(export)。
  它们能到达任意路径，所以必须过 `internal/pathguard` 判定。
- *专用 owner*：`novaai_config` 拥有 `config.json`，`novaai_root_module` 与
  `novaai_hook_*` 拥有 `/data/adb/modules`，`novaai_systemless` 拥有
  systemless 覆盖。它们就是这些位置的合法管理者，因此**不**过判定。
  对它们的使用由 `profiles` 控制（`default` 拒绝 `novaai_config`、
  `novaai_root_module`、`novaai_systemless`）。

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

`default` 的 `denyTools` 包含 `novaai_shell` 与 `novaai_script`。

原因：只要通用 shell 可达，上面所有判定都只是建议——`reboot recovery`、
`rm -fr /data`、`echo x > /dev/block/by-name/boot` 都能绕过工具层设计。
62 个结构化工具已覆盖绝大多数场景。

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
| `PROTECTED_PATH` | 通用载体的目标路径落在受保护位置 |
| `UNSAFE_ARCHIVE` | 备份归档含绝对路径、`..` 或受保护成员 |
| `INVALID_PARAM` | ID 类参数（`moduleId` / `scheduleId` / 技能 `id`）未通过白名单 |
| `-32003` | profile 不允许该工具 |
| `-32009` | 触发限流 |
