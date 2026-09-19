# 构建与发布流水线

工作流文件：[`.github/workflows/release.yml`](../.github/workflows/release.yml)

## 触发策略

| 事件 | 行为 |
|------|------|
| push 到 `main` | 构建 + 全部门禁；若 `module.prop` 的版本**还没有**对应 Release，就自动建 tag 并发布。同一版本重复 push 不会重复发布 |
| push tag `v*` | 构建 + 全部门禁 + 发布该 tag；并要求 tag 与 `module.prop` 的版本一致，不一致直接失败 |
| pull request → `main` | 只跑校验与构建，**永不发布** |
| 手动 `workflow_dispatch` | 由 `publish` 开关决定是否发布；关闭时只上传 artifact |

版本号的唯一 owner 是 `module.prop`（见 [KNOWN_ISSUES](KNOWN_ISSUES.md) 第 10b 节）。
tag 名由它推导，工作流里没有任何版本字面量。

## 四个 job

| job | 平台 | 职责 | 是否阻断发布 |
|-----|------|------|--------------|
| `verify` | ubuntu | `gofmt -l` · `go vet` · `go test` · `bash -n`（8 个 shell 脚本） | 是 |
| `build` | ubuntu | 调用 `build.sh all` 打包 → `verify_package.ps1` 校验 → 上传 artifact + sha256 | 是 |
| `regression` | **windows** | `audit_actions.ps1` · `audit_shell.ps1` · `probe_mcp/session/limits.ps1` | 是 |
| `release` | ubuntu | 判断该版本是否已有 Release，没有则 `gh release create` | — |

`release` 的 `needs` 是 `[build, regression]`，所以审计或探针失败时**不会发布**。
只有 `release` job 有 `contents: write`，其余 job 都是 `contents: read`。

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
它们的可移植性，不如在它们已知全绿的平台上跑（26/26、13/13、8/8、审计全 0）。
移植到 Linux 是一件独立工作，见 [KNOWN_ISSUES 第 11 节](KNOWN_ISSUES.md)。

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

1. 改 `module.prop` 的 `version` 与 `versionCode`（唯一版本来源）。
2. push 到 `main`。
3. 流水线自动构建、校验、审计、探针，然后建 tag `v<version>` 并发布 Release，
   附 `NovaAI-MCP-v<version>.zip` 与同名 `.sha256`。

要发预发布或补发某个版本，也可以 push 一个与 `module.prop` 一致的 tag。

## 边界

- 流水线**不验证模块能否真正安装**。`customize.sh` / `service.sh` /
  `uninstall.sh` / `action.sh` 仍然没有自动化台架，`bash -n` 只证明语法可解析，
  不证明 Magisk 装得上。真机验证清单见
  [KNOWN_ISSUES 第 10c/10d 节](KNOWN_ISSUES.md)。
- 流水线不签名、不校验 Android 侧的运行时行为。
