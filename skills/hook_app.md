# 应用 Hook 技能

全部通过 `novaai_shell` 完成。

## Xposed / LSPosed 模块管理

LSPosed 自身在 `/data/adb/lspd`（属 `pathguard` 的硬拒绝前缀 ——
通用文件工具写不进去，**只能走 shell**）。

### 列出已安装模块

```bash
novaai_shell → command: "ls /data/adb/modules | grep -i lsposed; ls /data/adb/lspd"
```

### 启用 / 禁用模块

LSPosed 的模块开关位于模块自己的目录下（`/data/adb/modules/<模块>/disable`
文件存在即禁用）。注意这是一个**需要 root shell 的直接写操作**，
`pathguard` 在这个位置是硬拒绝的。

```
novaai_shell → command: "touch /data/adb/modules/<模块>/disable"   # 禁用
novaai_shell → command: "rm -f /data/adb/modules/<模块>/disable"   # 启用
```

改完通常需要重启（`novaai_power → action: reboot`）。

### 检查运行状态

```
novaai_shell → command: "ps -A | grep -i lspd"
novaai_log → action: logcat, lines: 200      # 之后自行筛 LSPosed 相关行
```

## Frida（需自行安装 frida-server）

```
# 列出进程
novaai_shell → command: "frida-ps -U"

# 附加到进程
novaai_shell → command: "frida -U -n <包名> -e 'console.log(Process.arch)'"

# 执行 hook 脚本（脚本先写盘，再喂给 frida）
novaai_fs_write → action: create, path: "hook.js", content: "<脚本内容>"
novaai_shell   → command: "frida -U -n <包名> -l /data/adb/novaai-mcp/workspace/hook.js"
```

`frida-server` 需要以 root 常驻：

```
novaai_shell → command: "nohup /data/local/tmp/frida-server -D &"
```

## 常见场景

### 绕过 Root 检测
1. 反编译找到检测类（见 `reverse_apk`）
2. 写 hook 脚本，让检测方法返回 `false`
3. 用上面的 `frida -l` 加载

### 修改返回值
1. 定位目标方法
2. Hook implementation，返回自定义值
3. `Java.perform()` 里替换 `implementation`

## 安全提醒

`/data/adb/modules` 与 `/data/adb/lspd` 是 `pathguard` 的硬拒绝位置 ——
改错会导致**卡开机或丢失 root**。这里的操作之所以可行，只是因为 shell
是万能绕过（见 [docs/security.md](../docs/security.md)），
不代表这些路径被放开了。
