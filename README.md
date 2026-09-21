# NovaAI-MCP

[![构建与发布](https://github.com/yuanxing109/NovaAI-MCP/actions/workflows/release.yml/badge.svg)](https://github.com/yuanxing109/NovaAI-MCP/actions/workflows/release.yml)

Android Root MCP 服务 —— 让 AI 助手直接控制你的设备。

## 简介

NovaAI-MCP 运行在 Android 设备上，遵循 MCP (Model Context Protocol) 标准，
以 root 权限提供设备控制能力。它同时是一个 **MCP 聚合网关**：对外一个地址、
一份合并后的工具列表；对内是自身的本地工具 + 你在 KernelSU WebUI 里添加的
上游 MCP 服务。

**本服务没有鉴权。** 权限边界就是**网络可达性**：谁能连上端口，谁就能调用工具。
面向单用户自有设备、家庭可信 WiFi 的设计，装完即用。使用前请先读
[⬇️ 安全模型](#️-安全模型必读)。

## 功能特性

### 🔌 上游 MCP 聚合
- 把其他 App / 本机进程的 MCP 服务接进来，**工具自动合并**
- 支持 HTTP 与 stdio 两种上游
- 工具名带 `{上游名}__` 前缀，与本地工具互不遮蔽
- 状态可见（运行中 / 未启动 / 错误 / 已禁用），可配懒启动
- **KernelSU WebUI**：增删改上游、探测状态、浏览工具目录、手动测试调用

### 📱 设备控制
- 应用管理（列表/信息/安装/启停/卸载）
- 屏幕操作（截图/录屏/点击/滑动/文本输入）
- 电源管理（重启/Recovery/Bootloader/关机）

### 📁 文件系统
- 文件读写（文本/二进制/Base64/补丁）
- 目录管理（创建/复制/移动/删除/权限/链接）
- 文件搜索（按名称/内容/大小/重复）
- 哈希计算（MD5/SHA1/SHA256）
- 归档与传输（ZIP/TAR/GZIP/XZ/7z、下载、分块上传、导出）

### 🔧 系统管理
- 进程管理（列表/信号/优先级/fd）
- 日志读取（Logcat/内核/dmesg/模块与 MCP 日志）
- Root 管理（框架探测、模块生命周期、Systemless 覆盖）
- 命令执行（Shell / 多行脚本）

### 🛡️ 防护与可观测
- Host / Origin 校验
- 受保护路径判定（分区、模块目录、`/sdcard/Android/{data,obb}` 硬拒绝）
- 危险 shell 命令拦截（`mkfs`、`dd of=/dev/block/*`、`rm -rf /system` 等）
- 审计日志（JSONL 按日、参数脱敏、7 天保留）
- 频率限制（全局 / shell / 并发三层）

## 安装

要求：Android 7.0+，已 Root（Magisk / KernelSU / APatch）。

1. 从 [Releases](https://github.com/yuanxing109/NovaAI-MCP/releases) 下载 `NovaAI-MCP-v*.zip`
2. 用 Root 管理器安装
3. 重启设备

> Releases 里带 `-dev.` 后缀的是预发布，不带的是稳定版，二者都附 `.sha256`。

## MCP 地址

```
TCP: http://0.0.0.0:5322/mcp     ← 局域网直连
Unix Socket: /data/adb/novaai-mcp/mcp.sock
```

客户端连接 `http://<设备IP>:5322/mcp`（本机用 `127.0.0.1`），不需要任何认证头。

## 工具列表

共 **30 个本地工具**（上游工具数量随配置变化，不计入）。

| 类别 | 数量 | 工具 |
|------|------|------|
| 服务/状态 | 6 | `status` `capabilities` `health_status` `upstream_status` `config` `diagnostics` |
| 文件系统 | 6 | `fs_info` `fs_read` `fs_write` `fs_manage` `fs_search` `fs_hash` |
| 归档传输 | 4 | `archive` `download` `transfer_upload` `transfer_export` |
| 命令执行 | 2 | `shell` `script` |
| 应用管理 | 4 | `app_list` `app_info` `app_install` `app_manage` |
| 系统 | 5 | `process` `log` `screen` `input` `power` |
| Root | 3 | `root_info` `root_module` `systemless` |

工具名统一带 `novaai_` 前缀（例如 `novaai_status`）。需要更底层的操作直接用
`novaai_shell` / `novaai_script`，以 root 身份执行任意命令。

设备上另有 5 份多步操作配方（APK 逆向、应用 Hook、网络调试、系统排障、备份恢复），
随模块安装在 `{stateDir}/skills/`，可用 `novaai_fs_read` 阅读。

## ⚠️ 安全模型（必读）

**本服务不鉴权。** 只要一个设备能连到 `0.0.0.0:5322`，它就能调用全部工具，
包括 `novaai_shell` —— 那是一个以 root 身份运行的任意命令执行口子。

| 你希望的效果 | 怎么配 |
|---|---|
| 局域网直连（默认） | `listen: "0.0.0.0:5322"` |
| 只有本机 / 数据线可达 | `listen: "127.0.0.1:5322"` |
| 只有 root 可达 | 用 Unix socket，不走 TCP |

**前提是家里 WiFi 可信、设备不暴露公网。** 要点：

1. **不要做端口转发**，不要在咖啡厅 / 酒店 / 公司 WiFi 用。
2. **同网段任何设备可 root shell** —— 家里的 IoT、访客设备、路由器本身。
3. **`pathguard` 与 `antibrick` 只防手滑**，不是对抗攻击者；shell 可达时它们
   全部可被绕过。
4. **Host / Origin 校验不防内网直连**，只防浏览器 DNS-rebinding。
5. **上游 MCP 的安全性由上游自己负责。** 本服务只做转发，不覆盖上游的鉴权；
   而 `stdio` 上游是以 root 身份 spawn 的任意可执行文件。

完整残余风险见 [docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md)。

## 配置

配置文件为 `{stateDir}/config.json`，共 9 个键：`stateDir`、`listen`、
`unixSocket`、`profile`、`limits`、`audit`、`shellTimeoutSeconds`、
`resultPreviewBytes`、`upstreams`。完整字段见 [docs/config.md](docs/config.md)。

**本地模式**：把 `listen` 改成 `127.0.0.1:5322`，服务只有本机与数据线可达。

## 上游 MCP 聚合

把另一个 MCP 服务接进来，它的工具会自动合并到本服务的工具列表：

```json
{
  "upstreams": [
    {
      "name": "other_app_mcp",
      "type": "http",
      "url": "http://127.0.0.1:9999/mcp",
      "enabled": true
    }
  ]
}
```

AI 客户端随后会看到 `other_app_mcp__*` 系列工具。

- 推荐用 WebUI 增删：在 KernelSU 管理器里打开本模块的 WebUI。
- 字段与状态模型：**[docs/upstream.md](docs/upstream.md)**
- WebUI 页面说明：**[docs/webui.md](docs/webui.md)**

> WebUI 只在 **KernelSU** 上可见；Magisk / APatch 用户请直接改 `config.json`。

## 使用示例

```bash
# 服务状态
novaai_status → {}

# 截屏
novaai_screen → action: screenshot

# 执行 shell 命令
novaai_shell → command: "id"

# 读取应用列表
novaai_app_list → action: list
```

## 开发

```bash
bash build.sh all          # Unix / macOS / CI
```

```powershell
pwsh -File build.ps1 all   # Windows
```

构建产物在 `dist/`。CI 与发布流水线见 [docs/CI.md](docs/CI.md)。

## 许可证

本项目采用 MIT License，详见 [LICENSE](LICENSE)。

## 项目地址

GitHub: [yuanxing109/NovaAI-MCP](https://github.com/yuanxing109/NovaAI-MCP)
