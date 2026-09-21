# NovaAI-MCP

[![构建与发布](https://github.com/yuanxing109/NovaAI-MCP/actions/workflows/release.yml/badge.svg)](https://github.com/yuanxing109/NovaAI-MCP/actions/workflows/release.yml)

Android Root MCP 服务 - 让 AI 助手直接控制你的设备

## 简介

NovaAI-MCP 是一个运行在 Android 设备上的 MCP (Model Context Protocol) 服务，提供完整的 Root 权限控制能力。通过标准化的 MCP 协议，AI 助手可以直接与设备进行交互。

它同时是一个 **MCP 聚合网关**：对外一个地址、一份合并后的工具列表；对内是自身的
本地工具 + 你在 KernelSU WebUI 里添加的任意上游 MCP 服务。

**本服务没有鉴权。** 权限边界就是**网络可达性**：谁能连上端口，谁就能调用工具。
面向单用户自有设备、家庭可信 WiFi 的设计，装完即用。使用前请先读
[⬇️ 安全模型](#️-安全模型必读)。

## 功能特性

### 🔌 上游 MCP 聚合
- 把其他 App / 本机进程暴露的 MCP 服务接进来，**工具自动合并**
- HTTP 与 stdio 两种上游
- 工具名前缀命名空间（`{上游}__{工具}`），与本地工具互不遮蔽
- 四态状态模型（运行中 / 未启动 / 错误 / 已禁用）+ 懒启动
- **KernelSU WebUI**：上游增删改、状态探测、工具目录浏览、手动测试调用
- 详见 [docs/upstream.md](docs/upstream.md) 与 [docs/webui.md](docs/webui.md)

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
- Host / Origin 校验（防浏览器 DNS-rebinding）
- 受保护路径判定（分区、模块目录、`/sdcard/Android/{data,obb}` 硬拒绝）
- shell 命令拦截（防 `mkfs` / `dd of=/dev/block/*` / `rm -rf /system` 等手滑）
- 审计日志（JSONL 按日、参数脱敏、7 天保留）
- 频率限制（全局 / shell / 并发三层）

## 安装要求

- Android 7.0+
- Root 权限（Magisk/KernelSU/APatch）

## 安装方法

1. 从 [Releases](https://github.com/yuanxing109/NovaAI-MCP/releases) 下载 `NovaAI-MCP-v*.zip`
2. 通过 Magisk/KernelSU/APatch 安装
3. 重启设备

> 下载页上带 `-dev.` 后缀的是预发布（tag 形如 `v0.06-dev.N`），
> 不带后缀的是稳定版。二者都附 `.sha256`。发布由 CI 自动构建并校验，
> 详见 [docs/CI.md](docs/CI.md)。

## MCP 地址

```
TCP: http://0.0.0.0:5322/mcp     ← 局域网直连，无需 token
Unix Socket: /data/adb/novaai-mcp/mcp.sock
```

客户端连接 `http://<设备IP>:5322/mcp`（本机用 `127.0.0.1`），
**不需要任何认证头**。

> **本地模式**：把 `listen` 改成 `127.0.0.1:5322`，服务只有本机与数据线可达。
> 详见 [docs/config.md](docs/config.md)。

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

（工具名统一带 `novaai_` 前缀，例如 `novaai_status`。）

> 需要更底层的操作（进程属性、网络诊断、任意包管理…）直接用
> `novaai_shell` / `novaai_script`，以 root 身份执行任意命令。

> 上游工具以 `{上游名}__{工具名}` 混在**同一个** `tools/list` 里返回。
> 前缀对不上的名字仍然是 `-32015 工具不存在`。

> `tools/list` 返回的 `inputSchema` 是**给客户端的契约**，服务端不做校验 ——
> 未知 action 由每个 handler 的兜底分支返回 `UNKNOWN_ACTION`。详见
> [docs/extensions.md](docs/extensions.md) 第 2.3 节。

> **工具结果的 `code` 是英文的，且不会翻译。** 它给程序匹配用。同一个结果里
> 另有一个 `codeName` 字段给人和 AI 看：
> `{"code": "PROTECTED_PATH", "codeName": "路径受保护", "message": "路径受保护（/system）：…"}`。
> 数字错误码（`-32015` 这类）不参与这套机制。完整对照表见
> [docs/errors.md](docs/errors.md) 第 3 节。

> 工具调用会过一次档位判定（工具存在性 → 档位 → 限流 → 执行）。
> 只有 `default` 一个档位，**放行全部工具** —— 见下面的安全模型。

> **通用文件与 shell 载体还会过一道受保护路径判定**（分区、`/data/adb/modules`、
> 模块自身配置、`/sdcard/Android/{data,obb}` 硬拒绝），详见
> [docs/security.md](docs/security.md)。

## ⚠️ 安全模型（必读）

### 权限边界 = 网络可达性

**本服务不鉴权。** 只要一个设备能连到 `0.0.0.0:5322`，它就能调用全部工具，
包括 `novaai_shell` —— 那是一个以 root 身份运行的任意命令执行口子。

| 你希望的效果 | 怎么配 |
|---|---|
| 局域网直连（默认） | `listen: "0.0.0.0:5322"` |
| 只有本机 / 数据线可达 | `listen: "127.0.0.1:5322"` |
| 只有 root 可达 | 用 Unix socket，不走 TCP |

**前提是家里 WiFi 可信、设备不暴露公网。** 具体残余风险见
[docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md) 第 13 节，简版：

1. **不要做端口转发**，不要在咖啡厅 / 酒店 / 公司 WiFi 用。
2. **同网段任何设备可 root shell** —— 家里的 IoT、访客设备、路由器本身。
3. **`pathguard` 与 `antibrick` 只防手滑**，不是对抗攻击者。shell 可达时
   它们全部可被绕过。
4. **Host / Origin 校验不防内网直连**，只防浏览器 DNS-rebinding。
5. **上游 MCP 的安全性由上游自己负责。** 本服务只做转发，不覆盖上游的鉴权；
   而 `stdio` 上游是以 root 身份 spawn 的任意可执行文件 —— 与 `novaai_shell`
   同级的能力。完整清单见 [docs/upstream.md](docs/upstream.md) 的安全边界一节。

### 配置

共 9 个键（含上游列表）：`stateDir` `listen` `unixSocket` `profile`
`limits` `audit` `shellTimeoutSeconds` `resultPreviewBytes` `upstreams`。
完整字段见 [docs/config.md](docs/config.md)；配置文件里的未知键会被静默忽略。

## 上游 MCP 聚合

把另一个 MCP 服务接进来，它的工具会**自动合并**到本服务的 `tools/list`：

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

重启后（或调 `novaai_config action=reload_upstreams`）AI 客户端就会看到
`other_app_mcp__*` 系列工具。

- **推荐用 WebUI 增删**：KernelSU 管理器里点本模块的「打开」。
- 字段、状态模型、路由规则、launch 配置：**[docs/upstream.md](docs/upstream.md)**
- WebUI 页面说明与已知限制：**[docs/webui.md](docs/webui.md)**

> WebUI 只在 **KernelSU** 上可见；Magisk / APatch 用户请直接改 `config.json`。

## 使用示例

```bash
# 服务状态
novaai_status → {}

# 截屏
novaai_screen → action: screenshot

# 执行 Shell 命令（default 档位直接放行）
novaai_shell → command: "id"

# 读取应用列表
novaai_app_list → action: list
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
