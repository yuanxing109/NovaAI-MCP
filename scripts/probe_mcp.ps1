# NovaAI-MCP 协议合规回归测试
#
# 默认自举：脚本自行构建 daemon、生成隔离 state 目录并在结束时清理，不依赖设备。
#   pwsh -File scripts/probe_mcp.ps1
#
# 也可指向外部已在运行的服务（例如设备上的实例）：
#   pwsh -File scripts/probe_mcp.ps1 -Port 5322 -Token (Get-Content /path/to/token -Raw).Trim()
#
# 覆盖：initialize 协商、通知无响应、tools/list、tools/call 的 MCP content 包装、
#       未知工具错误码、批量请求、鉴权、DNS rebinding（Host 校验）、Origin 校验、
#       限流、会话复用、profile 门禁、pathguard。

param(
  [int]$Port = 15322,
  [string]$Token = '',
  [string]$HostName = '127.0.0.1',
  [int]$ExpectTools = 61,
  [string]$StateDir = "$env:TEMP\nova-mcp-probe",
  [string]$GoExe = 'go'
)

$ErrorActionPreference = 'Stop'
$base = "http://${HostName}:${Port}/mcp"
$pass = 0; $fail = 0

function Check($name, $cond, $detail) {
  if ($cond) { $script:pass++; Write-Host ("  PASS  {0}" -f $name) }
  else { $script:fail++; Write-Host ("  FAIL  {0}  -> {1}" -f $name, $detail) }
}

# ---- 自举：未提供 -Token 时自行构建并启动一个隔离 daemon ----
# 提供 -Token 时按"外部已在运行的服务"处理，不做构建与清理。
$root = Split-Path -Parent $PSScriptRoot
$proc = $null
if (-not $Token) {
  $exe = Join-Path $env:TEMP 'novaaimcpd_mcpprobe.exe'
  Push-Location (Join-Path $root 'src')
  & $GoExe build -o $exe ./cmd/novaaimcpd
  if ($LASTEXITCODE -ne 0) { Pop-Location; Write-Host 'BUILD FAILED'; exit 1 }
  Pop-Location

  if (Test-Path $StateDir) { Remove-Item -Recurse -Force $StateDir }
  New-Item -ItemType Directory -Force -Path $StateDir | Out-Null

  # 先跑一次让 daemon 生成默认 config.json，改掉端口后再正式启动。
  $p0 = Start-Process -FilePath $exe -ArgumentList @('-state', $StateDir) `
    -RedirectStandardOutput "$StateDir\boot1.log" -RedirectStandardError "$StateDir\boot1.err" `
    -PassThru -WindowStyle Hidden
  Start-Sleep -Seconds 2
  Stop-Process -Id $p0.Id -Force -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 500

  $cfgPath = Join-Path $StateDir 'config.json'
  if (-not (Test-Path $cfgPath)) { Write-Host "配置未生成: $cfgPath"; exit 1 }
  $cfg = Get-Content $cfgPath -Raw | ConvertFrom-Json
  $cfg.network.port = $Port
  $cfg | ConvertTo-Json -Depth 12 | Set-Content $cfgPath -Encoding UTF8

  $proc = Start-Process -FilePath $exe -ArgumentList @('-state', $StateDir) `
    -RedirectStandardOutput "$StateDir\boot2.log" -RedirectStandardError "$StateDir\boot2.err" `
    -PassThru -WindowStyle Hidden
  Start-Sleep -Seconds 2
  $Token = (Get-Content (Join-Path $StateDir 'token') -Raw).Trim()
}

try {

$client = New-Object System.Net.Http.HttpClient
$client.Timeout = [TimeSpan]::FromSeconds(30)

function Send($body, $headers, $hostOverride) {
  $req = New-Object System.Net.Http.HttpRequestMessage('POST', $base)
  $req.Content = New-Object System.Net.Http.StringContent($body, [Text.Encoding]::UTF8, 'application/json')
  if ($hostOverride) { $req.Headers.Host = $hostOverride }
  foreach ($k in $headers.Keys) { [void]$req.Headers.TryAddWithoutValidation($k, $headers[$k]) }
  $resp = $client.SendAsync($req).Result
  $text = $resp.Content.ReadAsStringAsync().Result
  return [pscustomobject]@{ Status = [int]$resp.StatusCode; Body = $text; Headers = $resp.Headers }
}

$auth = @{}
if ($Token) { $auth['Authorization'] = "Bearer $Token" }

Write-Host "`n[1] initialize"
$r = Send '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}' $auth
$j = $r.Body | ConvertFrom-Json
Check 'HTTP 200' ($r.Status -eq 200) $r.Status
Check 'protocolVersion 协商' ($j.result.protocolVersion -eq '2025-06-18') $j.result.protocolVersion
Check '声明 tools 能力' ($null -ne $j.result.capabilities.tools) '缺少 tools 能力'
Check '返回 Mcp-Session-Id' ($null -ne $r.Headers.GetValues('Mcp-Session-Id')) '缺少 session 头'
$sid = ($r.Headers.GetValues('Mcp-Session-Id') | Select-Object -First 1)

$h2 = @{}
if ($Token) { $h2['Authorization'] = "Bearer $Token" }
$h2['Mcp-Session-Id'] = $sid

Write-Host "`n[2] notifications/initialized（通知不应有响应体）"
$r = Send '{"jsonrpc":"2.0","method":"notifications/initialized"}' $h2
Check 'HTTP 202 且空响应体' ($r.Status -eq 202 -and $r.Body.Trim() -eq '') "status=$($r.Status) body=[$($r.Body)]"

Write-Host "`n[3] tools/list"
$r = Send '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' $h2
$j = $r.Body | ConvertFrom-Json
Check "工具数 = $ExpectTools" ($j.result.tools.Count -eq $ExpectTools) "实际 $($j.result.tools.Count)"
Check '不存在 session_arm 工具' (($j.result.tools | Where-Object { $_.name -like '*session_arm*' }).Count -eq 0) '发现 session_arm'

Write-Host "`n[4] tools/call 正常工具（MCP content 包装）"
$r = Send '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"novaai_session_status","arguments":{}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check '返回 content 数组' ($null -ne $j.result.content -and $j.result.content.Count -ge 1) $r.Body
Check 'content[0].type = text' ($j.result.content[0].type -eq 'text') $j.result.content[0].type
Check 'isError 不为 true' ($j.result.isError -ne $true) "isError=$($j.result.isError)"

Write-Host "`n[5] tools/call 未知工具"
$r = Send '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"novaai_does_not_exist","arguments":{}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check 'code = -32015' ($j.error.code -eq -32015) $r.Body

Write-Host "`n[6] 批量请求（含通知，应被过滤）"
$r = Send '[{"jsonrpc":"2.0","id":10,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":11,"method":"ping"}]' $h2
$j = $r.Body | ConvertFrom-Json
Check '返回数组且过滤通知' ($j.Count -eq 2) "返回 $($j.Count) 项"

Write-Host "`n[7] 鉴权"
$r = Send '{"jsonrpc":"2.0","id":5,"method":"tools/list"}' @{}
Check '无 token 被拒 (-32001)' (($r.Body | ConvertFrom-Json).error.code -eq -32001) $r.Body
$r = Send '{"jsonrpc":"2.0","id":6,"method":"tools/list"}' @{ 'Authorization' = 'Bearer wrongtoken' }
Check '错误 token 被拒 (-32001)' (($r.Body | ConvertFrom-Json).error.code -eq -32001) $r.Body

Write-Host "`n[8] DNS rebinding / Origin"
$r = Send '{"jsonrpc":"2.0","id":7,"method":"tools/list"}' $auth 'evil.example.com'
Check '恶意 Host 被拒 (-32001)' (($r.Body | ConvertFrom-Json).error.code -eq -32001) $r.Body
$h = @{ 'Origin' = 'http://evil.example.com' }
if ($Token) { $h['Authorization'] = "Bearer $Token" }
$r = Send '{"jsonrpc":"2.0","id":8,"method":"tools/list"}' $h
Check '未白名单 Origin 被拒 (-32001)' (($r.Body | ConvertFrom-Json).error.code -eq -32001) $r.Body

Write-Host "`n[9] 限流（perTool novaai_log = 2 QPS / burst 4）"
$codes = @()
for ($i = 0; $i -lt 12; $i++) {
  $rr = Send '{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"novaai_log","arguments":{"action":"logcat","lines":1}}}' $h2
  $jj = $rr.Body | ConvertFrom-Json
  if ($null -ne $jj.error) { $codes += $jj.error.code } else { $codes += 'ok' }
}
Check '触发限流 (-32009)' (($codes | Where-Object { $_ -eq -32009 }).Count -gt 0) "codes=$($codes -join ',')"

Write-Host "`n[10] 会话复用"
$r = Send '{"jsonrpc":"2.0","id":30,"method":"ping"}' $h2
$sid2 = ($r.Headers.GetValues('Mcp-Session-Id') | Select-Object -First 1)
Check '会话 ID 保持稳定' ($sid2 -eq $sid) "$sid -> $sid2"

Write-Host "`n[11] profile 权限校验"
$r = Send '{"jsonrpc":"2.0","id":40,"method":"tools/call","params":{"name":"novaai_config","arguments":{"action":"get"}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check 'default 拒绝 novaai_config (-32003)' ($j.error.code -eq -32003) $r.Body
$r = Send '{"jsonrpc":"2.0","id":41,"method":"tools/call","params":{"name":"novaai_shell","arguments":{"command":"id"}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check 'default 拒绝 novaai_shell (-32003)' ($j.error.code -eq -32003) $r.Body
$r = Send '{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"novaai_auth_status","arguments":{}}}' $h2
$j = $r.Body | ConvertFrom-Json
Check '低风险工具正常返回' ($null -ne $j.result.content) $r.Body

Write-Host "`n[12] 受保护路径判定 (pathguard)"
$r = Send '{"jsonrpc":"2.0","id":50,"method":"tools/call","params":{"name":"novaai_fs_write","arguments":{"action":"create","path":"/system/build.prop","content":"x"}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_write 拒绝 /system (PROTECTED_PATH)' ($txt -match 'PROTECTED_PATH') $r.Body
$r = Send '{"jsonrpc":"2.0","id":51,"method":"tools/call","params":{"name":"novaai_fs_write","arguments":{"action":"create","path":"/data/adb/modules/x/disable","content":""}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_write 拒绝模块目录 (PROTECTED_PATH)' ($txt -match 'PROTECTED_PATH') $r.Body
$r = Send '{"jsonrpc":"2.0","id":52,"method":"tools/call","params":{"name":"novaai_fs_write","arguments":{"action":"create","path":"guard-ok.txt","content":"ok"}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_write 放行工作区路径' ($txt -notmatch 'PROTECTED_PATH') $r.Body

Write-Host "`n[13] 递归删除保护"
$r = Send '{"jsonrpc":"2.0","id":60,"method":"tools/call","params":{"name":"novaai_fs_manage","arguments":{"action":"remove","path":"/data","recursive":true,"confirmDangerous":true}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_manage 拒绝 rm -rf /data' ($txt -match 'PROTECTED_PATH') $r.Body
$r = Send '{"jsonrpc":"2.0","id":61,"method":"tools/call","params":{"name":"novaai_fs_manage","arguments":{"action":"remove","path":"/data/local/tmp/nova-guard-test","recursive":true,"confirmDangerous":true}}}' $h2
$j = $r.Body | ConvertFrom-Json
$txt = ($j.result.content | Select-Object -First 1).text
Check 'fs_manage 放行 /data/local/tmp 下删除' ($txt -notmatch 'PROTECTED_PATH') $r.Body

} finally {
  if ($proc) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 300
  }
}

Write-Host "`n==================== 结果 ===================="
Write-Host "PASS=$pass  FAIL=$fail"
if ($fail -gt 0) { exit 1 }
