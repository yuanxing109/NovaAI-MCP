# 备份恢复技能

> 本轮工具精简后，`novaai_backup` 已删除。归档能力由 `novaai_archive`
> 承担，其余通过 `novaai_shell` 调 `tar`。
>
> **`confirmDangerous` 也已移除**：`/sdcard/Android/{data,obb}` 现在是被
> 硬拒绝的位置，不提供"确认后放行"。确需访问走 `novaai_shell`。

## 本服务自身状态的备份

模块的配置与审计在 `{stateDir}`（默认 `/data/adb/novaai-mcp`）下。

### 创建备份

```
novaai_archive → action: create, format: "tar.gz",
                 source: ["/data/adb/novaai-mcp/workspace"],
                 destination: "/data/adb/novaai-mcp/workspace/nova-mcp-backup.tar.gz"
```

> 目标写成 `{stateDir}/workspace/...` 是有意的：`{stateDir}` 整体受
> `pathguard` 保护，只有 `workspace` / `tmp` / `downloads` / `uploads` /
> `artifacts` / `backups` / `schedules` / `skills` 这几个子树可写。
> 想放到外置存储就写 `/sdcard/...`。

### 列出归档内容

```
novaai_archive → action: list, source: ["/data/adb/novaai-mcp/workspace/nova-mcp-backup.tar.gz"]
```

### 校验归档

```
novaai_archive → action: verify, source: [".../nova-mcp-backup.tar.gz"]
novaai_fs_hash  → action: sha256, path: ".../nova-mcp-backup.tar.gz"
```

## 恢复

### 解到临时目录（推荐）

```
novaai_archive → action: extract,
                 source: [".../nova-mcp-backup.tar.gz"],
                 destination: "/data/local/tmp/restore"
```

解压目标同样过受保护路径判定，所以解压到 `/` 会被拒绝
（`PROTECTED_PATH`）。**先解到临时目录，再逐项复制**，是这个服务里唯一
安全的恢复姿势。

### 成员级安全检查

`novaai_archive extract` 不解析归档成员路径（7z/tar 自身会拒绝 `../`）。
要逐条判定成员是否落在硬拒绝前缀，用 shell 先列一份清单：

```
novaai_shell → command: "tar -tzf /data/adb/novaai-mcp/workspace/nova-mcp-backup.tar.gz | grep -E '^(/|\\.\\.)|(^|/)\\.\\.' || echo SAFE"
```

上面这条只要**输出任何路径**就说明归档里有绝对路径或 `..` 成员，
此时不要继续恢复。

> 归档里混入 `/system`、`/data/adb/modules` 这类成员时，解压到临时目录
> 也不会造成伤害；危害只在你手工把解出来的东西复制回那些位置时出现。

## 应用数据

### 备份应用数据

```
novaai_archive → action: create, format: "tar.gz",
                 source: ["/data/data/com.app"],
                 destination: "/data/local/tmp/app_data.tar.gz"
```

### 恢复应用数据

```
novaai_archive → action: extract,
                 source: ["/data/local/tmp/app_data.tar.gz"],
                 destination: "/data/local/tmp/restore"
novaai_fs_manage → action: copy, source: "/data/local/tmp/restore/data/data/com.app",
                   destination: "/data/data/com.app", recursive: true
```

## 注意事项

- **恢复前先验证归档完整性**（`action: verify` + `novaai_fs_hash`）。
- **`/sdcard/Android/{data,obb}` 是硬拒绝**，通用文件与归档工具都写不进去，
  也没有确认放行。
- 系统应用数据恢复后通常需要重启：`novaai_power → action: reboot`。
- 早期版本里 `novaai_backup restore` 有一条"逐条判定归档成员"的实现，
  随该工具一起删除；现在这一步要靠上面那条 `tar -tzf` 检查，
  **是手工的，不是自动的**。
