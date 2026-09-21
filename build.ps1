#Requires -Version 5.1
<#
NovaAI-MCP v0.05 构建脚本（Windows / PowerShell）

与 build.sh 的关系
------------------
  build.sh   Unix（Linux / macOS / CI）构建入口，依赖 bash + zip
  build.ps1  Windows 构建入口，依赖 PowerShell + go

两者产出同一个模块包。Windows 侧必须用本脚本，原因有二：
  1. 本机没有 zip（Git for Windows 只带 unzip，不带 zip）
  2. Git 自带的 bsdtar 写出的 zip 不保留 Unix 权限位
     （实测：0755 的文件被写成 -rw-rw-rw-）

staging 清单在两处各有一份。改动其中一处必须同步另一处，
否则 Windows 包与 Unix 包内容不一致。见 docs/KNOWN_ISSUES.md。

可执行权限矩阵与产物校验**不在本文件里**：
  scripts/package_contract.ps1  权限矩阵的唯一声明点（本脚本与校验器共用）
  scripts/verify_package.ps1    产物校验的唯一实现（本脚本与 CI 共用）
不要把它们复制回来：那正是 docs/KNOWN_ISSUES.md 第 1 节记录的漂移来源。

用法
----
  pwsh -File build.ps1            # 全量：编译 + 打包（先跑必需文件检查）
  pwsh -File build.ps1 go         # 只编译三架构二进制
  pwsh -File build.ps1 package    # 只打包（需 bin/ 已就绪）；CI 走 build.sh package
  pwsh -File build.ps1 check      # 只检查模块必需文件是否在位
  pwsh -File build.ps1 clean      # 清理 dist/

从 v0.07 起 bin/ 是**版本化内容**：三个 ABI 的 novaaimcpd 由本地编译后提交进
仓库（CI 不再编译），7zz / jar / wrapper 也随仓库提供。所以：
  · clean **不再**删 bin/ —— 删它会连带删掉工作区里被 git 跟踪的文件。
  · 编译出来的二进制是"要提交的产物"，重编后请一起 commit（本地编译 → 推送产物）。
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'go', 'package', 'zip', 'check', 'clean')]
    [string]$Target = 'all'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Root      = $PSScriptRoot
$SrcDir    = Join-Path $Root 'src'
$BinDir    = Join-Path $Root 'bin'
$DistDir   = Join-Path $Root 'dist'

# 版本号唯一来源是 module.prop，与 customize.sh / action.sh / build.sh 一致。
$propPath = Join-Path $Root 'module.prop'
if (-not (Test-Path -LiteralPath $propPath)) { throw "缺少 $propPath" }
$Version = (Select-String -LiteralPath $propPath -Pattern '^version=(.*)$' |
            Select-Object -First 1).Matches[0].Groups[1].Value.Trim()
if ([string]::IsNullOrEmpty($Version)) { throw "无法从 module.prop 读取 version" }
$ModuleName = "NovaAI-MCP-v$Version"

# 可执行权限矩阵的唯一声明点。打包（Build-ModuleZip）与校验
# （Test-Package → scripts/verify_package.ps1）都从这一份读。
$ContractPath = Join-Path $Root 'scripts\package_contract.ps1'
if (-not (Test-Path -LiteralPath $ContractPath)) { throw "缺少 $ContractPath" }
. $ContractPath

$ArchTargets = @(
    @{ Dir = 'arm64-v8a';   GOOS = 'android'; GOARCH = 'arm64'; Extra = @{} },
    @{ Dir = 'armeabi-v7a'; GOOS = 'linux';   GOARCH = 'arm';   Extra = @{ GOARM = '7' } },
    @{ Dir = 'x86_64';      GOOS = 'linux';   GOARCH = 'amd64'; Extra = @{} }
)

function Write-Log([string]$Message) { Write-Host "[build] $Message" }

# 打包前把"模块包里必须有"的东西逐一点名检查（与 build.sh 的 require_module_files
# 同一份清单，改一处必须同步另一处）。
#
# 为什么是硬失败而不是条件复制：这些文件里有 7z、反编译 jar、wrapper、三个 ABI
# 的 daemon。旧实现对它们是 Test-Path 后条件复制，缺件时打包照样成功、产物校验
# 照样通过（校验器只看存在的条目），于是没有 7z / 没有 wrapper 的残包会被发出去。
function Test-RequiredFiles {
    Write-Log '检查模块必需文件'
    $missing = New-Object System.Collections.Generic.List[string]

    $rootFiles = @('module.prop', 'customize.sh', 'service.sh', 'post-fs-data.sh',
                   'uninstall.sh', 'action.sh', 'common.sh', 'sepolicy.rule',
                   'README.md', 'LICENSE',
                   'META-INF\com\google\android\update-binary',
                   'webroot\index.html')
    foreach ($f in $rootFiles) {
        if (-not (Test-Path -LiteralPath (Join-Path $Root $f))) { $missing.Add($f) }
    }
    foreach ($d in @('docs', 'skills', 'webroot')) {
        if (-not (Test-Path -LiteralPath (Join-Path $Root $d))) { $missing.Add("$d\") }
    }

    # 三个 ABI 的 daemon 与 7zz
    foreach ($t in $ArchTargets) {
        foreach ($n in @('novaaimcpd', '7zz')) {
            $p = Join-Path $BinDir "$($t.Dir)\$n"
            if (-not (Test-Path -LiteralPath $p)) { $missing.Add("bin\$($t.Dir)\$n") }
        }
    }
    # 随包分发的反编译 jar（wrapper 指向它们）
    foreach ($j in @('apktool.jar', 'smali.jar', 'baksmali.jar')) {
        if (-not (Test-Path -LiteralPath (Join-Path $BinDir "tools\$j"))) { $missing.Add("bin\tools\$j") }
    }
    # 安装到 PATH 的 wrapper
    foreach ($w in @('apktool', 'baksmali', 'dexdump', 'jadx', 'smali', 'sqlite3')) {
        if (-not (Test-Path -LiteralPath (Join-Path $BinDir "wrappers\$w"))) { $missing.Add("bin\wrappers\$w") }
    }

    if ($missing.Count -gt 0) {
        Write-Host ''
        Write-Host '模块包缺件，已中止：' -ForegroundColor Red
        foreach ($m in $missing) { Write-Host "  · $m" }
        Write-Host ''
        Write-Host '补齐方式：'
        Write-Host '  · daemon      -> 在 src\ 下编译三个 ABI（build.ps1 all 的编译段）'
        Write-Host '  · 7zz/jar/wrapper -> 这些是随包分发的资产，应随仓库一起提供'
        Write-Host '不要用"少打几个文件"绕过：残包装到设备上是静默的功能缺失。'
        throw "模块包缺 $($missing.Count) 个必需文件"
    }
}

function Resolve-GoBinary {
    $cmd = Get-Command go -CommandType Application -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    $candidates = @()
    if ($env:GOROOT) { $candidates += (Join-Path $env:GOROOT 'bin\go.exe') }
    $candidates += @(
        'D:\Dev\go\bin\go.exe'
        (Join-Path $env:LOCALAPPDATA 'Programs\Go\bin\go.exe')
        'C:\Go\bin\go.exe'
        'C:\Program Files\Go\bin\go.exe'
    )
    foreach ($p in $candidates) {
        if ($p -and (Test-Path -LiteralPath $p)) { return $p }
    }
    throw "未找到 go 命令。请安装 Go 或把 go.exe 所在目录加入 PATH。"
}

# zip 的 ExternalAttributes 高 16 位是 Unix st_mode，低 16 位是 DOS 属性。
# 值超过 Int32.MaxValue，必须按位模式转成 int32 再赋值。
function ConvertTo-ExternalAttributes([int]$UnixMode, [bool]$IsDirectory) {
    $v = [uint32]$UnixMode -shl 16
    if ($IsDirectory) { $v = $v -bor 0x10 }   # DOS 目录位
    return [System.BitConverter]::ToInt32([System.BitConverter]::GetBytes($v), 0)
}

# .NET 的 ZipArchive 固定把 central directory 的 "version made by" 高位写成 0（FAT），
# 于是 zipinfo / unzip 这类按 host 字段判断的解压器会忽略我们写好的 Unix 权限位。
# 这里在打包后直接把该字节改成 3（Unix）。
function Set-ZipHostToUnix([string]$Path) {
    $b = [System.IO.File]::ReadAllBytes($Path)

    $eocd = -1
    for ($i = $b.Length - 22; $i -ge 0; $i--) {
        if ($b[$i] -eq 0x50 -and $b[$i+1] -eq 0x4b -and $b[$i+2] -eq 0x05 -and $b[$i+3] -eq 0x06) {
            $eocd = $i; break
        }
    }
    if ($eocd -lt 0) { throw "zip 缺少 EOCD 记录: $Path" }

    $total = $b[$eocd+10] + ($b[$eocd+11] -shl 8)
    $p = [int][BitConverter]::ToUInt32($b, $eocd + 16)
    $patched = 0

    for ($k = 0; $k -lt $total; $k++) {
        if (-not ($b[$p] -eq 0x50 -and $b[$p+1] -eq 0x4b -and $b[$p+2] -eq 0x01 -and $b[$p+3] -eq 0x02)) {
            throw "central directory 第 $k 条签名不符（偏移 $p）"
        }
        $b[$p+5] = 3
        $patched++

        $nameLen  = $b[$p+28] + ($b[$p+29] -shl 8)
        $extraLen = $b[$p+30] + ($b[$p+31] -shl 8)
        $cmtLen   = $b[$p+32] + ($b[$p+33] -shl 8)
        $p = $p + 46 + $nameLen + $extraLen + $cmtLen
    }

    [System.IO.File]::WriteAllBytes($Path, $b)
    return $patched
}

function Build-Go {
    # 源码不在本仓库（只在含源码的开发工作区里）。缺了就说清楚，
    # 别让 go build 丢一个"目录不存在"的模糊错误出来。
    if (-not (Test-Path -LiteralPath $SrcDir)) {
        throw "缺少 $SrcDir —— 源码不在本仓库，本仓库只负责打包与发布。要编译 daemon，请在含源码的开发工作区里跑 all；本仓库用 package。"
    }
    $go = Resolve-GoBinary
    Write-Log "使用 go: $go"

    $env:GOPROXY = 'https://goproxy.cn,https://mirrors.aliyun.com/goproxy/,https://goproxy.io,direct'
    $env:GOSUMDB = 'sum.golang.google.cn'
    $env:GO111MODULE = 'on'
    $env:CGO_ENABLED = '0'

    $commit = (& git -C $SrcDir rev-parse --short HEAD 2>$null)
    if (-not $commit) { $commit = 'unknown' }
    $commit = $commit.Trim()
    $ldflags = "-s -w -X main.Version=$Version -X main.Commit=$commit"

    Write-Log "构建 Go 二进制 (version=$Version commit=$commit)"

    Push-Location $SrcDir
    try {
        & $go mod tidy
        if ($LASTEXITCODE -ne 0) { throw "go mod tidy 失败" }

        foreach ($t in $ArchTargets) {
            Write-Log "  -> $($t.Dir)  (GOOS=$($t.GOOS) GOARCH=$($t.GOARCH))"
            $outDir = Join-Path $BinDir $t.Dir
            New-Item -ItemType Directory -Force -Path $outDir | Out-Null

            $env:GOOS = $t.GOOS
            $env:GOARCH = $t.GOARCH
            foreach ($k in $t.Extra.Keys) { Set-Item -Path "env:$k" -Value $t.Extra[$k] }

            $out = Join-Path $outDir 'novaaimcpd'
            & $go build -trimpath -ldflags $ldflags -o $out ./cmd/novaaimcpd
            if ($LASTEXITCODE -ne 0) { throw "$($t.Dir) 构建失败" }
        }
    }
    finally {
        Pop-Location
        foreach ($k in @('GOOS', 'GOARCH', 'GOARM')) {
            Remove-Item -Path "env:$k" -ErrorAction SilentlyContinue
        }
    }
    Write-Log "Go 构建完成"
}

function Build-ModuleZip {
    Write-Log "打包模块 ZIP"

    # 缺件必须在这里就失败，不能等到"条件复制"把包打成残的。
    Test-RequiredFiles

    $stage = Join-Path ([System.IO.Path]::GetTempPath()) ("novaai-stage-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $stage | Out-Null

    try {
        # 模块根文件
        foreach ($f in @('module.prop', 'customize.sh', 'service.sh', 'post-fs-data.sh',
                         'uninstall.sh', 'action.sh', 'common.sh', 'sepolicy.rule', 'README.md',
                         'LICENSE')) {
            Copy-Item -LiteralPath (Join-Path $Root $f) -Destination $stage
        }

        # 随模块附带文档，便于在设备上离线查阅
        $docsDst = Join-Path $stage 'docs'
        New-Item -ItemType Directory -Force -Path $docsDst | Out-Null
        Copy-Item -Path (Join-Path $Root 'docs\*') -Destination $docsDst -Recurse

        # 二进制与随附工具（customize.sh 安装时依赖这些内容）。
        # 这些是**无条件**复制：Test-RequiredFiles 已经在位性检查过了。
        foreach ($t in $ArchTargets) {
            $dst = Join-Path $stage "bin\$($t.Dir)"
            New-Item -ItemType Directory -Force -Path $dst | Out-Null
            Copy-Item -LiteralPath (Join-Path $BinDir "$($t.Dir)\novaaimcpd") -Destination $dst
            Copy-Item -LiteralPath (Join-Path $BinDir "$($t.Dir)\7zz") -Destination $dst
        }
        foreach ($sub in @('tools', 'wrappers')) {
            $dst = Join-Path $stage "bin\$sub"
            New-Item -ItemType Directory -Force -Path $dst | Out-Null
            Copy-Item -Path (Join-Path $BinDir "$sub\*") -Destination $dst -Recurse
        }
        $skillsDst = Join-Path $stage 'skills'
        New-Item -ItemType Directory -Force -Path $skillsDst | Out-Null
        Copy-Item -Path (Join-Path $Root 'skills\*.md') -Destination $skillsDst

        # WebUI：KernelSU 只认模块根目录的 webroot/，且必须存在 index.html，
        # 否则模块页面入口不出现。权限与 SELinux context 由 KernelSU 自动设置，
        # 因此这里不做 chmod（也**不要**把 webroot 加进可执行矩阵）。
        $webSrc = Join-Path $Root 'webroot'
        if (-not (Test-Path -LiteralPath $webSrc)) {
            throw "缺少 $webSrc（KernelSU WebUI 入口）"
        }
        if (-not (Test-Path -LiteralPath (Join-Path $webSrc 'index.html'))) {
            throw "webroot/ 存在但没有 index.html，KernelSU 不会显示模块页面"
        }
        $webDst = Join-Path $stage 'webroot'
        New-Item -ItemType Directory -Force -Path $webDst | Out-Null
        Copy-Item -Path (Join-Path $webSrc '*') -Destination $webDst -Recurse

        # META-INF 只有仓库根一份，作为唯一事实来源直接复制
        $metaSrc = Join-Path $Root 'META-INF\com\google\android\update-binary'
        if (-not (Test-Path -LiteralPath $metaSrc)) { throw "缺少 $metaSrc" }
        Copy-Item -LiteralPath (Join-Path $Root 'META-INF') -Destination $stage -Recurse

        # 权限判定：矩阵的唯一声明点是 scripts/package_contract.ps1（本文件顶部已点源）。
        # 不要在这里重新定义 Test-Executable —— 见 docs/KNOWN_ISSUES.md 第 1 节。

        New-Item -ItemType Directory -Force -Path $DistDir | Out-Null
        $output = Join-Path $DistDir "$ModuleName.zip"
        if (Test-Path -LiteralPath $output) { Remove-Item -LiteralPath $output -Force }

        Add-Type -AssemblyName System.IO.Compression | Out-Null
        Add-Type -AssemblyName System.IO.Compression.FileSystem | Out-Null

        $stageFull = (Resolve-Path -LiteralPath $stage).Path
        $fs = [System.IO.File]::Open($output, [System.IO.FileMode]::Create)
        $zip = New-Object System.IO.Compression.ZipArchive($fs, [System.IO.Compression.ZipArchiveMode]::Create)
        $fileCount = 0

        try {
            # 目录条目（与 `zip -r` 的输出保持一致）
            $dirs = Get-ChildItem -LiteralPath $stage -Recurse -Directory |
                    ForEach-Object { $_.FullName.Substring($stageFull.Length + 1).Replace('\', '/') + '/' } |
                    Sort-Object
            foreach ($d in $dirs) {
                $e = $zip.CreateEntry($d, [System.IO.Compression.CompressionLevel]::NoCompression)
                $e.ExternalAttributes = ConvertTo-ExternalAttributes -UnixMode 0x81ED -IsDirectory $true
            }

            $files = Get-ChildItem -LiteralPath $stage -Recurse -File | Sort-Object FullName
            foreach ($f in $files) {
                $rel = $f.FullName.Substring($stageFull.Length + 1).Replace('\', '/')
                $mode = if (Test-Executable $rel) { 0x81ED } else { 0x81A4 }   # 0100755 / 0100644
                $e = $zip.CreateEntry($rel, [System.IO.Compression.CompressionLevel]::Optimal)
                $e.ExternalAttributes = ConvertTo-ExternalAttributes -UnixMode $mode -IsDirectory $false
                $in = [System.IO.File]::OpenRead($f.FullName)
                $out = $e.Open()
                try { $in.CopyTo($out) } finally { $out.Dispose(); $in.Dispose() }
                $fileCount++
            }
        }
        finally {
            $zip.Dispose()
            $fs.Dispose()
        }

        $patched = Set-ZipHostToUnix -Path $output
        $sizeMb = [math]::Round((Get-Item -LiteralPath $output).Length / 1MB, 2)
        Write-Log "打包完成: $output"
        Write-Log "  文件 $fileCount 个，central directory $patched 条已标记为 Unix 宿主，$sizeMb MB"
    }
    finally {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Test-Package {
    $output = Join-Path $DistDir "$ModuleName.zip"
    if (-not (Test-Path -LiteralPath $output)) { throw "未找到产物: $output" }

    Write-Log "校验产物"
    # 判据的唯一实现在 scripts/verify_package.ps1 —— CI 用同一个脚本校验
    # build.sh 的产物，所以两个平台的包按同一套规则判定。
    # 不要把这套判据复制回这里：那正是 docs/KNOWN_ISSUES.md 第 1 节的漂移来源。
    $verifier = Join-Path $Root 'scripts\verify_package.ps1'
    if (-not (Test-Path -LiteralPath $verifier)) { throw "缺少 $verifier" }
    & $verifier -Path $output
}

switch ($Target) {
    'all'     { Build-Go; Build-ModuleZip; Test-Package }
    'go'      { Build-Go }
    'package' { Build-ModuleZip; Test-Package }
    'zip'     { Build-ModuleZip; Test-Package }
    'check'   { Test-RequiredFiles }
    'clean' {
        # 只删 dist/（本地产物，不入库）。**不要**删 bin/ —— 从 v0.07 起那里是
        # 版本化内容（三个 ABI 的编译产物 + 随包分发的 7zz/jar/wrapper），
        # 删掉它等于删掉工作区里被 git 跟踪的文件。
        Write-Log '清理构建产物（只删 dist/）'
        if (Test-Path -LiteralPath $DistDir) { Remove-Item -LiteralPath $DistDir -Recurse -Force }
        Write-Log '完成'
    }
}
Write-Log "全部完成"
