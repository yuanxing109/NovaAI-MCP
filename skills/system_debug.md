# 系统调试技能

## 日志查看

### Logcat 日志
```
novaai_log → action: logcat, lines: 100
```

### 内核日志
```
novaai_log → action: kernel, lines: 50
```

### 模块日志
```
novaai_log → action: module, lines: 50
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

### 读取属性
```
novaai_property → action: get, key: "ro.build.version.release"
```

### 列出所有属性
```
novaai_property → action: list
```

### 设置属性
```
novaai_property → action: set, key: "persist.sys.timezone", value: "Asia/Shanghai"
```

## 常见场景

### 调试应用崩溃
1. novaai_log → action: logcat, lines: 200
2. 搜索 "FATAL" 或 "Exception"
3. 定位崩溃堆栈

### 检查系统状态
1. novaai_device_info → action: battery
2. novaai_device_info → action: storage
3. novaai_process → action: list
