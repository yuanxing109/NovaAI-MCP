# APK 逆向分析技能

全部通过 `novaai_shell` 调用设备上的工具链完成。

## 前置条件

- **Java 来自 Termux**：模块自带的 `apktool` / `smali` / `baksmali` wrapper 都直接执行
  `/data/data/com.termux/files/usr/bin/java`，没有 Termux 就跑不起来。
- 随模块分发的 jar 在 `/data/adb/modules/novaai.mcp/bin/tools/` 下：
  `bin/tools/apktool.jar`、`bin/tools/smali.jar`、`bin/tools/baksmali.jar`
- **`jadx` 的 jar 不在模块里**：`jadx` wrapper 指向 Termux 的
  `$PREFIX/share/java/jadx-1.5.5-all.jar`，需要先在 Termux 里装 jadx。
- `dexdump` 用系统自带（`/apex/com.android.art/bin/dexdump`），不依赖 Termux。

先确认哪些命令真的可用：

```
novaai_capabilities → {}     # 返回设备上实际存在的命令与缺失清单
```

| 命令 | 用途 | 来源 |
|------|------|------|
| apktool | APK 反编译 / 重打包 | 模块 jar + Termux java |
| baksmali / smali | DEX ↔ Smali | 模块 jar + Termux java |
| jadx | Java 反编译 | Termux 包 |
| dexdump | DEX 结构 | 系统 |
| strings / readelf | 字符串 / ELF | toybox / binutils |

## 工作流程

### 1. 取得 APK

```
novaai_shell → command: "pm path <包名>"
novaai_fs_manage → action: copy, source: "<上一步的路径>", destination: "/data/local/tmp/base.apk"
```

### 2. 反编译

```
# apktool：资源 + Smali
novaai_shell → command: "cd /data/local/tmp && apktool d -f base.apk -o out"

# jadx：反编译为 Java（需 Termux 里装了 jadx）
novaai_shell → command: "cd /data/local/tmp && jadx -d jadx-out base.apk"
```

`apktool` / `jadx` / `smali` / `baksmali` 都是 PATH 上的 wrapper，直接叫名字即可，
不用手写 `java -jar`。也可以用自己的 jar 路径：`java -jar <路径>.jar`。

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
# 按内容搜索（action: content 用 query 传关键词，不是 pattern）
novaai_fs_search → action: content, root: "/data/local/tmp/jadx-out", query: "api_key"

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
3. 改 Smali 后重打包：`apktool b out -o patched.apk`
4. 装回设备：`novaai_app_install`
