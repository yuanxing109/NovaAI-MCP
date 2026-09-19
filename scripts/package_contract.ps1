#Requires -Version 5.1
<#
NovaAI-MCP 模块 ZIP 的**可执行权限矩阵** —— 唯一声明点。

消费者有三处，全部读这一份：

  build.ps1                    打包时决定每个条目写 0755 还是 0644
  scripts/verify_package.ps1   校验时决定每个条目"应该"是什么
  scripts/audit_shell.ps1      第 3 项从这里提取模式，再验证 build.sh 的
                               chmod 目标能覆盖它们

**不要把这个矩阵复制到别处。** docs/KNOWN_ISSUES.md 第 1 节记录的 G3
（build.sh 漏 chmod 7zz，于是 Linux 检出上产出的包会被 Windows 侧校验器判失败）
就是"两份清单漂移"的一个实例 —— 那份清单当时同时存在于 build.sh 的 chmod
和 build.ps1 的 Test-Executable 里。

约定
----
本文件只定义一个函数，不设置 StrictMode / ErrorActionPreference，也不执行
任何动作：它会被 build.ps1 与 verify_package.ps1 点源（dot-source）到调用方
作用域里。模式必须写成单引号字面量，因为 audit_shell.ps1 用正则
`(?:-like|-eq)\s+'([^']+)'` 提取它们。
#>

# $rel 是 zip 内的相对路径，用 '/' 分隔（不是本机路径分隔符）。
function Test-Executable([string]$rel) {
    if ($rel -like 'bin/wrappers/*') { return $true }
    if ($rel -like 'bin/*/novaaimcpd' -or $rel -like 'bin/*/7zz') { return $true }
    if ($rel -eq 'META-INF/com/google/android/update-binary') { return $true }
    if ($rel -notmatch '/' -and $rel -like '*.sh') { return $true }
    return $false
}
