# 网络调试技能

> 本轮工具精简后，`novaai_network` 已删除。下面全部通过 `novaai_shell`
> 完成 —— Android 自带的 toybox 已提供大部分网络诊断命令。

## 网络诊断

### 查看网络接口

```
novaai_shell → command: "ip addr"
novaai_shell → command: "getprop | grep -E 'net\\.|wifi\\.' "
```

### 查看路由表

```
novaai_shell → command: "ip route"
```

### DNS 查询

Android 没有 `dig` / `nslookup`，用 `getprop` 读配置的 DNS：

```
novaai_shell → command: "getprop | grep -i dns"
novaai_shell → command: "ping -c 1 -W 2 <域名>"
```

### Ping 测试

```
novaai_shell → command: "ping -c 4 8.8.8.8"
```

### HTTP 请求

```
novaai_shell → command: "curl -sS -m 10 -o /dev/null -w '%{http_code}' https://api.example.com"
novaai_shell → command: "curl -sS -m 10 https://api.example.com/health"
```

没有 `curl` 时退到 `ping` / `nc`：

```
novaai_capabilities → {}     # 先确认 curl / nc / ping 是否存在
```

### 下载文件

优于手写 curl 的做法是用本服务的专用工具（它会做 SHA-256 校验与重试）：

```
novaai_download → url: "https://example.com/f.bin", destination: "/data/local/tmp/f.bin"
```

## 抓包与连接

### 查看连接

```
novaai_shell → command: "ss -tnp"
```

### 查看监听端口

```
novaai_shell → command: "ss -tlnp"
```

### WiFi 信息

```
novaai_shell → command: "dumpsys wifi | grep -iE 'mWifiInfo|SSID' | head"
```

### 代理设置

```
novaai_shell → command: "settings get global http_proxy"
novaai_shell → command: "settings put global http_proxy 127.0.0.1:8080"
novaai_shell → command: "settings put global http_proxy :0"     # 清除代理
```

## 常见场景

### 测试 API 连通性

```
novaai_shell → command: "curl -sS -m 5 -o /dev/null -w 'code=%{http_code} time=%{time_total}\\n' https://api.example.com/health"
```

### 诊断网络问题（按顺序）

```
novaai_shell → command: "ip addr"                 # 1. 有网卡 / 有 IP 吗
novaai_shell → command: "ip route"                # 2. 有默认路由吗
novaai_shell → command: "ping -c 2 192.168.1.1"   # 3. 网关通吗
novaai_shell → command: "ping -c 2 8.8.8.8"       # 4. 外网通吗
novaai_shell → command: "getprop | grep -i dns"   # 5. DNS 配了吗
```
