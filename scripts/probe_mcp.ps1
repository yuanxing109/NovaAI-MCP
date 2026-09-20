# NovaAI-MCP 协议合规回归测试
#
# 默认自举：脚本自行构建 daemon、生成隔离 state 目录并在结束时清理，不依赖设备。
#   pwsh -File scripts/probe_mcp.ps1
#
# 也可指向外部已在运行的服务（例如设备上的实例）：
#   pwsh -File scripts/probe_mcp.ps1 -Port 5322 -HostName 192.168.1.20
#   （本服务不鉴权，没有 -Token 参数。）
#
# 覆盖：initialize 协商、通知无响应、tools/list、tools/call 的 MCP content 包装、
#       未知工具错误码、批量请求、无鉴权通路、DNS rebinding（Host 校验）、
#       Origin 校验、限流、会话复用、default 档位放行、pathguard（含
#       /sdcard/Android/{data,obb} 硬拒绝）。

param(
  [int]$Port = 15322,
  [string]$HostName = '127.0.0.1',
  # 本地工具数。上游工具不在其中 —— 数量随配置变化，见 docs/upstream.md。
  [int]$ExpectTools = 30,
  [string]$StateDir = "$env:TEMP\nova-mcp-probe",
  [string]$GoExe = 'go',
  [switch]$External
)

$ErrorActionPreference = 'Stop'
$base = "http://${HostName}:${Port}/mcp"
$pass = 0; $fail = 0

function Check($name, $cond, $detail) {
  if ($cond) { $script:pass++; Write-Host ("  PASS  {0}" -f $name) }
  else { $script:fail++; Write-Host ("  FAIL  {0}  -> {1}" -f $name, $detail) }
}

# ---- 自举：默认自行构建并启动一个隔离 daemon ----
# -External 时按"外部已在运行的服务"处理，不做构建与清理。
$root = Split-Path -Parent $PSScriptRoot
$proc = $null
if (-not $External) {
  $exe = Join-Path $env:TEMP 'novaaimcpd_mcpprobe.exe'
  Push-Location (Join-Path $root 'src')
  & $GoExe build -o $exe ./cmd/novaaimcpd
  if ($LASTEXITCODE -ne 0) { Pop-Location; Write-Host 'BUILD FAILED'; exit 1 }
  Pop-Location

  if (Test-Path $StateDir) { Remove-Item -Recurse -Force $StateDir }
  New-Item -ItemType Directory -Force -Path $StateDir | Out-Null

  # 先跑一次让 daemon 生成默认 config.json，改掉端口与限流后再正式启动。
  $p0 = Start-Process -FilePath $exe -ArgumentList @('-state', $StateDir) `
    -RedirectStandardOutput "$StateDir\boot1.log" -RedirectStandardError "$StateDir\boot1.err" `
    -PassThru -WindowStyle Hidden
  Start-Sleep -Seconds 2
  Stop-Process -Id $p0.Id -Force -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 500

  $cfgPath = Join-Path $StateDir 'config.json'
  if (-not (Test-Path $cfgPath)) { Write-Host "配置未生成: $cfgPath"; exit 1 }
  $cfg = Get-Content $cfgPath -Raw | ConvertFrom-Json
  $cfg.listen = "127.0.0.1:$Port"
  # 审计目录与 pathguard 的保护前缀取自 config.stateDir（不是 -state 参数），
  # 因此必须一起改，否则在本机（Windows）会落到当前盘的 \data\adb\... 下。
  $cfg.stateDir = $StateDir
  # 压到很小的 QPS，便于在十几发请求内触发 -32009（默认 50 太宽，需要打很久）。
  $cfg.limits.globalQps = 5
  $cfg.limits.shellQps = 5
  $cfg | ConvertTo-Json -Depth 12 | Set-Content $cfgPath -Encoding UTF8

  $proc = Start-Process -FilePath $exe -ArgumentList @('-state', $StateDir) `
    -RedirectStandardOutput "$StateDir\boot2.log" -RedirectStandardError "$StateDir\boot2.err" `
    -PassThru -WindowStyle Hidden
  Start-Sleep -Seconds 2
}

try {

$client = New-Object System.Net.Http.HttpClient
$client.Timeout = [TimeSpan]::FromSeconds(30)

# $hostOverride 已**停用**：连接池预热后 .NET 会丢掉这个覆盖（见 SendRaw 上方）。
# 参数保留是为了让"有人又想用它"时当场炸掉 —— PowerShell 的多余位置参数不会
# 报错，若直接删掉参数，将来传进来的 Host 会被 $args 静默吞掉，然后发出一个
# Host 根本没改过的请求，把断言变成假 PASS 或假 FAIL。
function Send($body, $headers, $hostOverride) {
  if ($hostOverride) {
    throw "Send 的 hostOverride 不可靠（连接池复用时会丢 Host），请改用 SendRaw()"
  }
  $req = New-Object System.Net.Http.HttpRequestMessage('POST', $base)
  $req.Content = New-Object System.Net.Http.StringContent($body, [Text.Encoding]::UTF8, 'application/json')
  foreach ($k in $headers.Keys) { [void]$req.Headers.TryAddWithoutValidation($k, $headers[$k]) }
  $resp = $client.SendAsync($req).Result
  $text = $resp.Content.ReadAsStringAsync().Result
  return [pscustomobject]@{ Status = [int]$resp.StatusCode; Body = $text; Headers = $resp.Headers }
}

# 手写 HTTP 报文，走原始 socket 发。
#
# 为什么 [8] 的 Host 检查不能用上面的 Send（$hostOverride）：
# **连接池预热之后，HttpRequestMessage.Headers.Host 的覆盖到不了线上。**
# 实测（Windows / pwsh 7.6.6）：同一个 HttpClient 先发一发正常请求，再改 Host，
# 服务端收到的仍是 127.0.0.1 —— 冷客户端、换一个新客户端、原始 socket 三种
# 写法都能正确送达，只有"复用已建立的连接"会丢。
#
# 后果是这一项会变成**假 FAIL**：服务端明明按合同把域名 Host 拒了，报文里却
# 根本没有那个域名。旧版（HEAD 就有）一直踩这个坑。
# 手写报文没有这层不确定性：我们写什么字节，服务端就收到什么。
function SendRaw([string]$hostHeader) {
  $tcp = New-Object System.Net.Sockets.TcpClient($HostName, $Port)
  try {
    $payload = '{"jsonrpc":"2.0","id":7,"method":"tools/list"}'
    $raw = "POST /mcp HTTP/1.1`r`n" +
           "Host: $hostHeader`r`n" +
           "Content-Type: application/json`r`n" +
           "Content-Length: $($payload.Length)`r`n" +
           "Connection: close`r`n`r`n" +
           $payload
    $bytes = [Text.Encoding]::ASCII.GetBytes($raw)
    $st = $tcp.GetStream()
    $st.Write($bytes, 0, $bytes.Length)
    $st.Flush()
    $sr = New-Object System.IO.StreamReader($st)
    return $sr.ReadToEnd()
  } finally {
    $tcp.Close()
  }
}

# 本服务不鉴权：auth 表恒为空。保留这个变量是为了让下面的调用点保持对称。
$auth = @{}

Write-Host "`n[1] initialize"
$r = Send '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}' $auth
$j = $r.Body | ConvertFrom-Json
Check 'HTTP 200' ($r.Status -eq 200) $r.Status
Check 'protocolVersion 协商' ($j.result.protocolVersion -eq '2025-06-18') $j.result.protocolVersion
Check '声明 tools 能力' ($null -ne $j.result.capabilities.tools) '缺少 tools 能力'
Check '返回 Mcp-Session-Id' ($null -ne $r.Headers.GetValues('Mcp-Session-Id')) '缺少 session 头'
$sid = ($r.Headers.GetValues('Mcp-Session-Id') | Select-Object -First 1)

$h2 = @{}
$h2['Mcp-Session-Id'] = $sid

Write-Host "`n[2] notifications/initialized（通知不应有响应体）"
$r = Send '{"jsonrpc":"2.0","method":"notifications/initialized"}' $h2
Check 'HTTP 202 且空响应体' ($r.Status -eq 202 -and $r.Body.Trim() -eq '') "status=$($r.Status) body=[$($r.Body)]"

Write-Host "`n[3] tools/list"
$r = Send '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' $h2
$j = $r.Body | ConvertFrom-Json
Check "工具数 = $ExpectTools" ($j.result.tools.Count -eq $ExpectTools) "实际 $($j.result.tools.Count)"
Check '不存在 session_arm 工具' (($j.result.tools | Where-Object { $_.name -like '*session_arm*' }).Count -eq 0) '发现 session_arm'
Check '已裁掉的 novaai_skill 不存在' (($j.result.tools | Where-Object { $_.name -eq 'novaai_skill' }).Count -eq 0) 'novaai_skill 仍在'
Check '已裁掉的 novaai_setting 不存在' (($j.result.tools | Where-Object { $_.name -eq 'novaai_setting' }).Count -eq 0) 'novaai_setting 仍在'
Check '核心工具 novaai_shell 存在' (($j.result.tools | Where-Object { $_.name -eq 'novaai_shell' }).Count -eq 1) '缺少 novaai_shell'
Check '上游状态工具存在' (($j.result.tools | Where-Object { $_.name -eq 'novaai_upstream_status' }).Count -eq 1) '缺少 novaai_upstream_status'
Check '未配置上游时不出现带 __ 的工具' (($j.result.tools | Where-Object { $_.name -like '*__*' }).Count -eq 0) '出现了上游工具'

Write-Host "`n[4] tools/call 正常工具（MCP content 包装）"
$r = Send '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"novaai_status","arguments":{}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check '返回 content 数组' ($null -ne $j.result.content -and $j.result.content.Count -ge 1) $r.Body
Check 'content[0].type = text' ($j.result.content[0].type -eq 'text') $j.result.content[0].type
Check 'isError 不为 true' ($j.result.isError -ne $true) "isError=$($j.result.isError)"

# 路径注意：工具结果是 {success, code, codeName, data}，业务字段在 **data** 下。
# 初版这里写成 structuredContent.security.auth（漏了 data 层）→ 永远拿到 $null
# → 是一条恒失败的断言。写断言前先确认自己在读的层级真的存在。
$sd = $j.result.structuredContent.data
Check 'structuredContent.data 存在' ($null -ne $sd) $r.Body
Check 'security.auth = none' ($sd.security.auth -eq 'none') "auth=$($sd.security.auth)"
Check 'security.profile = default' ($sd.security.profile -eq 'default') "profile=$($sd.security.profile)"
Check 'address.tcp 暴露 listen（唯一的权限旋钮）' (-not [string]::IsNullOrEmpty($sd.address.tcp)) "tcp=$($sd.address.tcp)"

# 已删除的配置段不得回归。前置条件（data 非空）写进条件本身：data 一旦缺失，
# 这几条必须**失败**，而不是因为读不到东西而恒真 —— 后者正是"永不失败的检查"。
# 判定用序列化后的文本，所以嵌套任意深度也拦得住。
$flat = if ($null -ne $sd) { $sd | ConvertTo-Json -Depth 8 } else { '<data 缺失>' }
$flatPreview = $flat.Substring(0, [Math]::Min(120, $flat.Length))
Check '已删除的 security.token 未回归' ($null -ne $sd -and $flat -notmatch '"token"') $flatPreview
Check '已删除的 security.lan 未回归' ($null -ne $sd -and $flat -notmatch '"lan"') $flatPreview
Check '已删除的 sessionBinding 未回归' ($null -ne $sd -and $flat -notmatch 'sessionBinding') $flatPreview

Write-Host "`n[5] tools/call 未知工具"
$r = Send '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"novaai_does_not_exist","arguments":{}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check 'code = -32015' ($j.error.code -eq -32015) $r.Body

Write-Host "`n[6] 批量请求（含通知，应被过滤）"
$r = Send '[{"jsonrpc":"2.0","id":10,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":11,"method":"ping"}]' $h2
$j = $r.Body | ConvertFrom-Json
Check '返回数组且过滤通知' ($j.Count -eq 2) "返回 $($j.Count) 项"

Write-Host "`n[7] 无鉴权通路"
$r = Send '{"jsonrpc":"2.0","id":5,"method":"tools/list"}' @{}
Check '无任何认证头即可通过' ($r.Status -eq 200 -and $null -ne ($r.Body | ConvertFrom-Json).result) $r.Body
$r = Send '{"jsonrpc":"2.0","id":6,"method":"tools/list"}' @{ 'Authorization' = 'Bearer whatever' }
Check '带 Authorization 头也不报错（服务端不看它）' ($r.Status -eq 200 -and $null -ne ($r.Body | ConvertFrom-Json).result) $r.Body

Write-Host "`n[8] DNS rebinding / Origin"
# 恶意 Host 必须被拒。走 SendRaw（手写报文）而不是 Send：理由见 SendRaw 上方。
$raw8 = SendRaw 'evil.example.com'
Check '恶意 Host 被拒 (-32001)' ($raw8 -match '\-32001') $raw8.Substring(0, [Math]::Min(200, $raw8.Length))

# 对照：**同一个通道**、合法 Host 必须放行。
# 少了这一条，一个"把什么请求都拒掉"的实现也能让上面那条通过 ——
# 单边检查永远分不清"正确拒绝"与"整体坏掉"。
$raw8ok = SendRaw "${HostName}:${Port}"
Check '对照：同一通道合法 Host 放行' ($raw8ok -match '"tools"') $raw8ok.Substring(0, [Math]::Min(200, $raw8ok.Length))

# Origin 是普通头，不受连接池那个问题影响，继续用 Send。
$h = @{ 'Origin' = 'http://evil.example.com' }
$r = Send '{"jsonrpc":"2.0","id":8,"method":"tools/list"}' $h
Check '任何 Origin 都被拒 (-32001)' (($r.Body | ConvertFrom-Json).error.code -eq -32001) $r.Body

Write-Host "`n[9] 限流（globalQps = 5，burst = 10）"
# 用**单个 JSON-RPC 批量请求**打 40 发：服务端顺序执行、几乎不耗时。
# 不要用 40 次独立 HTTP 请求 —— 每次往返的耗时足够把令牌按 5 QPS 回填满，
# 结果永远触发不了限流（这是本节第一版写成 PASS 的假象来源）。
#
# 注意：这里用字符串拼接而不是 -f 格式化 —— JSON 以裸 `{` 开头，在 -f 的
# 格式串里那是一个未转义的格式项，会抛 FormatException（异常被吞进
# error 流，表现为"循环体一次都没跑"，极难排查）。
$batch = @()
for ($i = 1; $i -le 40; $i++) {
  $batch += '{"jsonrpc":"2.0","id":' + (200 + $i) + ',"method":"tools/call","params":{"name":"novaai_health_status","arguments":{}}}'
}
$r = Send ('[' + ($batch -join ',') + ']') $h2
$body9 = $r.Body
if ($body9 -notmatch '^\s*\[') { $body9 = "[$body9]" }
$limited = ([regex]::Matches($body9, '"?-32009"?')).Count
$okCount = ([regex]::Matches($body9, '"result"')).Count
Write-Host "    ok=$okCount  rate_limited=$limited"
Check '触发限流 (-32009)' ($limited -gt 0) "ok=$okCount limited=$limited body=[$($body9.Substring(0, [Math]::Min(300, $body9.Length)))]"

Write-Host "`n[10] 会话复用"
Start-Sleep -Seconds 2
$r = Send '{"jsonrpc":"2.0","id":30,"method":"ping"}' $h2
$sid2 = ($r.Headers.GetValues('Mcp-Session-Id') | Select-Object -First 1)
Check '会话 ID 保持稳定' ($sid2 -eq $sid) "$sid -> $sid2"

Write-Host "`n[11] default 档位放行全部工具"
Start-Sleep -Seconds 2
$r = Send '{"jsonrpc":"2.0","id":40,"method":"tools/call","params":{"name":"novaai_config","arguments":{"action":"get"}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check 'novaai_config 不被档位拒绝（无 -32003）' ($j.error.code -ne -32003) $r.Body
$r = Send '{"jsonrpc":"2.0","id":41,"method":"tools/call","params":{"name":"novaai_shell","arguments":{"command":"id"}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check 'novaai_shell 不被档位拒绝（无 -32003）' ($j.error.code -ne -32003) $r.Body

Write-Host "`n[12] 受保护路径判定 (pathguard)"
Start-Sleep -Seconds 2
$r = Send '{"jsonrpc":"2.0","id":50,"method":"tools/call","params":{"name":"novaai_fs_write","arguments":{"action":"create","path":"/system/build.prop","content":"x"}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_write 拒绝 /system (PROTECTED_PATH)' ($txt -match 'PROTECTED_PATH') $r.Body
$r = Send '{"jsonrpc":"2.0","id":51,"method":"tools/call","params":{"name":"novaai_fs_write","arguments":{"action":"create","path":"/data/adb/modules/x/disable","content":""}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_write 拒绝模块目录 (PROTECTED_PATH)' ($txt -match 'PROTECTED_PATH') $r.Body
$r = Send '{"jsonrpc":"2.0","id":52,"method":"tools/call","params":{"name":"novaai_fs_write","arguments":{"action":"create","path":"/sdcard/Android/data/com.x/files/a","content":"x"}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_write 硬拒绝 /sdcard/Android/data (PROTECTED_PATH)' ($txt -match 'PROTECTED_PATH') $r.Body
$r = Send '{"jsonrpc":"2.0","id":53,"method":"tools/call","params":{"name":"novaai_fs_write","arguments":{"action":"create","path":"guard-ok.txt","content":"ok"}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_write 放行工作区路径' ($txt -notmatch 'PROTECTED_PATH') $r.Body

Write-Host "`n[13] 递归删除保护"
Start-Sleep -Seconds 2
$r = Send '{"jsonrpc":"2.0","id":60,"method":"tools/call","params":{"name":"novaai_fs_manage","arguments":{"action":"remove","path":"/data","recursive":true}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_manage 拒绝 rm -rf /data' ($txt -match 'PROTECTED_PATH') $r.Body
$r = Send '{"jsonrpc":"2.0","id":61,"method":"tools/call","params":{"name":"novaai_fs_manage","arguments":{"action":"remove","path":"/data/local/tmp/nova-guard-test","recursive":true}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_manage 放行 /data/local/tmp 下删除' ($txt -notmatch 'PROTECTED_PATH') $r.Body

Write-Host "`n[14] 上游聚合接口已接线（未配置上游时的空态）"
# 这里只验证"接口存在且空态可用"。真实上游的合并 / 转发 / 降级由
# Go 侧的 internal/upstream 测试与 internal/mcp 的端到端测试覆盖 ——
# 探针脚本里再起一个假上游进程会与那些测试重复，得不偿失。
$r = Send '{"jsonrpc":"2.0","id":70,"method":"tools/call","params":{"name":"novaai_upstream_status","arguments":{}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'novaai_upstream_status 可调用' ($null -ne $j.result.content) $r.Body
Check '空态返回 success' ($txt -match '"success":\s*true') $r.Body
Check '空态返回空数组' ($txt -match '"upstreams":\s*\[\s*\]') $r.Body

$r = Send '{"jsonrpc":"2.0","id":71,"method":"tools/call","params":{"name":"novaai_config","arguments":{"action":"probe_upstreams"}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'probe_upstreams 可调用' ($txt -match '"success":\s*true') $r.Body

$r = Send '{"jsonrpc":"2.0","id":72,"method":"tools/call","params":{"name":"novaai_config","arguments":{"action":"restart_upstream","name":"nope"}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'restart_upstream 未知上游返回 NOT_FOUND' ($txt -match 'NOT_FOUND') $r.Body

Write-Host "`n[15] 业务失败带 code / codeName / message 三件套"
# code 是英文标识符（给程序匹配，不改），codeName 是稳定中文名（给人看），
# message 是带细节的一次性原因。见 docs/errors.md 第 3 节。
$r = Send '{"jsonrpc":"2.0","id":80,"method":"tools/call","params":{"name":"novaai_fs_write","arguments":{"action":"create","path":"/system/build.prop","content":"x"}}}' $h2
$j = $r.Body | ConvertFrom-Json
$sc = $j.result.structuredContent
Check 'code 是英文标识符 PROTECTED_PATH' ($sc.code -eq 'PROTECTED_PATH') "code=$($sc.code)"
Check 'codeName 是中文名「路径受保护」' ($sc.codeName -eq '路径受保护') "codeName=$($sc.codeName)"
Check 'message 非空且带细节' (-not [string]::IsNullOrWhiteSpace($sc.message)) "message=[$($sc.message)]"

} finally {
  if ($proc) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 300
  }
}

Write-Host "`n==================== 结果 ===================="
Write-Host "PASS=$pass  FAIL=$fail"
if ($fail -gt 0) { exit 1 }
