# 打包与发布流水线

工作流文件：[`.github/workflows/release.yml`](../.github/workflows/release.yml)

**本流水线不编译任何东西。** 三个 ABI 的 daemon 由**本地编译后提交进仓库**
（`bin/<abi>/novaaimcpd`），7zz / 反编译 jar / wrapper 也是随包分发的资产，
都在仓库里。CI 只做两件事：调用 `build.sh package` 把仓库内容打成模块包，
然后发布。GitHub 只负责打包与发布。

## 触发策略

| 事件 | 行为 |
|------|------|
| push 到 `main` | 打包，然后走**两条互斥的发布通道**：稳定版（见下）与预发布（见下） |
| push tag `v*` | 打包 + 发布该 tag。稳定 tag `v<version>` 必须与 `module.prop` 一致；预发布 tag `v<version>-dev.<序号>` 走预发布通道 |
| pull request → `main` | 只打包（不发布），用来确认包还能打出来 |
| 手动 `workflow_dispatch` | 由 `publish` 开关决定是否发布稳定版；关闭时只上传 artifact |

版本号的唯一 owner 是 `module.prop`（见 [KNOWN_ISSUES](KNOWN_ISSUES.md) 第 10b 节）。
**稳定** tag 名由它推导，工作流里没有任何版本字面量。

## 两条发布通道

`ab0c368` 之后有一次真实的不一致：修复进了 `main`，流水线全绿，但 Release 里
还是旧产物 —— 因为 `module.prop` 的 `version` 没人动过。稳定版的版本号**由人决定**
这条规则要保留，所以有一条与其**互斥**的预发布通道：

| | 稳定通道（`release` job） | 预发布通道（`dev` job） |
|---|---|---|
| 触发 | tag `v*` / 手动 / push main 且该版本**尚无** Release | push main 或 push 预发布 tag |
| 版本号 | `module.prop` 的 `version` | `module.prop` 的 `version` **+ git 序号** |
| tag | `v0.07` | `v0.07-dev.21` |
| 前置条件 | 该版本还没有 Release | 该版本**已经有**稳定 Release |
| 产物 | 同名 zip | 同名 zip（内容与当次构建一致） |

两者条件互补，所以每次 push 到 `main` 最多只有一条通道动作。

```
push main ──► package ──┬──► release : v0.07 尚未发布 → 建 tag + Release
                        │              已发布     → 静默跳过
                        └──► dev     : v0.07 尚未发布 → 静默跳过
                                        已发布     → v0.07-dev.<n>
```

> **升版本的那次 push 通常拿不到预发布。** `release` 与 `dev` 的 `needs` 都是
> `[package]`，两者并发启动；`dev` 第一步要 `gh release view v<version>` 确认
> 稳定版已发布，而此刻 `release` 还没建出来 —— 于是按设计静默跳过（`publish=false`，
> job 仍然是绿的）。这不是故障：那次 push 的产物就是稳定版本身。

### 为什么 dev 序号来自 `git rev-list --count HEAD`

commit 数是仓库的先天性质：**本地跑同一段脚本能得到与 CI 相同的数字**，可复现、
可审计，不需要查询任何外部状态。run number 做不到这一点（它只存在于 GitHub 上）。

代价是两条分支上同一个 commit 会算出同一个序号。GitHub 的 tag 不可移动，直接建
同名 tag 会让 `gh release create` 失败并把 `dev` job 判成红色。所以脚本会先查
"同版本已发布的最大序号"，若 `serial <= last` 就取 `last + 1`。失败模式因此是一条
`if`，而不是一次崩溃。

### 为什么必须"先稳定、后预发布"

GitHub 上 tag 是**全局命名空间且不可移动**。真正的理由是**精确性**：`v0.07` 必须
指向"就是那个稳定构建"的 commit。"`v0.07` 到底对应哪次构建"必须唯一。

因此 `dev` job 的第一件事是检查 `v<version>` 是否**已经作为 Release** 存在；不存在就
静默跳过。用 `gh release view` 而不是查 tag ref：只建了 tag 而没发 Release 的中间
状态会误判。

## 三个 job

| job | 平台 | 职责 | 是否阻断发布 |
|-----|------|------|--------------|
| `package` | ubuntu | 调用 `build.sh package` 打包 → `verify_package.ps1` 校验 → 上传 artifact + sha256 | 是 |
| `release` | ubuntu | 稳定通道：判断该版本是否已有 Release，没有则 `gh release create` | — |
| `dev` | ubuntu | 预发布通道：稳定版已发布时发 `v<version>-dev.<序号>` | — |

只有 `release` 与 `dev` 有 `contents: write`，其余是 `contents: read`；两者共用
`concurrency: { group: publish-release }`，串行化以避免同时建同一个 Release。

## 几个刻意的决定

### CI 不编译、不跑测试与审计

源码（`src/`）**不在本仓库**，只在含源码的开发工作区里。因此：

- `gofmt` / `go vet` / `go test` 无从跑起；
- 依赖源码的静态审计（`audit_actions` / `audit_shell` / `audit_skills`）与
  三个协议探针（`probe_mcp` / `probe_session` / `probe_limits`）也无从跑起。

原先 CI 里的 `verify`（ubuntu，go 工具链 + `bash -n`）与 `regression`
（windows，审计 + 探针）两个 job 已随之移除。**它们只能在开发机上跑**，
命令见下。各脚本的通过数以 [KNOWN_ISSUES 第 9 节](KNOWN_ISSUES.md) 为准。

> 直接后果：**CI 里只剩打包这一道闸门**。所以 `build.sh` 的
> `require_module_files`（以及 `build.ps1` 的 `Test-RequiredFiles`）对缺件必须
> **硬失败**，不允许条件复制 —— 残包（缺 7z / 缺 wrapper / 缺某个 ABI 的 daemon）
> 是静默的功能缺失，而 CI 已经没有第二个地方能发现它。

> 另一个后果要明说：**CI 无法发现"仓库里的二进制与源码不同步"。** 改了源码却没
> 在本地重编并提交 `bin/<abi>/novaaimcpd`，打出来的包仍然是旧的 daemon，流水线
> 依旧全绿。这是"本地编译、仓库只打包"这个形态的固有代价，靠人守流程：
> **改源码后本地跑一次 `all`，把重新编译的二进制一起 commit。**

### CI 不自己打包

打包的 Unix owner 是 `build.sh`，Windows owner 是 `build.ps1`。工作流只**调用**
`build.sh package`，不在 YAML 里重新实现打包 —— 否则就是第三份清单，
正是 [KNOWN_ISSUES 第 1 节](KNOWN_ISSUES.md) 记录的那类漂移。
这条由 `scripts/audit_shell.ps1` 第 8 项机械守住。

### 用 `bash build.sh` 而不是 `./build.sh`

仓库里**所有**文件的 git mode 都是 `100644`（开发机 `core.fileMode=false`），
检出后没有可执行位。`./build.sh` 在 Linux runner 上会直接 `Permission denied`。

### 产物校验只有一份实现

```
scripts/package_contract.ps1   可执行权限矩阵 —— 唯一声明点
scripts/verify_package.ps1     产物校验 —— 唯一实现
   ├── build.ps1 的 Test-Package 调用它
   └── CI 用同一个脚本校验 build.sh 的产物
scripts/audit_shell.ps1 第 3 项 从 package_contract.ps1 提取矩阵，
                                 再验证 build.sh 的 chmod 覆盖它
```

于是"Windows 包与 Linux 包按同一套规则判定"是结构保证，而不是人工同步。

## 本地怎么跑（这些都不在 CI 里）

```powershell
# 编译三个 ABI + 打包 + 校验（需要源码，即开发工作区）
pwsh -File build.ps1 all

# 只打包（不碰源码；CI 走的是这条路，对应 bash build.sh package）
pwsh -File build.ps1 package

# 只检查"模块必需文件是否在位"
pwsh -File build.ps1 check

# 依赖源码的闸门（都在开发机上跑）
cd src; gofmt -l .; go vet ./...; go test -count=1 ./...
pwsh -File scripts/audit_actions.ps1
pwsh -File scripts/audit_shell.ps1
pwsh -File scripts/audit_skills.ps1
pwsh -File scripts/probe_mcp.ps1
pwsh -File scripts/probe_session.ps1
pwsh -File scripts/probe_limits.ps1
```

> 跑 `.ps1` 必须用 `pwsh`（PowerShell 7），**不能用 `powershell.exe`（5.1）** ——
> 后者把 BOM-less UTF-8 的 `.ps1` 按系统 ANSI 解析，中文注释会把紧随其后的引号
> 吃掉，脚本全部语法报错。

## 发布流程

### 稳定版（手动决定版本号）

1. 改 `module.prop` 的 `version` 与 `versionCode`（唯一版本来源）。
2. 改了源码的话，本地重编三个 ABI，让 `bin/<abi>/novaaimcpd` 与新版本一致。
3. `git commit`（**编译产物要一起提交**）并 push 到 `main`。
4. 流水线打包、校验，然后建 tag `v<version>` 并发布 Release，
   附 `NovaAI-MCP-v<version>.zip` 与同名 `.sha256`。

要补发某个版本，也可以 push 一个与 `module.prop` 一致的 tag。

### 预发布（每次 push 到 main 自动）

只要 `module.prop` 的版本**已经**发布过稳定版，每次 push 到 `main` 都会自动发一个
`v<version>-dev.<序号>`，附同样的 zip 与 sha256。要让缺陷修复尽快可下载，
**不需要**先提版本号 —— 这正是这条通道存在的理由（但升版本那次例外，见上文）。

注：预发布用 `gh release create --prerelease` 标记，GitHub 会把它归入
Pre-release 并在 API 里打上 `prerelease` 标志，因此用户与工具都能可靠区分。

## 边界

- **不验证包能否真正安装。** `customize.sh` / `service.sh` / `uninstall.sh` /
  `action.sh` 仍然没有自动化台架；真机验证清单见
  [KNOWN_ISSUES 第 10c/10d 节](KNOWN_ISSUES.md)。
- **不验证二进制与源码一致**（见上）。
- 不签名、不校验 Android 侧的运行时行为。
