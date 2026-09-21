# audit_skills.ps1 - 审计 skills/*.md 与工具契约的一致性。
#
# 为什么需要它：skills/*.md 没有任何工具入口（novaai_skill 已删），客户端只能
# 通过 novaai_fs_read 读。文档里写错一个工具名、action 或参数名，AI 就会照着
# 调一个不存在的东西 —— 2026-09-21 用同口径的一次性脚本查出 5 处这类错误
# （含 `fs_search action: content` 用 `pattern` 而不是 `query`，实际会执行
# `grep -r -l '' <root>`，静默返回所有文件）。
#
# 五项检查：
#   1) 引用的工具名必须存在于注册表；
#   2) 被引用但解析不出 schema 的工具必须报出来（检查器自己的盲点）；
#   3) 调用的 action 必须在该工具 schema 的枚举里；
#   4) 用到的参数名必须在该工具 schema 里；
#   5) 引用的 bin/tools/*.jar 必须真的存在。
#
# 检查 2 是刻意的：解析失败就跳过等于给了一条静默通过的暗道。
# 判定口径是静态近似：只认 `novaai_X → action: y, p: v` 这种写法，
# 散文里的工具名只做存在性检查。

param(
    [string]$Root = (Split-Path -Parent $PSScriptRoot)
)

$ErrorActionPreference = 'Stop'
$fail = 0

function Write-Check([string]$Name, $Problems) {
    $list = @($Problems | Where-Object { $_ -and "$_".Trim() -ne '' })
    if ($list.Count -eq 0) {
        Write-Host ("[ OK ] {0}" -f $Name)
    } else {
        Write-Host ("[FAIL] {0}" -f $Name) -ForegroundColor Red
        foreach ($p in $list) { Write-Host ("       - {0}" -f $p) -ForegroundColor Red }
        $script:fail = 1
    }
}

# 脚本以 BOM-less UTF-8 存放，必须显式按 UTF-8 读；用 Get-Content 在
# PowerShell 5.1 下会按 ANSI 解析而吃掉中文后的引号。
function Read-Utf8([string]$Path) {
    $enc = New-Object System.Text.UTF8Encoding($false)
    return [System.IO.File]::ReadAllText($Path, $enc)
}

# 从 $Start（指向开括号）开始配对扫描，返回闭括号下标；找不到返回 -1。
# 不能用非贪婪正则：schema 内部还有 map/struct 字面量，同样带括号。
function Find-Closing([string]$Text, [int]$Start, [char]$Open, [char]$Close) {
    $depth = 0
    for ($i = $Start; $i -lt $Text.Length; $i++) {
        if ($Text[$i] -eq $Open) { $depth++ }
        elseif ($Text[$i] -eq $Close) {
            $depth--
            if ($depth -eq 0) { return $i }
        }
    }
    return -1
}

# ---------------------------------------------------------------- Go 侧真相
$toolNames = New-Object 'System.Collections.Generic.HashSet[string]'
$propsOf = @{}
$enumOf = @{}

$goFiles = Get-ChildItem -Path (Join-Path $Root 'src\internal\tools') -Recurse -Filter *.go |
    Where-Object { $_.Name -notlike '*_test.go' }

foreach ($f in $goFiles) {
    $text = Read-Utf8 $f.FullName

    foreach ($m in [regex]::Matches($text, 'reg\(\s*"(?<n>novaai_[a-z0-9_]+)"')) {
        $name = $m.Groups['n'].Value
        [void]$toolNames.Add($name)

        $next = $text.IndexOf('reg(', $m.Index + $m.Length)
        $end = if ($next -gt 0) { $next } else { $text.Length }
        $block = $text.Substring($m.Index, $end - $m.Index)

        $sm = [regex]::Match($block, 'objSchema\(')
        if (-not $sm.Success) { continue }
        $sClose = Find-Closing $block ($sm.Index + $sm.Length - 1) '(' ')'
        if ($sClose -lt 0) { continue }
        $schema = $block.Substring($sm.Index, $sClose - $sm.Index)

        $mm = [regex]::Match($schema, 'map\[string\]any\{')
        if (-not $mm.Success) { continue }
        $bStart = $mm.Index + $mm.Length
        $bClose = Find-Closing $schema ($bStart - 1) '{' '}'
        if ($bClose -lt 0) { continue }
        $body = $schema.Substring($bStart, $bClose - $bStart)

        if (-not $propsOf.ContainsKey($name)) {
            $propsOf[$name] = New-Object 'System.Collections.Generic.HashSet[string]'
        }
        foreach ($p in [regex]::Matches($body, '"([A-Za-z_][A-Za-z0-9_]*)"\s*:')) {
            [void]$propsOf[$name].Add($p.Groups[1].Value)
        }

        $am = [regex]::Match($body, '"action"\s*:\s*enumProp\(')
        if ($am.Success) {
            $aStart = $am.Index + $am.Length
            $aClose = Find-Closing $body ($aStart - 1) '(' ')'
            if ($aClose -gt 0) {
                $vals = [regex]::Matches($body.Substring($aStart, $aClose - $aStart), '"([^"]*)"')
                if ($vals.Count -gt 1) {
                    if (-not $enumOf.ContainsKey($name)) {
                        $enumOf[$name] = New-Object 'System.Collections.Generic.HashSet[string]'
                    }
                    # 第一个字面量是中文描述，其余才是合法值
                    for ($k = 1; $k -lt $vals.Count; $k++) {
                        [void]$enumOf[$name].Add($vals[$k].Groups[1].Value)
                    }
                }
            }
        }
    }

    # 直注册通路（internal/tools/*.go 里的 reg.Register(&Tool{Name: "..."})）。
    # 它们的 schema 是字面量而不是 objSchema(...)，所以参数表要从
    # "properties": map[string]any{...} 里取 —— 不解析它们，检查 2 会一直
    # 报"盲点"，而那正是这个检查要暴露的东西，不能靠忽略绕过去。
    foreach ($m in [regex]::Matches($text, 'Name:\s*"(?<n>novaai_[a-z0-9_]+)"')) {
        $name = $m.Groups['n'].Value
        [void]$toolNames.Add($name)

        $next = $text.IndexOf('Name:', $m.Index + $m.Length)
        $end = if ($next -gt 0) { $next } else { $text.Length }
        $block = $text.Substring($m.Index, $end - $m.Index)

        $pm = [regex]::Match($block, '"properties"\s*:\s*map\[string\]any\{')
        if (-not $pm.Success) { continue }
        $pStart = $pm.Index + $pm.Length
        $pClose = Find-Closing $block ($pStart - 1) '{' '}'
        if ($pClose -lt 0) { continue }
        if (-not $propsOf.ContainsKey($name)) {
            $propsOf[$name] = New-Object 'System.Collections.Generic.HashSet[string]'
        }
        foreach ($p in [regex]::Matches($block.Substring($pStart, $pClose - $pStart),
                                        '"([A-Za-z_][A-Za-z0-9_]*)"\s*:')) {
            [void]$propsOf[$name].Add($p.Groups[1].Value)
        }
    }
}

# ---------------------------------------------------------------- skills 侧
$unknown = @()
$badAction = @()
$badParam = @()
$badJar = @()
$unchecked = New-Object 'System.Collections.Generic.HashSet[string]'
$referenced = New-Object 'System.Collections.Generic.HashSet[string]'
$skillFiles = @(Get-ChildItem -Path (Join-Path $Root 'skills') -Filter *.md)
$schemaTools = 0

foreach ($f in $skillFiles) {
    $text = Read-Utf8 $f.FullName
    $lines = $text -split "`n"

    foreach ($m in [regex]::Matches($text, 'novaai_(?<n>[a-z0-9_]+)')) {
        $full = 'novaai_' + $m.Groups['n'].Value
        if (-not $toolNames.Contains($full)) {
            $ln = ($text.Substring(0, $m.Index) -split "`n").Count
            $unknown += ("{0}:{1}  {2}" -f $f.Name, $ln, $full)
        } else {
            [void]$referenced.Add($full)
        }
    }

    for ($i = 0; $i -lt $lines.Count; $i++) {
        $cm = [regex]::Match($lines[$i], 'novaai_(?<n>[a-z0-9_]+)\s*→\s*(?<rest>.*)$')
        if (-not $cm.Success) { continue }

        $tool = 'novaai_' + $cm.Groups['n'].Value
        $body = $cm.Groups['rest'].Value
        $j = $i + 1
        while ($j -lt $lines.Count -and $lines[$j] -match '^\s' -and
               $lines[$j].Trim() -ne '' -and $lines[$j] -notmatch '→') {
            $body += "`n" + $lines[$j]
            $j++
        }
        $i = $j - 1   # for 的 $i++ 会回到 $j

        if (-not $propsOf.ContainsKey($tool)) {
            [void]$unchecked.Add($tool)
            continue
        }

        # 去掉引号里的内容：URL 与 shell 命令里的冒号都在引号里，
        # 不去掉会把 `https:` / `-s Tag:E` 当成参数名。
        $clean = [regex]::Replace($body, '"(?:[^"\\]|\\.)*"', '""')
        $clean = [regex]::Replace($clean, "'(?:[^'\\]|\\.)*'", "''")

        $am = [regex]::Match($clean, 'action:\s*(?<a>[a-z_0-9]+)')
        if ($am.Success -and $enumOf.ContainsKey($tool)) {
            $act = $am.Groups['a'].Value
            if (-not $enumOf[$tool].Contains($act)) {
                $legal = (@($enumOf[$tool]) | Sort-Object) -join ', '
                $badAction += ("{0}:{1}  {2} → {3}   合法: {4}" -f $f.Name, ($i + 1), $tool, $act, $legal)
            }
        }

        foreach ($pm in [regex]::Matches($clean, '([A-Za-z_][A-Za-z0-9_]*)\s*:')) {
            $key = $pm.Groups[1].Value
            if ($key -eq 'action' -or $key -eq 'http' -or $key -eq 'https') { continue }
            if (-not $propsOf[$tool].Contains($key)) {
                $known = (@($propsOf[$tool]) | Sort-Object) -join ', '
                $badParam += ("{0}:{1}  {2}.{3}   该工具参数: {4}" -f $f.Name, ($i + 1), $tool, $key, $known)
            }
        }
    }

    foreach ($m in [regex]::Matches($text, 'bin/tools/(?<j>[a-z0-9_.-]+\.jar)')) {
        $jar = $m.Groups['j'].Value
        if (-not (Test-Path -LiteralPath (Join-Path $Root "bin\tools\$jar"))) {
            $badJar += ("{0}: bin/tools/{1}" -f $f.Name, $jar)
        }
    }
}

$schemaTools = $propsOf.Keys.Count
$checkable = @($referenced | Where-Object { $propsOf.ContainsKey($_) }).Count

Write-Host ''
Write-Check '1. 引用的工具名都存在于注册表' ($unknown | Select-Object -Unique)
Write-Check '2. 被引用但 schema 未解析（这些行等于没校验）' `
    (@($unchecked | Sort-Object) | ForEach-Object { "{0} —— 检查器盲点，需修解析逻辑" -f $_ })
Write-Check '3. action 在该工具的 schema 枚举里' ($badAction | Select-Object -Unique)
Write-Check '4. 参数名在该工具的 schema 里' ($badParam | Select-Object -Unique)
Write-Check '5. 引用的 bin/tools/*.jar 存在' ($badJar | Select-Object -Unique)

Write-Host ''
Write-Host ("注册工具: {0} 个；解析出参数表的: {1} 个" -f $toolNames.Count, $schemaTools)
Write-Host ("skills 文件: {0} 个；引用了 {1} 个工具，其中可校验 {2} 个" -f
    $skillFiles.Count, $referenced.Count, $checkable)

if ($fail -eq 0) {
    Write-Host 'skills 契约审计：全部通过' -ForegroundColor Green
} else {
    Write-Host 'skills 契约审计：存在失败项' -ForegroundColor Red
}
exit $fail
