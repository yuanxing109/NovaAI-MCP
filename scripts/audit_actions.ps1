# audit_actions.ps1 - 静态审计工具契约与实现的一致性。
#
# 四项检查：
#   1) schema 里声明的 action，是否在 handler 里真的被分派；
#   2) profile/risk.go 的 perActionRisk 里列的 action，是否真的在 schema 里存在；
#   3) 声明了 action，但 handler 从不读 in.Action —— 这个字段是摆设，
#      客户端传什么值都得到同一个行为；
#   4) 声明了 action、handler 也读了，但没有兜底分支 —— 未知 action 会掉进
#      空切片索引而 panic；panic 被 mcp 层的 safeCall 兜住，用户只看到
#      "工具 X 内部 panic"，看不出真正原因；
#   5) schema 声明的参数名，handler 里没有同名 json tag —— 参数是摆设，
#      和假 action 是同一类缺陷（声明了不存在的输入）。
#
# 判定口径是静态近似：跨函数复用的 action 会被误报，输出需人工过一遍。
# 检查 3/4 只是快速定位手段；权威结论以 internal/tools/v02/actions_test.go
# 为准 —— 它用真实 handler 逐个试，不依赖正则。

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$files = Get-ChildItem -Path (Join-Path $root 'src\internal\tools') -Recurse -Filter *.go |
    Where-Object { $_.Name -notlike '*_test.go' }

$totalTools = 0
$totalMissing = 0
$totalVestigial = 0
$totalNoCatchAll = 0
$totalUnreadParam = 0
$declaredByTool = @{}

# JSON Schema 关键字不是参数名，检查 5 必须排除，否则每个 object 型参数
# 都会因为内部的 type/additionalProperties 而误报。
$schemaKeywords = @('type', 'items', 'additionalProperties', 'properties',
    'required', 'enum', 'description', 'format', 'default', 'minimum',
    'maximum', 'oneOf', 'anyOf', 'const', 'pattern')

# Get-SchemaBody 取出 objSchema(...) 的括号内文本。
# 用配对计数而不是正则：schema 内部的 map/struct 字面量同样有括号。
function Get-SchemaBody([string]$block) {
    $m = [regex]::Match($block, 'objSchema\(')
    if (-not $m.Success) { return $null }

    $depth = 0
    for ($i = $m.Index + $m.Length - 1; $i -lt $block.Length; $i++) {
        switch ($block[$i]) {
            '(' { $depth++ }
            ')' {
                $depth--
                if ($depth -eq 0) { return $block.Substring($m.Index + $m.Length, $i - $m.Index - $m.Length) }
            }
        }
    }
    return $null
}

foreach ($f in $files) {
    $text = Get-Content -Raw -LiteralPath $f.FullName

    # 以 reg("name" ... 为切分点，把文件拆成若干工具块
    $marks = [regex]::Matches($text, 'reg\(\s*"(?<name>[a-z0-9_]+)"')
    for ($i = 0; $i -lt $marks.Count; $i++) {
        $start = $marks[$i].Index
        $end = if ($i + 1 -lt $marks.Count) { $marks[$i + 1].Index } else { $text.Length }
        $block = $text.Substring($start, $end - $start)
        $name = $marks[$i].Groups['name'].Value
        $totalTools++

        # ---- 检查 5：schema 参数名必须在 handler 里有同名 json tag ----
        # 放在 action 判定之前：没有 action 的工具同样可能有摆设参数。
        #
        # 这里必须做括号配对扫描，不能用 `.*?\n\s*\},` 之类的正则：
        # objSchema 有两种收尾（`}, "req")` 与 `})`），非贪婪正则在无 required
        # 的 schema 上会一路吃到 handler 内部的 `},`，把返回值字段当成参数名。
        $body = Get-SchemaBody $block
        if ($null -ne $body) {
            $unread = @()
            foreach ($p in [regex]::Matches($body, '"([A-Za-z_][A-Za-z0-9_]*)"\s*:')) {
                $prop = $p.Groups[1].Value
                if ($schemaKeywords -contains $prop) { continue }
                if ($unread -contains $prop) { continue }
                # handler 用 struct tag 反序列化；没有 tag 就是从不读取。
                if (-not [regex]::IsMatch($block, ('json:"' + [regex]::Escape($prop) + '"'))) {
                    $unread += $prop
                }
            }
            if ($unread.Count -gt 0) {
                $totalUnreadParam += $unread.Count
                Write-Host ("[摆设参数] {0,-28} schema 声明但 handler 不读: {1}" -f $name, ($unread -join ', '))
            }
        }

        # 精确定位 action 的枚举：必须是 "action": enumProp(...)。
        # 早期版本用"块里第一个 enumProp"，一旦某个工具把 identity/namespace
        # 之类的枚举写在 action 前面，就会整体错位。
        $actionEnum = [regex]::Match($block, '"action"\s*:\s*enumProp\((?<vals>[^)]*)\)')
        if (-not $actionEnum.Success) { continue }

        $declared = @()
        foreach ($s in [regex]::Matches($actionEnum.Groups['vals'].Value, '"([^"]+)"')) {
            $v = $s.Groups[1].Value
            if ($v -match '[\u4e00-\u9fff]') { continue }  # 跳过中文标题参数
            $declared += $v
        }
        if ($declared.Count -eq 0) { continue }
        $declaredByTool[$name] = $declared

        # handler 是否真的读了 action。
        $usesAction = [regex]::IsMatch($block, 'in\.Action')
        if (-not $usesAction) {
            $totalVestigial++
            Write-Host ("[摆设字段] {0,-28} 声明了 action 但 handler 从不读 in.Action" -f $name)
            continue
        }

        # case "..." = 实际处理的 action。
        # 必须支持 `case "a", "b":` 这种合并写法，否则第二个字面量会被
        # 误判成"声明了但没处理"（早期版本就漏掉了 update / kill 这类）。
        $caseLits = @()
        foreach ($m in [regex]::Matches($block, 'case\s+((?:"[^"]+"\s*,?\s*)+)\s*:')) {
            foreach ($s in [regex]::Matches($m.Groups[1].Value, '"([^"]+)"')) {
                $caseLits += $s.Groups[1].Value
            }
        }
        # 若没有任何 case 字面量命中声明的 action，说明这些 case 属于嵌套的
        # 其他字段分派（例如 shell/script 里的 identity 开关 root/shell/uid），
        # 不能当作 action 分派。
        $handled = @()
        if (@($caseLits | Where-Object { $declared -contains $_ }).Count -gt 0) {
            $handled += $caseLits
        }
        # 另两种分派写法：if in.Action == "validate" / if in.Action != "run"
        foreach ($m in [regex]::Matches($block, 'Action\s*[!=]=\s*"([^"]+)"')) {
            $handled += $m.Groups[1].Value
        }

        $missing = $declared | Where-Object { $handled -notcontains $_ } | Select-Object -Unique
        if ($missing.Count -gt 0) {
            $totalMissing += $missing.Count
            Write-Host ("[未实现] {0,-28} {1}" -f $name, ($missing -join ', '))
        }

        # 兜底分支：未知 action 必须落到 UNKNOWN_ACTION，而不是继续往下走。
        if (-not [regex]::IsMatch($block, 'UNKNOWN_ACTION')) {
            $totalNoCatchAll++
            Write-Host ("[无兜底] {0,-28} 未知 action 没有 UNKNOWN_ACTION 出口" -f $name)
        }
    }
}

# ---- 第二遍：perActionRisk 里的 action 必须在 schema 里真的声明过 ----
$riskPath = Join-Path $root 'src\internal\profile\risk.go'
$riskText = Get-Content -Raw -LiteralPath $riskPath
$totalPhantom = 0

foreach ($m in [regex]::Matches($riskText, '"([a-z0-9_]+)"\s*:\s*\{(?<body>[^}]*)\}')) {
    $tool = $m.Groups[1].Value
    $body = $m.Groups['body'].Value
    $riskActions = @()
    foreach ($a in [regex]::Matches($body, '"([a-z0-9_]+)"\s*:')) {
        $riskActions += $a.Groups[1].Value
    }
    if ($riskActions.Count -eq 0) { continue }

    if (-not $declaredByTool.ContainsKey($tool)) {
        # 工具没有 action 枚举（单动作），风险表却按 action 分级 —— 可疑
        $totalPhantom += $riskActions.Count
        Write-Host ("[无枚举] {0,-28} 风险表列了 action 但 schema 没有枚举: {1}" -f $tool, ($riskActions -join ', '))
        continue
    }
    $declared = $declaredByTool[$tool]
    $phantom = $riskActions | Where-Object { $declared -notcontains $_ } | Select-Object -Unique
    if ($phantom.Count -gt 0) {
        $totalPhantom += $phantom.Count
        Write-Host ("[幽灵项] {0,-28} 风险表有、schema 无: {1}" -f $tool, ($phantom -join ', '))
    }
}

Write-Host ""
Write-Host "扫描工具数: $totalTools"
Write-Host "声明但无 case 的 action 总数: $totalMissing"
Write-Host "摆设 action 字段总数: $totalVestigial"
Write-Host "缺兜底分支的工具总数: $totalNoCatchAll"
Write-Host "摆设参数总数: $totalUnreadParam"
Write-Host "风险表幽灵 action 总数: $totalPhantom"
if ($totalMissing -eq 0 -and $totalPhantom -eq 0 -and $totalVestigial -eq 0 -and
    $totalNoCatchAll -eq 0 -and $totalUnreadParam -eq 0) {
    Write-Host "结论: 工具契约与实现一致"
}
