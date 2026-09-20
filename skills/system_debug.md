# 系统调试技能

> 本轮工具精简后，`novaai_property` / `novaai_setting` / `novaai_service` /
> `novaai_device_info` 等已删除。日志与进程有专用工具，其余走 `novaai_shell`。

## 日志查看

### Logcat

```
novaai_log → action: logcat, lines: 100
```

### 内核日志

```
novaai_log → action: kernel, lines: 50
```

### 模块日志 / MCP 日志

```
novaai_log → action: module, lines: 50
```

`novaai_log` 是读取日志的专用入口。要按 tag 过滤或持续跟踪，用 shell：

```
novaai_shell → command: "logcat -d -s AndroidRuntime:E | tail -100"
```

## 进程管理

### 查看进程

```
novaai_process → action: list, query: "app"
```

### 查看进程详情

```
novaai_process → action: info, pid: <PID>
```

### 发送信号

```
novaai_process → action: kill, pid: <PID>, signal: "TERM"
```

## 系统属性

`novaai_property` 已删除，用 shell 调 `getprop` / `setprop`：

```
# 读取单个属性
novaai_shell → command: "getprop ro.build.version.release"

# 列出属性（按前缀筛，避免刷屏）
novaai_shell → command: "getprop | grep -E 'ro\\.product|persist\\.sys'"

# 设置属性（persist.* 在重启后保留）
novaai_shell → command: "setprop persist.sys.timezone Asia/Shanghai"
```

## 系统设置

`novaai_setting` 已删除，用 shell 调 `settings`：

```
novaai_shell → command: "settings get global http_proxy"
novaai_shell → command: "settings put system screen_brightness 128"
novaai_shell → command: "settings list secure | head -50"
```

## 设备信息

```
novaai_capabilities → {}                      # 运行时能力 + 缺失命令清单
novaai_status       → {}                       # 服务版本 / 地址 / 档位 / 运行时间
novaai_shell        → command: "getprop ro.product.model; getprop ro.build.version.release"
```

## 常见场景

### 调试应用崩溃
1. `novaai_log → action: logcat, lines: 200`
2. 搜索 `FATAL` 或 `Exception`
3. 定位崩溃堆栈；需要 tag 过滤时用 `logcat -d -s AndroidRuntime:E`

### 检查系统状态
1. `novaai_shell → command: "dumpsys battery | head -20"`
2. `novaai_shell → command: "df -h /data /sdcard"`
3. `novaai_process → action: list`

### 自检本服务
1. `novaai_health_status → {}`
2. `novaai_diagnostics → action: collect`（写一份诊断报告到工作区）
