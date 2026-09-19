# 网络调试技能

## 网络诊断

### 查看网络接口
```
novaai_network → action: interfaces
```

### 查看路由表
```
novaai_network → action: routes
```

### DNS 查询
```
novaai_network → action: dns
```

### Ping 测试
```
novaai_network → action: ping, host: "8.8.8.8"
```

### HTTP 请求
```
novaai_network → action: http, url: "https://api.example.com", method: "GET"
```

## 抓包分析

### 查看连接
```
novaai_network → action: connections
```

### 查看端口
```
novaai_network → action: ports
```

### WiFi 信息
```
novaai_network → action: wifi
```

## 常见场景

### 测试 API 连通性
```
novaai_network → action: http, url: "https://api.example.com/health", method: "GET"
```

### 检查代理设置
```
novaai_network → action: proxy
```

### 诊断网络问题
1. novaai_network → action: interfaces
2. novaai_network → action: routes
3. novaai_network → action: ping, host: "8.8.8.8"
4. novaai_network → action: dns
