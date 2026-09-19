# 备份恢复技能

## 备份操作

### 创建备份
```
novaai_backup → action: create, sources: ["/data/adb/novaai-mcp"], destination: "/sdcard/backup.tar.gz"
```

### 列出备份
```
novaai_backup → action: list
```

### 验证备份
```
novaai_backup → action: verify, path: "/sdcard/backup.tar.gz", sha256: "<哈希值>"
```

## 恢复操作

### 恢复备份
```
novaai_backup → action: restore, path: "/sdcard/backup.tar.gz", destination: "/", confirmDangerous: true
```

恢复前会先列出归档成员并逐条判定：只要有一条会落到分区
（`/system`、`/boot`…）或模块目录（`/data/adb/modules`…），整次恢复被拒绝，
返回 `UNSAFE_ARCHIVE`。归档里的绝对路径与 `..` 成员同样直接拒绝。

## 应用数据备份

### 备份应用数据
```
novaai_archive → action: create, format: "tar.gz",
                 source: ["/data/data/com.app"], destination: "/sdcard/app_data.tar.gz"
```

### 恢复应用数据
```
novaai_archive → action: extract, source: ["/sdcard/app_data.tar.gz"],
                 destination: "/data/local/tmp/restore"
```

解压目标同样过受保护路径判定，所以解压到 `/` 会被拒绝
（`PROTECTED_PATH`）；先解到临时目录再逐项复制。

## 注意事项
- 恢复操作需要 `confirmDangerous: true`
- 恢复前建议先验证备份完整性
- 系统应用数据恢复可能需要重启
- 默认 profile 不含 `novaai_shell`，上面全部用结构化工具完成
