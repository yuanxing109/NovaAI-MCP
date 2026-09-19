# APK 逆向分析技能

## 工具链
- **apktool** - APK 反编译/重打包
- **jadx** - Java 反编译器
- **dexdump** - DEX 结构分析
- **strings** - 字符串提取
- **readelf** - ELF 分析

## 工作流程

### 1. 获取 APK
```
novaai_reverse_apk → action: info, package: <包名>
novaai_reverse_apk → action: extract, package: <包名>, output: /sdcard/output.apk
```

### 2. 反编译
```
# 用 apktool（提取资源 + Smali）
novaai_reverse_apk → action: decompile, path: <APK路径>, tool: apktool

# 用 jadx（反编译为 Java）
novaai_reverse_apk → action: decompile, path: <APK路径>, tool: jadx
```

### 3. 分析
```
# 查看字符串
novaai_reverse_strings → action: extract, path: <文件>

# 查看 DEX 结构
novaai_reverse_dex → action: classes, path: <DEX文件>

# 查看 ELF 头
novaai_reverse_binary → action: info, path: <ELF文件>
```

### 4. 定位关键代码
```
# 搜索特定字符串
novaai_reverse_strings → action: search, path: <APK>, pattern: "api_key"

# 搜索类
novaai_reverse_dex → action: methods, path: <DEX>, class: "MainActivity"
```

## 常见场景

### 查找 API 密钥
1. strings 提取
2. grep 搜索 "key", "token", "secret"

### 分析网络请求
1. jadx 反编译
2. 搜索 "http", "url", "api"

### 绕过验证
1. apktool 提取 Smali
2. 搜索 "check", "verify", "validate"
3. 修改 Smali 后重打包
