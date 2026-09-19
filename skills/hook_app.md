# 应用 Hook 技能

## Xposed 模块管理

### 列出已安装模块
```
novaai_hook_xposed → action: list
```

### 启用/禁用模块
```
novaai_hook_xposed → action: enable, module: <模块名>
novaai_hook_xposed → action: disable, module: <模块名>
```

### 检查 LSPosed 状态
```
novaai_hook_xposed → action: status
```

## Frida Hook（需要安装 frida）

### 列出进程
```
novaai_hook_frida → action: list
```

### 附加到进程
```
novaai_hook_frida → action: attach, package: <包名>
```

### 执行 Hook 脚本
```
novaai_hook_frida → action: script, package: <包名>, script: "
Java.perform(function() {
    var MainActivity = Java.use('com.app.MainActivity');
    MainActivity.onCreate.implementation = function() {
        console.log('Hooked!');
        this.onCreate();
    };
});
"
```

## 常见场景

### 绕过 Root 检测
1. 找到检测类
2. Hook 检测方法
3. 返回 false

### 修改返回值
1. 定位目标方法
2. Hook implementation
3. 返回自定义值
