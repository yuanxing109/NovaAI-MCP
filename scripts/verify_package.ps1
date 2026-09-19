#Requires -Version 5.1
<#
NovaAI-MCP 模块 ZIP 产物校验 —— **唯一实现**。

判据
----
  1. 每个文件条目的 Unix 权限位必须符合可执行矩阵（矩阵的唯一声明点在
     scripts/package_contract.ps1）：
       0755  bin/wrappers/*  ·  bin/*/novaaimcpd  ·  bin/*/7zz
             META-INF/com/google/android/update-binary  ·  模块根的 *.sh
       0644  其余全部
  2. central directory 每条记录的 "version made by" 宿主字节必须是 3（Unix）。
     .NET 的 ZipArchive 固定写 0（FAT），于是 zipinfo / unzip 这类按宿主字段
     判断的解压器会忽略我们写好的权限位，build.ps1 打包后会逐条改写该字节；
     Info-ZIP 的 zip 在 Unix 上本来就写 3。两者都必须过这一关。
  3. 打印条目数、可执行文件数、宿主字段分布。

为什么单独成文件（不要合回 build.ps1）
--------------------------------------
原先这套判据只存在于 build.ps1 的 Test-Package 里，而 build.sh 没有任何产物
校验。CI 在 Linux 上跑 build.sh 产出包；若 CI 用第二份实现去校验，就会重新
制造 docs/KNOWN_ISSUES.md 第 1 节记录的"两份清单"漂移。所以本文件是唯一实现：

  build.ps1 的 Test-Package 调用它；
  CI 用同一个脚本校验 build.sh 的产物。

于是"Windows 包与 Linux 包按同一套规则判定"成为结构保证，而不是人工同步。

用法
----
  pwsh -NoProfile -File scripts/verify_package.ps1 -Path dist/NovaAI-MCP-v0.05.zip

退出码：通过时 0；失败时抛异常（pwsh -File 因此以非 0 退出）。
注意这里**不调用 exit** —— build.ps1 用 `&` 在同一个会话里调用本脚本，
脚本内 `exit` 会直接终结调用方的会话。
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Path
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath $Path)) { throw "未找到产物: $Path" }
$Path = (Resolve-Path -LiteralPath $Path).Path

$contract = Join-Path $PSScriptRoot 'package_contract.ps1'
if (-not (Test-Path -LiteralPath $contract)) { throw "缺少可执行矩阵声明: $contract" }
. $contract

# PowerShell 5.1 需要显式加载该程序集；7 已内置。加载失败不致命。
try { Add-Type -AssemblyName System.IO.Compression.FileSystem -ErrorAction Stop } catch { }

Write-Host "[verify] 校验产物: $Path"

$bad = @()
$zip = [System.IO.Compression.ZipFile]::OpenRead($Path)
try {
    $execExpected = @()
    foreach ($e in $zip.Entries) {
        $rel = $e.FullName
        if ($rel.EndsWith('/')) { continue }   # 目录条目不参与权限判定
        $mode = ($e.ExternalAttributes -shr 16) -band 0xFFFF
        $wantExec = Test-Executable $rel
        if ($wantExec) { $execExpected += $rel }
        if ($wantExec -and $mode -ne 0x81ED) { $bad += "$rel 期望 0755，实际 0$('{0:X4}' -f $mode)" }
        if (-not $wantExec -and $mode -ne 0x81A4) { $bad += "$rel 期望 0644，实际 0$('{0:X4}' -f $mode)" }
    }
    $entries = $zip.Entries.Count
}
finally { $zip.Dispose() }

# 宿主字段要读原始字节：ZipArchive 不暴露 central directory 的 "version made by"。
$b = [System.IO.File]::ReadAllBytes($Path)
$eocd = -1
for ($i = $b.Length - 22; $i -ge 0; $i--) {
    if ($b[$i] -eq 0x50 -and $b[$i+1] -eq 0x4B -and $b[$i+2] -eq 0x05 -and $b[$i+3] -eq 0x06) { $eocd = $i; break }
}
if ($eocd -lt 0) { throw "zip 缺少 EOCD 记录: $Path" }

$total = $b[$eocd+10] + ($b[$eocd+11] -shl 8)
$p = [int][BitConverter]::ToUInt32($b, $eocd + 16)
$hosts = @{}
for ($k = 0; $k -lt $total; $k++) {
    if (-not ($b[$p] -eq 0x50 -and $b[$p+1] -eq 0x4B -and $b[$p+2] -eq 0x01 -and $b[$p+3] -eq 0x02)) {
        throw "central directory 第 $k 条签名不符（偏移 $p）"
    }
    $h = [string]$b[$p+5]
    $hosts[$h] = 1 + [int]$hosts[$h]

    $nameLen  = $b[$p+28] + ($b[$p+29] -shl 8)
    $extraLen = $b[$p+30] + ($b[$p+31] -shl 8)
    $cmtLen   = $b[$p+32] + ($b[$p+33] -shl 8)
    $p = $p + 46 + $nameLen + $extraLen + $cmtLen
}

$hostDesc = ($hosts.GetEnumerator() | Sort-Object Name |
             ForEach-Object { "host=$($_.Key):$($_.Value)" }) -join ' '
Write-Host "[verify]   条目 $entries 个；可执行文件 $($execExpected.Count) 个；宿主字段 $hostDesc"

if ($hosts.Keys.Count -ne 1 -or -not $hosts.ContainsKey('3')) {
    throw "宿主字段未全部标记为 Unix(3)：$hostDesc"
}
if ($bad.Count -gt 0) {
    $bad | ForEach-Object { Write-Host "  x $_" }
    throw "权限位校验失败，共 $($bad.Count) 项"
}

Write-Host "[verify]   权限位与宿主字段校验通过"
