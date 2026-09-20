# APK 逆向分析技能

> 本轮工具精简后，`novaai_reverse_*` 系列已删除。下面全部通过
> `novaai_shell` 调设备上的工具链完成。

## 工具链（需自行确认可用）

`novaai_capabilities` 会返回设备上实际存在的命令。apktool / jadx / smali
的 jar 随模块分发在 `<模块目录>/bin/tools/`，需要设备上有 Java。

| 命令 | 用途 |
|------|------|
| apktool | APK 反编译 / 重打包 |
| jadx | Java 反编译 |
| dexdump | DEX 结构分析 |
| strings | 字符串提取 |
| readelf | ELF 分析 |

## 工作流程

### 1. 取得 APK

```
novaai_shell → command: "pm path <包名>"
novaai_fs_manage → action: copy, source: "<上一步的路径>", destination: "/data/local/tmp/base.apk"
```

### 2. 反编译

```
# apktool：资源 + Smali
novaai_shell → command: "cd /data/local/tmp && java -jar <模块目录>/bin/tools/apktool.jar d -f base.apk -o out"

# jadx：反编译为 Java
novaai_shell → command: "cd /data/local/tmp && java -jar <模块目录>/bin/tools/jadx.jar -d jadx-out base.apk"
```

### 3. 分析

```
# 字符串
novaai_shell → command: "strings /data/local/tmp/base.apk | grep -iE 'api_key|secret' | head -50"

# DEX 结构
novaai_shell → command: "dexdump -f /data/local/tmp/base.apk 2>/dev/null | head -80"

# ELF 头
novaai_shell → command: "readelf -h /data/local/tmp/lib/arm64-v8a/libx.so"
```

### 4. 定位关键代码

```
# 搜索字符串（走 novaai_fs_search 的内容搜索，也能过受保护路径判定）
novaai_fs_search → action: content, root: "/data/local/tmp/jadx-out", pattern: "api_key"

# 搜索类 / 方法
novaai_shell → command: "grep -rn 'class MainActivity' /data/local/tmp/jadx-out | head"
```

## 常见场景

### 查找 API 密钥
1. `strings` 提取（上一步第 3 条）
2. 用 `grep -iE 'key|token|secret'` 窄化

### 分析网络请求
1. jadx 反编译
2. `grep -rn 'http\|url\|api' <输出目录>`

### 绕过验证
1. apktool 提取 Smali
2. `grep -rn 'check\|verify\|validate' <输出目录>`
3. 改 Smali 后重打包：`java -jar apktool.jar b out -o patched.apk`
4. 装回设备：`novaai_app_install`
