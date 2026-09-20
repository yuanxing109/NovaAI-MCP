# NovaAI-MCP 会话行为回归测试
#
# 覆盖会话身份相关的四类行为：
#   [1] 会话分配与复用（initialize 建会话、带 sid 复用、响应头稳定）
#   [2] 无状态请求不占用会话名额（不实现会话的客户端不应被挡）
#   [3] 会话数上限是否生效（超过 32 个真正创建的会话返回 -32014）
#   [4] 会话不携带权限（复用旧 sid 不改变准入结果）
#   [5] 批量请求中单个工具编码失败是否污染整批
#
# 脚本自行构建 daemon、生成隔离 state 目录并在结束时清理，不依赖设备。
# 用法: pwsh -File scripts/probe_session.ps1
#
# 本服务不鉴权：所有请求都不带认证头。
#
# 判定口径：每条 Check 描述的是"期望行为"，FAIL 即缺陷。
#
# 已删除的旧覆盖面（记在此处避免以后重复实现）：
#   - 会话级限流：按身份的限流层已整层删除，只剩全局 / shell / 并发三层
#   - 空闲超时自愈：空闲 30 分钟 + 巡检 5 分钟已收敛为常量，无法在测试里压缩
#   - 请求体上限：MaxRequestBytes 是常量 64 MiB，构造那么大的请求体不现实
#   - novaai_session_list / novaai_session_status：工具已裁掉

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
$cfg.listen = "127.0.0.1:$Port"
# 审计目录与 pathguard 的保护前缀取自 config.stateDir（不是 -state 参数）。
$cfg.stateDir = $StateDir
# 全局限流抬到不可能触发，让唯一可能出现的错误码是会话相关的 -32014。
$cfg.limits.globalQps = 100000
$cfg.limits.shellQps = 100000
$cfg | ConvertTo-Json -Depth 12 | Set-Content $cfgPath -Encoding UTF8

$proc = Start-Process -FilePath $exe -ArgumentList @('-state', $StateDir) `
  -RedirectStandardOutput "$StateDir\boot2.log" -RedirectStandardError "$StateDir\boot2.err" `
  -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2

$base = "http://127.0.0.1:$Port/mcp"
$client = New-Object System.Net.Http.HttpClient
$client.Timeout = [TimeSpan]::FromSeconds(20)

function Send($body, $sid) {
  $req = New-Object System.Net.Http.HttpRequestMessage('POST', $base)
  $req.Content = New-Object System.Net.Http.StringContent($body, [Text.Encoding]::UTF8, 'application/json')
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

$init = '{"jsonrpc":"2.0","id":80,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}'
$call = '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"novaai_health_status","arguments":{}}}'

try {
  Write-Host "`n[1] 会话分配与复用"
  $r = Send $init $null
  Check 'HTTP 200' ($r.Status -eq 200) $r.Status
  Check 'initialize 返回 Mcp-Session-Id' ($null -ne $r.Sid) "sid=$($r.Sid)"
  $sid = $r.Sid

  $r2 = Send '{"jsonrpc":"2.0","id":3,"method":"ping"}' $sid
  Check '带 sid 复用时响应头稳定' ($r2.Sid -eq $sid) "$sid -> $($r2.Sid)"

  Write-Host "`n[2] 无状态请求不占会话名额"
  # 这正是本节存在的理由：早期实现给每个请求都建会话，导致不实现会话的
  # 客户端调几十次就被挡到空闲超时（30 分钟）才恢复。
  $c = @(); for ($i = 0; $i -lt 40; $i++) { $c += (ErrCode (Send $call $null).Body) }
  Check '无状态请求不出现 -32014' `
    (($c | Where-Object { $_ -eq -32014 }).Count -eq 0) "codes=$($c -join ',')"
  Check '无状态请求正常返回 content' `
    (($c | Where-Object { $_ -eq 'ok' }).Count -eq 40) "codes=$($c -join ',')"

  Write-Host "`n[3] 会话数上限 32"
  # [1] 已建 1 个，再建 40 个必然撞上限。
  $e = @(); for ($i = 0; $i -lt 40; $i++) { $e += (ErrCode (Send $init $null).Body) }
  Check '创建会话超过上限返回 -32014' (($e | Where-Object { $_ -eq -32014 }).Count -gt 0) "codes=$($e -join ',')"
  Check '上限之前的会话创建成功' (($e | Where-Object { $_ -eq 'ok' }).Count -ge 30) "ok=$((($e | Where-Object { $_ -eq 'ok' }).Count))"

  Write-Host "`n[4] 会话不携带权限"
  # 复用旧 sid 调一个"历史上会被档位拒绝"的工具：default 现在放行全部，
  # 因此不应出现 -32003。会话对象只有 ID/CreatedAt/LastSeen，没有 profile。
  $r = Send '{"jsonrpc":"2.0","id":41,"method":"tools/call","params":{"name":"novaai_config","arguments":{"action":"get"}}}' $sid
  Check '复用 sid 不触发 -32003（会话无权限）' ((ErrCode $r.Body) -ne -32003) $r.Body

  Write-Host "`n[5] 批量请求隔离性"
  $r = Send '[{"jsonrpc":"2.0","id":11,"method":"ping"},{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"novaai_does_not_exist","arguments":{}}},{"jsonrpc":"2.0","id":13,"method":"ping"}]' $null
  Check '批量响应体非空（单个工具失败不污染整批）' ($r.Body.Length -gt 0) "长度=$($r.Body.Length)"
  Check '批量中其余请求仍返回' ($r.Body -match '"id":11' -and $r.Body -match '"id":13') "body=[$($r.Body)]"
}
finally {
  Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 300
}

Write-Host "`n==================== 结果 ===================="
Write-Host "PASS=$pass  FAIL=$fail"
if ($fail -gt 0) { exit 1 }
