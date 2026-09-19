# NovaAI-MCP 会话行为回归测试
#
# 覆盖会话身份相关的四类行为：
#   [1] novaai_session_list 能否被 JSON 编码
#   [2] 会话级限流在复用 sid 时是否生效
#   [3] 会话级限流在省略 sid 时是否仍生效（会话身份是否可被客户端伪造绕过）
#   [4] 会话数上限是否生效、是否自愈
#   [5] 批量请求中单个工具编码失败是否污染整批
#
# 脚本自行构建 daemon、生成隔离 state 目录并在结束时清理，不依赖设备。
# 用法: pwsh -File scripts/probe_session.ps1
#
# 判定口径：每条 Check 描述的是"期望行为"，FAIL 即缺陷。

param(
  [int]$Port = 15325,
  [string]$StateDir = "$env:TEMP\nova-session-probe",
  [string]$GoExe = 'go'
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $env:TEMP 'novaaimcpd_sessionprobe.exe'
$pass = 0; $fail = 0

function Check($name, $cond, $detail) {
  if ($cond) { $script:pass++; Write-Host ("  PASS  {0}" -f $name) }
  else { $script:fail++; Write-Host ("  FAIL  {0}  -> {1}" -f $name, $detail) }
}

# ---- 构建 ----
Push-Location (Join-Path $root 'src')
& $GoExe build -o $exe ./cmd/novaaimcpd
if ($LASTEXITCODE -ne 0) { Pop-Location; Write-Host 'BUILD FAILED'; exit 1 }
Pop-Location

# ---- 隔离 state 目录，先生成默认配置再改写 ----
if (Test-Path $StateDir) { Remove-Item -Recurse -Force $StateDir }
New-Item -ItemType Directory -Force -Path $StateDir | Out-Null

$p1 = Start-Process -FilePath $exe -ArgumentList @('-state', $StateDir) `
  -RedirectStandardOutput "$StateDir\boot1.log" -RedirectStandardError "$StateDir\boot1.err" `
  -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2
Stop-Process -Id $p1.Id -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500

$cfgPath = Join-Path $StateDir 'config.json'
if (-not (Test-Path $cfgPath)) { Write-Host "配置未生成: $cfgPath"; exit 1 }

$cfg = Get-Content $cfgPath -Raw | ConvertFrom-Json
$cfg.network.port = $Port
# 全局限流抬到不可能触发，perTool 清空：让唯一可能生效的只有会话级限流，
# 这样 [3] 的结论不会被全局桶干扰。
$cfg.rateLimit.global.qps = 100000
$cfg.rateLimit.global.burst = 100000
$cfg.rateLimit.perSession.qps = 1
$cfg.rateLimit.perSession.burst = 1
$cfg.rateLimit.perTool = @{}
$cfg.session.maxSessions = 10
$cfg.session.idleTimeoutSeconds = 3
$cfg.session.sweepIntervalSeconds = 1
# 请求体上限压到 1 KiB，便于用一个小请求验证"超限时的报错语义"
$cfg.limits.maxRequestBytes = 1024
$cfg | ConvertTo-Json -Depth 12 | Set-Content $cfgPath -Encoding UTF8

$p2 = Start-Process -FilePath $exe -ArgumentList @('-state', $StateDir) `
  -RedirectStandardOutput "$StateDir\boot2.log" -RedirectStandardError "$StateDir\boot2.err" `
  -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2
$token = (Get-Content (Join-Path $StateDir 'token') -Raw).Trim()

$base = "http://127.0.0.1:$Port/mcp"
$client = New-Object System.Net.Http.HttpClient
$client.Timeout = [TimeSpan]::FromSeconds(20)

function Send($body, $sid) {
  $req = New-Object System.Net.Http.HttpRequestMessage('POST', $base)
  $req.Content = New-Object System.Net.Http.StringContent($body, [Text.Encoding]::UTF8, 'application/json')
  [void]$req.Headers.TryAddWithoutValidation('Authorization', "Bearer $token")
  if ($sid) { [void]$req.Headers.TryAddWithoutValidation('Mcp-Session-Id', $sid) }
  $resp = $client.SendAsync($req).Result
  return [pscustomobject]@{
    Status = [int]$resp.StatusCode
    Body   = $resp.Content.ReadAsStringAsync().Result
    Sid    = if ($resp.Headers.Contains('Mcp-Session-Id')) { ($resp.Headers.GetValues('Mcp-Session-Id') | Select-Object -First 1) } else { $null }
  }
}

function ErrCode($body) {
  try { $j = $body | ConvertFrom-Json } catch { return 'unparseable' }
  if ($null -ne $j.error) { return $j.error.code }
  return 'ok'
}

$call = '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"novaai_session_status","arguments":{}}}'

try {
  Write-Host "`n[1] novaai_session_list 编码"
  $r = Send '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"novaai_session_list","arguments":{}}}' $null
  Check 'HTTP 200' ($r.Status -eq 200) $r.Status
  Check '响应体非空' ($r.Body.Length -gt 0) "长度=$($r.Body.Length)"
  Check '响应体是合法 JSON' ($r.Body.Length -gt 0 -and (ErrCode $r.Body) -ne 'unparseable') "body=[$($r.Body)]"

  Write-Host "`n[2] novaai_session_status 对照"
  Start-Sleep -Seconds 2   # perSession qps=1，等 [1] 消耗的令牌回填
  $r = Send '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"novaai_session_status","arguments":{}}}' $null
  Check '正常返回 content' ($r.Body -match '"content"') $r.Body

  Write-Host "`n[3] 会话级限流（perSession qps=1 burst=1）"
  Start-Sleep -Seconds 2
  $sid = (Send '{"jsonrpc":"2.0","id":3,"method":"ping"}' $null).Sid
  $with = @(); for ($i = 0; $i -lt 6; $i++) { $with += (ErrCode (Send $call $sid).Body) }
  Check '复用 sid 时触发 -32009' (($with | Where-Object { $_ -eq -32009 }).Count -gt 0) "codes=$($with -join ',')"

  Start-Sleep -Seconds 2
  $without = @(); for ($i = 0; $i -lt 6; $i++) { $without += (ErrCode (Send $call $null).Body) }
  Check '省略 sid 时仍触发 -32009（会话身份不可被客户端绕过）' `
    (($without | Where-Object { $_ -eq -32009 }).Count -gt 0) "codes=$($without -join ',')"

  Write-Host "`n[4] 会话数上限 maxSessions=10"
  Start-Sleep -Seconds 2
  # (a) 无状态请求（不带 sid、不含 initialize）不应占用会话名额。
  #     这正是 [4b] 存在的理由：早期实现给每个请求都建会话，导致不实现
  #     会话的客户端调 32 次就被挡半小时。
  $c = @(); for ($i = 0; $i -lt 14; $i++) { $c += (ErrCode (Send $call $null).Body) }
  Check '无状态请求不占会话名额（不出现 -32014）' `
    (($c | Where-Object { $_ -eq -32014 }).Count -eq 0) "codes=$($c -join ',')"

  Start-Sleep -Seconds 2
  # (b) 真正创建会话（initialize）才应该撞上限
  $init = '{"jsonrpc":"2.0","id":80,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}'
  $e = @(); for ($i = 0; $i -lt 20; $i++) { $e += (ErrCode (Send $init $null).Body) }
  Check '创建会话超过上限返回 -32014' (($e | Where-Object { $_ -eq -32014 }).Count -gt 0) "codes=$($e -join ',')"

  Write-Host "  等待 idle(3s)+sweep(1s)+余量 7s ..."
  Start-Sleep -Seconds 7
  $d = @(); for ($i = 0; $i -lt 4; $i++) { $d += (ErrCode (Send $call $null).Body) }
  Check '空闲会话被回收后恢复可用' (($d | Where-Object { $_ -eq 'ok' }).Count -gt 0) "codes=$($d -join ',')"

  Write-Host "`n[5] 批量请求隔离性"
  Start-Sleep -Seconds 2
  $r = Send '[{"jsonrpc":"2.0","id":11,"method":"ping"},{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"novaai_session_list","arguments":{}}},{"jsonrpc":"2.0","id":13,"method":"ping"}]' $null
  Check '批量响应体非空（单个工具失败不污染整批）' ($r.Body.Length -gt 0) "长度=$($r.Body.Length)"
  Check '批量中其余请求仍返回' ($r.Body -match '"id":11' -and $r.Body -match '"id":13') "body=[$($r.Body)]"

  Write-Host "`n[6] 请求体上限（maxRequestBytes=1024）"
  Start-Sleep -Seconds 2
  $pad = 'A' * 2000
  $big = "{`"jsonrpc`":`"2.0`",`"id`":70,`"method`":`"ping`",`"params`":{`"pad`":`"$pad`"}}"
  Check '构造的请求体确实超过上限' ($big.Length -gt 1024) "长度=$($big.Length)"
  $r = Send $big $null
  $code = ErrCode $r.Body
  $msg = ''
  try { $msg = ($r.Body | ConvertFrom-Json).error.message } catch { $msg = $r.Body }
  Write-Host "    code=$code message=[$msg]"
  Check '超限报错语义明确（提示请求体过大，而非 JSON 解析失败）' `
    ($msg -notmatch 'JSON 解析失败') "message=[$msg]"
}
finally {
  Stop-Process -Id $p2.Id -Force -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 300
}

Write-Host "`n==================== 结果 ===================="
Write-Host "PASS=$pass  FAIL=$fail"
if ($fail -gt 0) { exit 1 }
