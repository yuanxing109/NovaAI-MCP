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

用法
----
  pwsh -File build.ps1            # 全量：编译 + 打包
  pwsh -File build.ps1 go         # 只编译三架构二进制
  pwsh -File build.ps1 zip        # 只打包（需 bin/ 已就绪）
  pwsh -File build.ps1 clean      # 清理 bin/ 与 dist/
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'go', 'zip', 'clean')]
    [string]$Target = 'all'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Root      = $PSScriptRoot
$SrcDir    = Join-Path $Root 'src'
$BinDir    = Join-Path $Root 'bin'
$DistDir   = Join-Path $Root 'dist'
$ModuleName = 'NovaAI-MCP-v0.05'
$Version    = '0.05'

$ArchTargets = @(
    @{ Dir = 'arm64-v8a';   GOOS = 'android'; GOARCH = 'arm64'; Extra = @{} },
    @{ Dir = 'armeabi-v7a'; GOOS = 'linux';   GOARCH = 'arm';   Extra = @{ GOARM = '7' } },
    @{ Dir = 'x86_64';      GOOS = 'linux';   GOARCH = 'amd64'; Extra = @{} }
)

function Write-Log([string]$Message) { Write-Host "[build] $Message" }

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

    foreach ($t in $ArchTargets) {
        $bin = Join-Path $BinDir "$($t.Dir)\novaaimcpd"
        if (-not (Test-Path -LiteralPath $bin)) { throw "缺少 $($t.Dir) 二进制：$bin" }
    }

    $stage = Join-Path ([System.IO.Path]::GetTempPath()) ("novaai-stage-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $stage | Out-Null

    try {
        # 模块根文件
        foreach ($f in @('module.prop', 'customize.sh', 'service.sh', 'post-fs-data.sh',
                         'uninstall.sh', 'action.sh', 'common.sh', 'sepolicy.rule', 'README.md')) {
            Copy-Item -LiteralPath (Join-Path $Root $f) -Destination $stage
        }

        # 随模块附带文档，便于在设备上离线查阅
        $docsSrc = Join-Path $Root 'docs'
        if (Test-Path -LiteralPath $docsSrc) {
            $docsDst = Join-Path $stage 'docs'
            New-Item -ItemType Directory -Force -Path $docsDst | Out-Null
            Copy-Item -Path (Join-Path $docsSrc '*') -Destination $docsDst -Recurse
        }

        # 二进制与随附工具（customize.sh 安装时依赖这些内容）
        foreach ($t in $ArchTargets) {
            $dst = Join-Path $stage "bin\$($t.Dir)"
            New-Item -ItemType Directory -Force -Path $dst | Out-Null
            Copy-Item -LiteralPath (Join-Path $BinDir "$($t.Dir)\novaaimcpd") -Destination $dst
            $sevenZip = Join-Path $BinDir "$($t.Dir)\7zz"
            if (Test-Path -LiteralPath $sevenZip) { Copy-Item -LiteralPath $sevenZip -Destination $dst }
        }
        foreach ($sub in @('tools', 'wrappers')) {
            $src = Join-Path $BinDir $sub
            if (Test-Path -LiteralPath $src) {
                $dst = Join-Path $stage "bin\$sub"
                New-Item -ItemType Directory -Force -Path $dst | Out-Null
                Copy-Item -Path (Join-Path $src '*') -Destination $dst -Recurse
            }
        }
        $skillsSrc = Join-Path $Root 'skills'
        if (Test-Path -LiteralPath $skillsSrc) {
            $skillsDst = Join-Path $stage 'skills'
            New-Item -ItemType Directory -Force -Path $skillsDst | Out-Null
            Copy-Item -Path (Join-Path $skillsSrc '*.md') -Destination $skillsDst
        }

        # META-INF 只有仓库根一份，作为唯一事实来源直接复制
        $metaSrc = Join-Path $Root 'META-INF\com\google\android\update-binary'
        if (-not (Test-Path -LiteralPath $metaSrc)) { throw "缺少 $metaSrc" }
        Copy-Item -LiteralPath (Join-Path $Root 'META-INF') -Destination $stage -Recurse

        # 权限判定：脚本、二进制、wrapper 可执行；其余 0644
        function Test-Executable([string]$rel) {
            if ($rel -like 'bin/wrappers/*') { return $true }
            if ($rel -like 'bin/*/novaaimcpd' -or $rel -like 'bin/*/7zz') { return $true }
            if ($rel -eq 'META-INF/com/google/android/update-binary') { return $true }
            if ($rel -notmatch '/' -and $rel -like '*.sh') { return $true }
            return $false
        }

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
    Add-Type -AssemblyName System.IO.Compression.FileSystem | Out-Null
    $zip = [System.IO.Compression.ZipFile]::OpenRead($output)
    $bad = @()
    try {
        $execExpected = @()
        foreach ($e in $zip.Entries) {
            $rel = $e.FullName
            $isDir = $rel.EndsWith('/')
            $mode = ($e.ExternalAttributes -shr 16) -band 0xFFFF
            if ($isDir) { continue }
            $wantExec = ($rel -like 'bin/wrappers/*') -or ($rel -like 'bin/*/novaaimcpd') -or
                        ($rel -like 'bin/*/7zz') -or ($rel -eq 'META-INF/com/google/android/update-binary') -or
                        ($rel -notmatch '/' -and $rel -like '*.sh')
            if ($wantExec) { $execExpected += $rel }
            if ($wantExec -and $mode -ne 0x81ED) { $bad += "$rel 期望 0755，实际 0$('{0:X4}' -f $mode)" }
            if (-not $wantExec -and $mode -ne 0x81A4) { $bad += "$rel 期望 0644，实际 0$('{0:X4}' -f $mode)" }
        }
        $entries = $zip.Entries.Count
    }
    finally { $zip.Dispose() }

    $b = [System.IO.File]::ReadAllBytes($output)
    $eocd = -1
    for ($i = $b.Length - 22; $i -ge 0; $i--) {
        if ($b[$i] -eq 0x50 -and $b[$i+1] -eq 0x4b -and $b[$i+2] -eq 0x05 -and $b[$i+3] -eq 0x06) { $eocd = $i; break }
    }
    $total = $b[$eocd+10] + ($b[$eocd+11] -shl 8)
    $p = [int][BitConverter]::ToUInt32($b, $eocd + 16)
    $hosts = @{}
    for ($k = 0; $k -lt $total; $k++) {
        $h = [string]$b[$p+5]
        $hosts[$h] = 1 + [int]$hosts[$h]
        $nameLen  = $b[$p+28] + ($b[$p+29] -shl 8)
        $extraLen = $b[$p+30] + ($b[$p+31] -shl 8)
        $cmtLen   = $b[$p+32] + ($b[$p+33] -shl 8)
        $p = $p + 46 + $nameLen + $extraLen + $cmtLen
    }

    $hostDesc = ($hosts.GetEnumerator() | ForEach-Object { "host=$($_.Key):$($_.Value)" }) -join ' '
    Write-Log "  条目 $entries 个；可执行文件 $($execExpected.Count) 个；宿主字段 $hostDesc"

    if ($hosts.Keys.Count -ne 1 -or -not $hosts.ContainsKey('3')) {
        throw "宿主字段未全部标记为 Unix(3)：$hostDesc"
    }
    if ($bad.Count -gt 0) {
        $bad | ForEach-Object { Write-Host "  ✗ $_" }
        throw "权限位校验失败，共 $($bad.Count) 项"
    }
    Write-Log "  权限位与宿主字段校验通过"
}

switch ($Target) {
    'all'   { Build-Go; Build-ModuleZip; Test-Package }
    'go'    { Build-Go }
    'zip'   { Build-ModuleZip; Test-Package }
    'clean' {
        Write-Log "清理构建产物"
        foreach ($d in @($BinDir, $DistDir)) {
            if (Test-Path -LiteralPath $d) { Remove-Item -LiteralPath $d -Recurse -Force }
        }
        Write-Log "完成"
    }
}
Write-Log "全部完成"
