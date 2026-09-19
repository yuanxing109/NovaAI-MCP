#Requires -Version 5.1
<#
NovaAI-MCP 模块侧静态审计

配套 scripts/audit_actions.ps1（那里审的是工具 action 契约，这里审的是模块
生命周期脚本与构建脚本）。两者都只做**机械可判定**的检查，可重复运行。

覆盖边界（重要，不要把它当成全覆盖）：
  - 只做文本/结构比对，不执行任何 shell 脚本。模块在真机上的安装、启动、
    看门狗、卸载流程仍**没有**自动化验证台架 —— 本脚本全绿不等于模块装得上。
  - "退役符号零引用"只扫**代码行**：以注释标记（# // *）开头的行与 .md 文档
    被排除。文档里解释"为什么删掉 manual-stop"是应该保留的，不算残留引用。
  - 本脚本自身（scripts/audit_*.ps1）被排除：它必须写下这些符号才能检查它们。
    这是刻意的边界，不是漏洞 —— 但它意味着检查器不能自我校验。
  - 不能判断"函数读了一个参数但读完什么也不做"这类语义缺陷
    （novaai_log 的 follow 参数就是这一类，已由 Go 测试覆盖）。
  - 不检查 zip 产物的权限位 —— 那由 scripts/verify_package.ps1 负责；第 3 项只
    比对"可执行矩阵与 build.sh 的 chmod 目标是否一致"。

退出码：任一检查失败则为 1。
#>

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$fail = 0

function Write-Check([string]$Name, [string[]]$Problems) {
    if ($Problems.Count -eq 0) {
        Write-Host ("[ OK ] {0}" -f $Name)
    } else {
        Write-Host ("[FAIL] {0}" -f $Name) -ForegroundColor Red
        foreach ($p in $Problems) { Write-Host ("       - {0}" -f $p) -ForegroundColor Red }
        $script:fail = 1
    }
}

# 被审计的代码文件：只含承载行为的文件类型。
# 排除 .md（文档要解释退役原因）、.json（数据）、dist/、.git/、.backup/，
# 以及 scripts/audit_*.ps1（检查器自身含符号表）。
$codeFiles = @(Get-ChildItem -LiteralPath $Root -Recurse -File |
    Where-Object {
        $_.FullName -notmatch '\\\.git\\' -and
        $_.FullName -notmatch '\\dist\\' -and
        $_.FullName -notmatch '\\\.backup\\' -and
        $_.FullName -notmatch '\\scripts\\audit_[^\\]*\.ps1$' -and
        ($_.Extension -in @('.sh', '.ps1', '.go', '.prop', '.rule') -or $_.Name -eq 'update-binary')
    })

# 注释行判定：Go/PowerShell 的 //、shell/PowerShell 的 #、块注释续行 *。
# 只按行首判断，所以行尾注释仍会被计入 —— 那是保守方向（宁可误报）。
function Test-IsCommentLine([string]$Line) {
    $t = $Line.TrimStart()
    return ($t.StartsWith('#') -or $t.StartsWith('//') -or $t.StartsWith('*') -or $t.StartsWith('/*'))
}

# 在代码行里搜一个模式，返回 "文件:行号:内容"。
function Find-CodeHits([string]$Pattern, [switch]$CaseSensitive) {
    $out = @()
    foreach ($f in $codeFiles) {
        $i = 0
        foreach ($line in (Get-Content -LiteralPath $f.FullName)) {
            $i++
            if (Test-IsCommentLine $line) { continue }
            $opts = [System.Text.RegularExpressions.RegexOptions]::None
            if (-not $CaseSensitive) { $opts = [System.Text.RegularExpressions.RegexOptions]::IgnoreCase }
            if ([regex]::IsMatch($line, $Pattern, $opts)) {
                $rel = $f.FullName.Substring($Root.Length + 1)
                $out += ("{0}:{1}: {2}" -f $rel, $i, $line.Trim())
            }
        }
    }
    return $out
}

# ---------------------------------------------------------------- 1. 退役符号
# 这些对象在本轮被移除；任何残留的**代码**引用都说明删除不彻底，或有人加回来了。
$retired = @(
    @{ Sym = 'manual-stop';               Why = 'G1 零读者的"手动停止标志"' },
    @{ Sym = 'zcr_print_summary';         Why = 'G10 零调用，被 action.sh 取代' },
    @{ Sym = 'module_path';               Why = 'G13 只写不读的元数据文件' },
    @{ Sym = 'install_time';              Why = 'G13 只写不读的元数据文件' },
    @{ Sym = 'ZIPFILE:-$3';               Why = 'G14 永远走不到的位置参数回退' },
    @{ Sym = 'zcr_start_supervisor auto'; Why = 'G9 函数体从不读取的实参' }
)
$problems = @()
foreach ($r in $retired) {
    foreach ($h in (Find-CodeHits -Pattern ([regex]::Escape($r.Sym)) -CaseSensitive)) {
        $problems += ("{0}（{1}）" -f $h, $r.Why)
    }
}
Write-Check '1. 退役符号零引用（G1/G9/G10/G13/G14）' $problems

# ------------------------------------------------- 2. PID 文件单一 owner（G2）
# daemon 自己写 PID；shell 只读。旧实现由 shell 写 `$!`（su 的 PID），
# 而 daemon 是 su 的孙进程，停止信号到不了它。
$problems = @()
foreach ($h in (Find-CodeHits -Pattern '>\s*"?\$ZCR_PID_FILE')) {
    $problems += ("shell 仍在写 PID 文件: {0}" -f $h)
}
$mainSrc = Get-Content -LiteralPath (Join-Path $Root 'src\cmd\novaaimcpd\main.go') -Raw
if ($mainSrc -notmatch 'writePIDFile\(pidPath\)') {
    $problems += 'main.go 不再调用 writePIDFile(pidPath) —— PID 文件没有 owner 了'
}
if ($mainSrc -notmatch 'pidFileName\s*=\s*"novaaimcpd\.pid"') {
    $problems += 'main.go 的 pidFileName 不再是 novaaimcpd.pid，与 common.sh 漂移'
}
if ((Get-Content -LiteralPath (Join-Path $Root 'common.sh') -Raw) -notmatch 'novaaimcpd\.pid') {
    $problems += 'common.sh 不再引用 novaaimcpd.pid'
}
Write-Check '2. PID 文件由 daemon 独占写入（G2）' $problems

# ------------------------------------- 3. 可执行集合一致：build.sh ⊇ 可执行矩阵
# G3 是这一类缺陷的一个实例：矩阵要求 bin/*/7zz 必须是 0755，而 build.sh 漏了
# chmod，于是 Linux 检出的包会被 Windows 侧校验器判失败。
# 矩阵的唯一声明点是 scripts/package_contract.ps1（build.ps1 与
# verify_package.ps1 都点源它）。这里把它抽出来，逐个验证 build.sh 的 chmod
# 目标能覆盖它。
$problems = @()
$contractPath = Join-Path $Root 'scripts\package_contract.ps1'
$fnMatch = $null
if (-not (Test-Path -LiteralPath $contractPath)) {
    $problems += 'scripts/package_contract.ps1 不存在 —— 可执行矩阵没有唯一声明点'
} else {
    $contractSrc = Get-Content -LiteralPath $contractPath -Raw
    $fnMatch = [regex]::Match($contractSrc, 'function Test-Executable.*?\n\s*\}', 'Singleline')
    if (-not $fnMatch.Success) {
        $problems += 'package_contract.ps1 里找不到 Test-Executable 函数 —— 提取失败，检查无法进行'
    }
}
if ($null -ne $fnMatch -and $fnMatch.Success) {
    $rules = @([regex]::Matches($fnMatch.Value, "(?:-like|-eq)\s+'([^']+)'") |
               ForEach-Object { $_.Groups[1].Value })
    if ($rules.Count -eq 0) {
        $problems += 'Test-Executable 里没有提取到任何模式 —— 提取失败，检查无法进行'
    }

    $shSrc = Get-Content -LiteralPath (Join-Path $Root 'build.sh') -Raw
    $shTargets = @([regex]::Matches($shSrc, '\$STAGE[^\s;)]*') | ForEach-Object {
        $_.Value.Replace('"', '').Replace("'", '') -replace '^\$STAGE', '' -replace '^/', ''
    } | Where-Object { $_ -ne '' })

    foreach ($r in $rules) {
        $sample = $r -replace 'bin/\*/', 'bin/arm64-v8a/'
        $sample = $sample -replace '\*\.', 'service.'
        $sample = $sample -replace '\*', 'x'
        $covered = @($shTargets | Where-Object { $sample -like $_ })
        if ($covered.Count -eq 0) {
            $problems += ("可执行矩阵要求 {0} 可执行（样本 {1}），但 build.sh 没有任何 chmod 目标覆盖它" -f $r, $sample)
        }
    }
    Write-Host ("       已比对 {0} 条矩阵规则 / {1} 个 build.sh 目标" -f $rules.Count, $shTargets.Count)
}
Write-Check '3. build.sh 覆盖可执行矩阵的全部规则（G3/G12）' $problems

# --------------------------------------------------- 4. 版本号唯一来源（G11）
# 唯一 owner 是 module.prop；构建与展示脚本都必须读它，不得内联字面量。
$problems = @()
$expectReads = @{
    'build.sh'     = 'module\.prop'
    'build.ps1'    = 'module\.prop'
    'customize.sh' = 'module\.prop'
    'action.sh'    = 'zcr_module_version'
}
foreach ($name in $expectReads.Keys) {
    $src = Get-Content -LiteralPath (Join-Path $Root $name) -Raw
    if ($src -notmatch $expectReads[$name]) {
        $problems += ("{0} 不再从 module.prop 读取版本（未找到 {1}）" -f $name, $expectReads[$name])
    }
}
$literalRules = @(
    @{ File = 'build.sh';     Re = '(?m)^\s*VERSION="?\d' },
    @{ File = 'build.sh';     Re = 'NovaAI-MCP-v\d' },
    @{ File = 'build.ps1';    Re = "(?m)^\s*\$Version\s*=\s*['""]?\d" },
    @{ File = 'build.ps1';    Re = "NovaAI-MCP-v\d" },
    @{ File = 'customize.sh'; Re = 'MODVER="\d' },
    @{ File = 'action.sh';    Re = 'NovaAI-MCP v\d' }
)
foreach ($rule in $literalRules) {
    $src = Get-Content -LiteralPath (Join-Path $Root $rule.File) -Raw
    if ($src -match $rule.Re) {
        $problems += ("{0} 里仍有内联版本字面量（匹配 {1}）" -f $rule.File, $rule.Re)
    }
}
Write-Check '4. 版本号唯一来源为 module.prop（G11）' $problems

# --------------------------------------------------- 5. jar 单一副本（G4）
$problems = @()
foreach ($h in (Find-CodeHits -Pattern 'novaai-mcp/tools' -CaseSensitive)) {
    $problems += ("仍从状态目录读工具 jar: {0}" -f $h)
}
$revSrc = Get-Content -LiteralPath (Join-Path $Root 'src\internal\tools\v02\reverse.go') -Raw
if ($revSrc -notmatch 'apktoolJarPath\(\)') {
    $problems += 'reverse.go 不再使用 apktoolJarPath() —— jar 路径来源不明'
}
if ((Get-Content -LiteralPath (Join-Path $Root 'customize.sh') -Raw) -match 'cp\s+"\$MODDIR/bin/tools/"') {
    $problems += 'customize.sh 又把 jar 复制到状态目录了'
}
Write-Check '5. 工具 jar 只有模块内一份（G4）' $problems

# --------------------------------------------------- 6. update-binary 发现规则
# G5：不再枚举框架路径，改为找第一个提供 install_module 的 util_functions.sh。
$problems = @()
$ub = Get-Content -LiteralPath (Join-Path $Root 'META-INF\com\google\android\update-binary') -Raw
if ($ub -notmatch 'for cand in') {
    $problems += 'update-binary 不再使用路径发现循环（回到硬编码枚举）'
}
if ($ub -notmatch 'grep -q .install_module.') {
    $problems += 'update-binary 在 source 之前不再校验 install_module，可能 source 到无关文件'
}
Write-Check '6. update-binary 用运行时发现而非硬编码路径（G5）' $problems

# ------------------------------------------- 7. shell 结构配平（无 shell 可执行）
# 开发机上没有可用于 `sh -n` 的 POSIX shell，模块脚本又不能在 Windows 上执行，
# 所以退一步做关键字配平。它抓不到所有语法错误（配平 ≠ 语法正确），但能抓到
# "加了 if 忘了 fi"这类最常见的手误 —— 那种错误会让整个脚本在设备上直接不执行，
# 而静态审计的其他项全部照常通过。
$problems = @()
$rules = @(
    @{ Name = 'if/fi';        Open = @('if');           Close = @('fi') },
    @{ Name = 'case/esac';    Open = @('case');         Close = @('esac') },
    @{ Name = 'while,for/done'; Open = @('while', 'for'); Close = @('done') }
)
$shellFiles = @(Get-ChildItem -LiteralPath $Root -Filter '*.sh' -File)
$shellFiles += Get-Item -LiteralPath (Join-Path $Root 'META-INF\com\google\android\update-binary')
foreach ($f in $shellFiles) {
    $body = (@(Get-Content -LiteralPath $f.FullName) |
             Where-Object { -not (Test-IsCommentLine $_) }) -join "`n"
    foreach ($r in $rules) {
        $openCount = 0
        foreach ($kw in $r.Open) {
            $openCount += ([regex]::Matches($body, "(?m)(^|\s)$kw(\s|$)")).Count
        }
        $closeCount = 0
        foreach ($kw in $r.Close) {
            $closeCount += ([regex]::Matches($body, "(?m)(^|\s)$kw(\s|$)")).Count
        }
        if ($openCount -ne $closeCount) {
            $problems += ("{0}: {1} 开 {2} 次 / 闭 {3} 次" -f $f.Name, $r.Name, $openCount, $closeCount)
        }
    }
}
Write-Check '7. shell 结构配平（if/fi, case/esac, while,for/done）' $problems

# ------------------------------------------- 8. CI 不得成为第三个打包实现
# 打包的 Unix owner 是 build.sh，Windows owner 是 build.ps1。工作流必须**调用**
# build.sh，而不是在 YAML 里重新实现编译/打包 —— 那会变成第三份清单，与第 1 节
# 记录的漂移同类。判据是机械的：出现 go build / zip -r 即视为重新实现。
# 注意这只覆盖 YAML 自身；`run:` 里调用的脚本（探针会 go build）不在此列。
$problems = @()
$wfDir = Join-Path $Root '.github\workflows'
if (-not (Test-Path -LiteralPath $wfDir)) {
    $problems += '.github/workflows 不存在 —— 发布流水线没有 owner'
} else {
    $wfs = @(Get-ChildItem -LiteralPath $wfDir -File |
             Where-Object { $_.Extension -in @('.yml', '.yaml') })
    if ($wfs.Count -eq 0) { $problems += '.github/workflows 下没有工作流文件' }

    $callsBuildSh = $false
    foreach ($w in $wfs) {
        # 与第 1 项同一套注释行策略：YAML 的 # 注释不算代码。
        $body = (@(Get-Content -LiteralPath $w.FullName) |
                 Where-Object { -not (Test-IsCommentLine $_) }) -join "`n"
        if ($body -match '\bgo\s+build\b') {
            $problems += ("{0} 自己调用了 go build —— 编译 owner 是 build.sh" -f $w.Name)
        }
        if ($body -match '\bzip\s+-r') {
            $problems += ("{0} 自己调用了 zip -r —— 打包 owner 是 build.sh" -f $w.Name)
        }
        # 只在 name: 里提到 build.sh 不算调用 —— 那会让"删掉构建步骤"逃过检查。
        foreach ($line in ($body -split "`n")) {
            if ($line -notmatch 'build\.sh') { continue }
            if ($line -match '^\s*(-\s*)?name\s*:') { continue }
            $callsBuildSh = $true
        }
    }
    if (-not $callsBuildSh) {
        $problems += '没有任何工作流调用 build.sh —— CI 可能重新实现了打包'
    }
}
Write-Check '8. CI 调用 build.sh 而非重新实现打包' $problems

Write-Host ''
if ($fail -eq 0) {
    Write-Host '模块侧静态审计：全部通过' -ForegroundColor Green
} else {
    Write-Host '模块侧静态审计：存在失败项' -ForegroundColor Red
}
exit $fail
