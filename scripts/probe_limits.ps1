# NovaAI-MCP 限制项生效性回归测试
#
# 覆盖两个"曾经是无消费者的摆设、现已接线"的配置项：
#   [1] limits.resultPreviewBytes —— 超限时截断 content、丢弃 structuredContent
#   [2] limits.resultPreviewBytes = 0 —— 不限制，保留 structuredContent
#   [3] shellTimeoutSeconds —— 由 Go 单元测试覆盖（见下）
#
# 为什么 [3] 不在这里做端到端：shell 系执行硬编码走 /system/bin/sh，
# Windows 上没有这个路径，命令根本起不来，无法观察超时行为。
# 该不变式由 src/internal/tools/v02/helpers_test.go 的
# TestShellTimeoutFromConfig 直接断言"配置是默认超时的唯一来源"。
#
# 脚本自行构建 daemon、生成隔离 state 目录并在结束时清理，不依赖设备。
# 用法: pwsh -File scripts/probe_limits.ps1
#
# 本服务不鉴权：所有请求都不带认证头。
#
# 判定口径：每条 Check 描述的是"期望行为"，FAIL 即缺陷。

param(
  [int]$Port = 15327,
  [string]$StateDir = "$env:TEMP\nova-limits-probe",
  [string]$GoExe = 'go',
  [int]$PreviewLimit = 300
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $env:TEMP 'novaaimcpd_limitsprobe.exe'
$pass = 0; $fail = 0
$proc = $null

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
# 审计目录与 pathguard 的保护前缀取自 config.stateDir（不是 -state 参数），
# 因此必须一起改，否则在本机（Windows）会落到当前盘的 \data\adb\... 下。
$cfg.stateDir = $StateDir
$cfg.resultPreviewBytes = $PreviewLimit
$cfg | ConvertTo-Json -Depth 12 | Set-Content $cfgPath -Encoding UTF8

$proc = Start-Process -FilePath $exe -ArgumentList @('-state', $StateDir) `
  -RedirectStandardOutput "$StateDir\boot2.log" -RedirectStandardError "$StateDir\boot2.err" `
  -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2

$base = "http://127.0.0.1:$Port/mcp"
$client = New-Object System.Net.Http.HttpClient
$client.Timeout = [TimeSpan]::FromSeconds(30)

function Send($body) {
  $req = New-Object System.Net.Http.HttpRequestMessage('POST', $base)
  $req.Content = New-Object System.Net.Http.StringContent($body, [Text.Encoding]::UTF8, 'application/json')
  $resp = $client.SendAsync($req).Result
  return $resp.Content.ReadAsStringAsync().Result
}

function Call($name, $argsJson) {
  return Send "{`"jsonrpc`":`"2.0`",`"id`":1,`"method`":`"tools/call`",`"params`":{`"name`":`"$name`",`"arguments`":$argsJson}}"
}

try {
  Write-Host "`n[1] resultPreviewBytes=$PreviewLimit：大结果被截断"
  $body = Call 'novaai_capabilities' '{}'
  $j = $body | ConvertFrom-Json

  Check '返回 content 数组' ($null -ne $j.result.content -and $j.result.content.Count -ge 1) $body
  $text = $j.result.content[0].text

  Check '正文含截断说明' ($text -match '结果已截断') "text 前 200 字=[$($text.Substring(0, [Math]::Min(200, $text.Length)))]"
  Check '丢弃 structuredContent（帧大小真的省了）' ($body -notmatch '"structuredContent"') '响应里仍带 structuredContent'
  Check 'isError 不为 true（截断不是失败）' ($j.result.isError -ne $true) "isError=$($j.result.isError)"

  $head = ($text -split "`n`n\[结果已截断", 2)[0]
  Check "截断后正文不超过上限 $PreviewLimit 字节" `
    ([Text.Encoding]::UTF8.GetByteCount($head) -le $PreviewLimit) `
    "实际 $([Text.Encoding]::UTF8.GetByteCount($head)) 字节"

  Write-Host "`n[2] 小结果不受影响：保留 structuredContent"
  $body2 = Call 'novaai_health_status' '{}'
  $j2 = $body2 | ConvertFrom-Json
  Check '返回 content' ($null -ne $j2.result.content) $body2
  Check '保留 structuredContent' ($body2 -match '"structuredContent"') '未超限却丢了 structuredContent'
  Check '无截断说明' ($body2 -notmatch '结果已截断') '未超限却出现截断说明'

  Write-Host "`n[3] shellTimeoutSeconds 由单元测试覆盖"
  Write-Host "    (Windows 无 /system/bin/sh，无法端到端观察；见 helpers_test.go)"
}
finally {
  if ($proc) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 300
  }
}

Write-Host "`n==================== 结果 ===================="
Write-Host "PASS=$pass  FAIL=$fail"
if ($fail -gt 0) { exit 1 }
