# 构建与发布流水线

工作流文件：[`.github/workflows/release.yml`](../.github/workflows/release.yml)

## 触发策略

| 事件 | 行为 |
|------|------|
| push 到 `main` | 构建 + 全部门禁，然后走**两条互斥的发布通道**：稳定版（见下）与预发布（见下） |
| push tag `v*` | 构建 + 全部门禁 + 发布该 tag。稳定 tag `v<version>` 必须与 `module.prop` 一致；预发布 tag `v<version>-dev.<序号>` 走预发布通道 |
| pull request → `main` | 只跑校验与构建，**永不发布** |
| 手动 `workflow_dispatch` | 由 `publish` 开关决定是否发布稳定版；关闭时只上传 artifact |

版本号的唯一 owner 是 `module.prop`（见 [KNOWN_ISSUES](KNOWN_ISSUES.md) 第 10b 节）。
**稳定** tag 名由它推导，工作流里没有任何版本字面量。

## 两条发布通道

`ab0c368` 之后有一次真实的不一致：修复进了 `main`，流水线全绿，但 Release 里
还是旧产物 —— 因为 `module.prop` 的 `version` 没人动过。稳定版的版本号**由人决定**
这条规则要保留（`CI.md:14`），所以新增了一条与其**互斥**的预发布通道：

| | 稳定通道（`release` job） | 预发布通道（`dev` job） |
|---|---|---|
| 触发 | tag `v*` / 手动 / push main 且该版本**尚无** Release | push main 或 push 预发布 tag |
| 版本号 | `module.prop` 的 `version` | `module.prop` 的 `version` **+ git 序号** |
| tag | `v0.05` | `v0.05-dev.9` |
| 前置条件 | 该版本还没有 Release | 该版本**已经有**稳定 Release |
| 产物 | 同名 zip | 同名 zip（内容与当次构建一致） |

两者条件互补，所以每次 push 到 `main` 最多只有一条通道动作。

```
push main ──► verify ──► build ──┬──► release : v0.05 尚未发布 → 建 tag + Release
                                 │              已发布     → 静默跳过
              └──► regression ───┴──► dev     : v0.05 尚未发布 → 静默跳过
                                                已发布     → v0.05-dev.<n>
```

### 为什么 dev 序号来自 `git rev-list --count HEAD`

commit 数是仓库的先天性质：**本地跑同一段脚本能得到与 CI 相同的数字**，可复现、
可审计，不需要查询任何外部状态。run number 做不到这一点（它只存在于 GitHub 上）。

代价是两条分支上同一个 commit 会算出同一个序号。GitHub 的 tag 不可移动，直接建
同名 tag 会让 `gh release create` 失败并把 `dev` job 判成红色。所以脚本会先查
"同版本已发布的最大序号"，若 `serial <= last` 就取 `last + 1`。失败模式因此是一条
`if`，而不是一次崩溃。

### 为什么必须"先稳定、后预发布"

GitHub 上 tag 是**全局命名空间且不可移动**。`v0.05` 与 `v0.05-dev.9` 是同一命名空间
里的两个名字，先建 `v0.05-dev.9` 不会阻止之后建 `v0.05`。

真正的理由是**精确性**：`v0.05` 必须指向"就是那个稳定构建"的 commit。若 dev 先占用
了 tag 词法空间，稳定版的 tag 就只能在别的 commit 上创建，"`v0.05` 到底对应哪次构建"
就不再唯一 —— 而 CI 已经为一个版本的 tag 与 Release 花掉了一份注意力，重复使用这个
名字只会让"哪个 commit 是 v0.05"变得含混。

因此 `dev` job 的第一件事是检查 `v<version>` 是否**已经作为 Release** 存在；不存在就
静默跳过（`publish=false`，不是失败）。这也意味着**首次 push 到 `main` 时只有稳定通道
动作**，dev 通道从第二次起才生效。

用 `gh release view` 而不是 `gh api .../git/ref/tags/...`：只建了 tag 而没发 Release 的
中间状态会误判，而"版本号已经用掉、稳定 Release 还没有"的窗口是真实存在的 ——
`release` 与 `dev` 并发运行，都只需 `[build, regression]`。

## 五个 job

| job | 平台 | 职责 | 是否阻断发布 |
|-----|------|------|--------------|
| `verify` | ubuntu | `gofmt -l` · `go vet` · `go test` · `bash -n`（8 个 shell 脚本） | 是 |
| `build` | ubuntu | 调用 `build.sh all` 打包 → `verify_package.ps1` 校验 → 上传 artifact + sha256 | 是 |
| `regression` | **windows** | `audit_actions.ps1` · `audit_shell.ps1` · `probe_mcp/session/limits.ps1` | 是 |
| `release` | ubuntu | 稳定通道：判断该版本是否已有 Release，没有则 `gh release create` | — |
| `dev` | ubuntu | 预发布通道：稳定版已发布时发 `v<version>-dev.<序号>` | — |

`release` 与 `dev` 的 `needs` 都是 `[build, regression]`，所以审计或探针失败时
**两条通道都不会发布**。只有这两个 job 有 `contents: write`，其余都是 `contents: read`。

它们共用 `concurrency: { group: publish-release }`：串行化，避免两次运行同时建同一个
tag 或同一个 Release。

## 几个刻意的决定

### CI 不自己打包

打包的 Unix owner 是 `build.sh`，Windows owner 是 `build.ps1`。工作流只**调用**
`build.sh`，不在 YAML 里重新实现编译或打包 —— 否则就是第三份清单，
正是 [KNOWN_ISSUES 第 1 节](KNOWN_ISSUES.md) 记录的那类漂移。
这条由 `scripts/audit_shell.ps1` 第 8 项机械守住。

### 用 `bash build.sh` 而不是 `./build.sh`

仓库里**所有**文件的 git mode 都是 `100644`（开发机 `core.fileMode=false`），
检出后没有可执行位。`./build.sh` 在 Linux runner 上会直接 `Permission denied`。

### 产物校验只有一份实现

原先权限位判据只存在于 `build.ps1` 的 `Test-Package` 里，而 `build.sh` 没有任何
产物校验。若 CI 再写一份校验，就会重新制造"两份清单"漂移 ——
G3（`build.sh` 漏 chmod `7zz`，于是 Linux 检出上产出的包被 Windows 侧校验器判失败）
就是那类漂移的一个实例。

现在：

```
scripts/package_contract.ps1   可执行权限矩阵 —— 唯一声明点
scripts/verify_package.ps1     产物校验 —— 唯一实现
   ├── build.ps1 的 Test-Package 调用它
   └── CI 用同一个脚本校验 build.sh 的产物
scripts/audit_shell.ps1 第 3 项 从 package_contract.ps1 提取矩阵，
                                 再验证 build.sh 的 chmod 覆盖它
```

于是"Windows 包与 Linux 包按同一套规则判定"是结构保证，而不是人工同步。

### 探针跑在 windows-latest

`scripts/*.ps1` 的五个脚本是为 Windows 写的（`$env:TEMP`、
`Start-Process -WindowStyle`），在 Linux 上的行为**未经验证**。与其在 CI 里赌
它们的可移植性，不如在它们已知全绿的平台上跑。
移植到 Linux 是一件独立工作，见 [KNOWN_ISSUES 第 11 节](KNOWN_ISSUES.md)。

> **本机怎么跑这几个脚本**：必须用 `pwsh`（PowerShell 7），**不能用
> `powershell.exe`（5.1）** —— 后者把 BOM-less UTF-8 的 `.ps1` 按系统 ANSI 解析，
> 中文注释会把紧随其后的引号吃掉，7 个脚本全部语法报错。
> 各脚本当前的通过数以 [KNOWN_ISSUES 第 9 节](KNOWN_ISSUES.md) 为准 ——
> 那里是**唯一**记着这些数字的地方，不要在这里再抄一份。

`bash -n` 之所以放在 ubuntu 的 `verify` 里，是因为那里有**真** bash；
`audit_shell.ps1` 第 7 项在 Windows 上只能做关键字配平（配平 ≠ 语法正确）。

## 首次运行需要确认的事

1. **这是 `build.sh` 第一次在 Linux 上真正执行。** 开发机是 Windows，
   此前只做过 `bash -n` 语法检查与静态比对。若 Info-ZIP 的宿主字段行为与预期
   不同，`verify_package.ps1` 会失败并打印实际值。
2. **`GO_VERSION: stable`**：`go.mod` 声明的是语言下限（`go 1.22`），不是 CI
   工具链。若想固定，把 `.github/workflows/release.yml` 里的 `GO_VERSION`
   改成具体版本号。
3. **首次 push 到 `main` 会立刻发布 `v0.05`**（当前 `module.prop` 的版本，
   仓库还没有任何 tag）。不想要就先用 `workflow_dispatch` 且关闭 `publish`。

## 本地复现同样的检查

```powershell
# 与 verify job 等价
cd src; gofmt -l .; go vet ./...; go test ./...

# 与 build job 等价
pwsh -File build.ps1 all          # Windows
bash build.sh all                 # Linux/macOS/CI

# 与 regression job 等价
pwsh -File scripts/audit_actions.ps1
pwsh -File scripts/audit_shell.ps1
pwsh -File scripts/probe_mcp.ps1
pwsh -File scripts/probe_session.ps1
pwsh -File scripts/probe_limits.ps1
```

## 发布流程

### 稳定版（手动决定版本号）

1. 改 `module.prop` 的 `version` 与 `versionCode`（唯一版本来源）。
2. push 到 `main`。
3. 流水线自动构建、校验、审计、探针，然后建 tag `v<version>` 并发布 Release，
   附 `NovaAI-MCP-v<version>.zip` 与同名 `.sha256`。

要补发某个版本，也可以 push 一个与 `module.prop` 一致的 tag。

### 预发布（每次 push 到 main 自动）

不需要任何操作。只要 `module.prop` 的版本**已经**发布过稳定版，每次 push 到 `main`
都会自动发一个 `v<version>-dev.<序号>`，附同样的 zip 与 sha256。

要让缺陷修复尽快可下载，**不需要**先提版本号 —— 这正是这条通道存在的理由。

### 用户应当下载哪个

| 想要 | 下载 |
|---|---|
| 稳定使用 | Releases 页里不带 `-dev.` 的最新版 |
| 拿到最新的缺陷修复 | 带 `-dev.` 的最新版（CI 全绿，但未经真机验证） |

注：预发布用 `gh release create --prerelease` 标记，GitHub 会把它归入
Pre-release 并在 API 里打上 `prerelease` 标志，因此用户与工具都能可靠区分，
不必只靠 tag 名后缀。

（初版曾以"会给 `audit_shell.ps1` 第 8 项造成假失败"为由不加这个参数，
实测该理由**不成立** —— 那一项只扫描 `build.sh` / `go build` / `zip -r` 字样，
`--prerelease` 三者都不含。已纠正。）

## 边界

- 流水线**不验证模块能否真正安装**。`customize.sh` / `service.sh` /
  `uninstall.sh` / `action.sh` 仍然没有自动化台架，`bash -n` 只证明语法可解析，
  不证明 Magisk 装得上。真机验证清单见
  [KNOWN_ISSUES 第 10c/10d 节](KNOWN_ISSUES.md)。
- 流水线不签名、不校验 Android 侧的运行时行为。
