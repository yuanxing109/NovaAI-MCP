# NovaAI-MCP

[![构建与发布](https://github.com/yuanxing109/NovaAI-MCP/actions/workflows/release.yml/badge.svg)](https://github.com/yuanxing109/NovaAI-MCP/actions/workflows/release.yml)

Android Root MCP 服务 - 让 AI 助手直接控制你的设备

## 简介

NovaAI-MCP 是一个运行在 Android 设备上的 MCP (Model Context Protocol) 服务，提供完整的 Root 权限控制能力。通过标准化的 MCP 协议，AI 助手可以直接与设备进行交互。

## 功能特性

### 📱 设备控制
- 应用管理（安装/卸载/启动/停止）
- 系统设置（显示/音频/网络/语言）
- 电源管理（重启/关机/Recovery）
- 屏幕操作（截图/录屏/点击/滑动）

### 📁 文件系统
- 文件读写（文本/二进制/Base64）
- 目录管理（创建/复制/移动/删除）
- 文件搜索（按名称/内容/大小）
- 哈希计算（MD5/SHA1/SHA256）

### 🔧 系统管理
- 进程管理（列表/信号/优先级）
- 系统属性（读取/设置）
- 服务管理（Binder/Init 服务）
- 网络操作（HTTP/Ping/DNS）

### 🔍 逆向工程
- APK 反编译（apktool/jadx）
- DEX 分析（类/方法/字符串）
- Smali 汇编/反汇编
- 二进制分析（ELF/符号表）
- Xposed 模块管理

### 🛡️ 安全特性
- Token 认证（loopback 也强制校验，可用 `security.anonymous` 显式关闭）
- Host / Origin 校验（防 DNS rebinding 与浏览器盲 CSRF）
- 权限控制（Profile 白/黑名单 + 风险等级上限，按 token 绑定 profile）
- 审计日志
- 频率限制（全局 / 客户端身份 / 单工具三层 + 并发上限）

## 安装要求

- Android 7.0+
- Root 权限（Magisk/KernelSU/APatch）
- [可选] Termux + Java（用于逆向工具）

## 安装方法

1. 从 [Releases](https://github.com/yuanxing109/NovaAI-MCP/releases) 下载 `NovaAI-MCP-v*.zip`
2. 通过 Magisk/KernelSU/APatch 安装
3. 重启设备

> 发布包由 CI 自动构建并校验：push 到 `main` 时，若 `module.prop` 的版本还没有
> 对应 Release，流水线会自动建 tag 并发布（含 `.sha256`）。见 [docs/CI.md](docs/CI.md)。

## MCP 地址

```
TCP: http://127.0.0.1:5322/mcp
Unix Socket: /data/adb/novaai-mcp/mcp.sock
```

## 工具列表

共 61 个工具，覆盖设备控制的各个方面。

| 类别 | 数量 | 说明 |
|------|------|------|
| 服务/状态 | 9 | 状态查询、能力探测、配置管理、自检，以及 auth/session/audit/health 状态 |
| 设备调度 | 2 | 设备信息、脚本任务（本服务不自动触发） |
| 文件系统 | 6 | 文件读写、搜索、哈希 |
| 归档传输 | 4 | 压缩、下载、上传、导出 |
| 命令执行 | 2 | Shell、脚本（默认 profile 拒绝，见下） |
| 应用管理 | 9 | 安装、卸载、权限 |
| Root/备份 | 4 | 模块管理、备份与恢复 |
| 系统管理 | 4 | 进程、服务、属性、设置 |
| 系统设置 | 10 | 显示、音频、网络 |
| 网络日志 | 2 | HTTP、日志 |
| 逆向工程 | 8 | APK/DEX/Smali 分析 |
| 技能 | 1 | 内置技能文档的匹配与读取 |

> 早期版本的 `novaai_task`（长任务查询）已删除：它的 5 个 action 全部空转，
> 服务端也没有任何工具会产生 taskId。
>
> `tools/list` 返回的 `inputSchema` 是**给客户端的契约**，服务端不做校验 ——
> 未知 action 由每个 handler 的兜底分支返回 `UNKNOWN_ACTION`。详见
> [docs/extensions.md](docs/extensions.md) 第 2.3 节。

> 工具调用会按 `profiles` + `sessionBinding` 做白/黑名单与风险等级校验，
> 详见 [docs/config.md](docs/config.md)。
>
> **通用文件与 shell 载体还会过一道受保护路径判定**（分区、`/data/adb/modules`、
> 模块自身配置等），详见 [docs/security.md](docs/security.md)。

## 逆向工具

内置以下逆向工具（需要 Termux + Java）：

| 工具 | 版本 | 功能 |
|------|------|------|
| apktool | 2.9.3 | APK 反编译 |
| jadx | 1.5.5 | Java 反编译 |
| smali | 3.0.10 | Smali 汇编 |
| baksmali | 3.0.10 | Smali 反汇编 |

## 使用示例

```bash
# 获取设备信息
novaai_device_info → action: get

# 截屏
novaai_screen → action: screenshot

# 反编译 APK
novaai_reverse_apk → action: decompile, package: com.app, tool: apktool

# 执行 Shell 命令（默认 profile 会拒绝；把 token 绑到 agent_full 才可用）
novaai_shell → command: "id"
```

> `novaai_shell` 不带 `action`：它是单动作工具。带 `action` 的工具只有
> 真正按 action 分派行为的那些。

## 开发

构建与发布流水线（触发策略、四个 job、门禁范围）见 [docs/CI.md](docs/CI.md)。

```bash
bash build.sh all          # Unix / macOS / CI
```

```powershell
pwsh -File build.ps1 all   # Windows
```

回归探针与静态审计脚本清单见
[docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md) 第 9 节。

## 许可证

本项目采用 MIT License，详见 [LICENSE](LICENSE)。

## 项目地址

GitHub: [yuanxing109/NovaAI-MCP](https://github.com/yuanxing109/NovaAI-MCP)
